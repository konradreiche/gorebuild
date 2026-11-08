package history

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/konradreiche/gorebuild/internal/profile"
)

type replayConfig struct {
	root         string
	cwd          string // repository-relative invocation directory
	target       string // relative to invocation directory
	targetDir    string // repository-relative target directory
	toolchain    string
	version      string
	env          []string
	flags        []string
	reason       string // unsupported global configuration, retained for each sample
	hashPackages []string
}

func replayHistory(ctx context.Context, report *Report, options Options, cwd, goRoot string) {
	selected := sampleEvents(report, options.Replay)
	config := replayConfig{root: report.Metadata.Repository, toolchain: filepath.Join(goRoot, "bin", "go"), version: report.Metadata.GoVersion, flags: options.BuildFlags, hashPackages: options.cacheHashPackages}
	report.Replay = &ReplaySummary{
		Requested: options.Replay, Selected: len(selected), Toolchain: config.toolchain,
		Sampling:    "chronological endpoints first, then round-robin frequent current-target packages, broad reach, multi-package events and chronological quantile anchors; prefer distinct package/input cohorts in four time bands before repeated coverage; duplicate events merged; --package restricts candidates; execute in first-parent log order; failures are not replaced",
		CachePolicy: "fresh isolated GOCACHE per event; cold parent, verified warm parent, then commit in the same worktree/cache/output path; separate preflight cache; shared module downloads",
	}
	var cwdInside bool
	config.cwd, cwdInside = repositoryDir(config.root, cwd)
	if !cwdInside {
		config.reason = "invocation directory is outside the repository"
	}
	for _, node := range report.Graph {
		if node.ImportPath == report.Metadata.ImportPath {
			var inside bool
			config.targetDir, inside = repositoryDir(config.root, node.Dir)
			if !inside {
				config.reason = "target is outside the repository"
			}
		}
	}
	if config.targetDir == "" {
		config.reason = "target is not a repository package"
	}
	relative, err := filepath.Rel(filepath.FromSlash(config.cwd), filepath.FromSlash(config.targetDir))
	if err != nil {
		config.reason = err.Error()
	}
	config.target = "./" + filepath.ToSlash(relative)
	if report.Metadata.ImportPath == "command-line-arguments" {
		config.reason = "file-list targets are not supported for historical replay; select a package"
	}
	if report.Metadata.GOWORK != "" && report.Metadata.GOWORK != "off" {
		config.reason = "workspace replay is excluded; use GOWORK=off and a self-contained module"
	}
	if report.Metadata.BuildEnvironment["GOCACHEPROG"] != "" {
		config.reason = "external GOCACHEPROG is unsupported for isolated replay; disable it to prevent cross-replay cache reuse"
	}
	for _, name := range []string{"overlay", "modfile", "pkgdir", "toolexec"} {
		if strings.Contains(report.Metadata.GOFLAGS, "-"+name) {
			config.reason = "replay excludes path-dependent -" + name + " configuration"
		}
		for _, flag := range options.BuildFlags {
			key, _, _ := strings.Cut(strings.TrimLeft(flag, "-"), "=")
			if key == name {
				config.reason = "replay excludes path-dependent -" + name + " configuration"
			}
		}
	}
	config.env = replayEnv(report.Metadata, goRoot)
	if options.CompareVCS {
		report.VCSComparison = &VCSComparisonSummary{}
	}
	for order, selection := range selected {
		event := report.Events[selection.Index]
		noVCS := config
		noVCS.flags = append(slices.Clone(config.flags), "-buildvcs=false")
		if options.CompareVCS && order%2 == 1 {
			event.NoVCSReplay = replayConfiguredEvent(ctx, noVCS, event, selection.Reasons)
			event.Replay = replayConfiguredEvent(ctx, config, event, selection.Reasons)
			event.ReplayOrder = []string{"no-vcs", "normal"}
		} else {
			event.Replay = replayConfiguredEvent(ctx, config, event, selection.Reasons)
			if options.CompareVCS {
				event.NoVCSReplay = replayConfiguredEvent(ctx, noVCS, event, selection.Reasons)
				event.ReplayOrder = []string{"normal", "no-vcs"}
			}
		}
		if options.CompareVCS {
			event.VCSComparison = compareReplay(event.Replay, event.NoVCSReplay)
		}
		report.Events[selection.Index] = event
	}
	summarizeMeasurements(report)
}

func replayConfiguredEvent(ctx context.Context, config replayConfig, event Event, reasons []string) *ReplayResult {
	var result *ReplayResult
	if ctx.Err() != nil {
		result = &ReplayResult{Status: "excluded", Stage: "cancelled", Reason: ctx.Err().Error()}
	} else if config.reason != "" {
		result = &ReplayResult{Status: "excluded", Stage: "configuration", Reason: config.reason}
	} else {
		result = replayEvent(ctx, config, event)
	}
	if len(result.ConfigurationReview) == 0 && (result.Stage == "configuration" || result.Stage == "cancelled") {
		recordUncertainConfiguration(result, event, event.Commit)
	}
	result.SelectionReasons = reasons
	result.BuildTarget = config.target
	result.WorkingDirectory = config.cwd
	result.BuildFlags = config.flags
	return result
}

func replayEnv(metadata Metadata, goRoot string) []string {
	overrides := map[string]string{
		"GOTOOLCHAIN": "local", "GOROOT": goRoot, "GOWORK": "off",
		"GOFLAGS": metadata.GOFLAGS, "GOOS": metadata.GOOS, "GOARCH": metadata.GOARCH,
	}
	maps.Copy(overrides, metadata.BuildEnvironment)
	var env []string
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if _, replaced := overrides[key]; !replaced && !gitLocationVariable(key) {
			env = append(env, entry)
		}
	}
	var keys []string
	for key := range overrides {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		env = append(env, key+"="+overrides[key])
	}
	return env
}

func replayEvent(ctx context.Context, config replayConfig, event Event) (result *ReplayResult) {
	result = &ReplayResult{Status: "excluded", Stage: "selection", BuildTarget: config.target, WorkingDirectory: config.cwd, BuildFlags: config.flags}
	if len(event.Parents) == 0 {
		result.Reason = "no available first parent (root or shallow boundary); no comparable baseline"
		return result
	}
	if _, err := replayGit(ctx, config.root, "cat-file", "-e", event.Parents[0]+"^{commit}"); err != nil {
		result.Reason = "first parent unavailable: " + err.Error()
		return result
	}
	temporary, err := os.MkdirTemp("", "gorebuild-replay-*")
	if err != nil {
		result.Status = "failed"
		result.Reason = err.Error()
		return result
	}
	defer os.RemoveAll(temporary)
	resolved, err := filepath.EvalSymlinks(temporary)
	if err != nil {
		resolved = temporary // Cleanup still uses the original temporary path.
	}
	temporary = resolved
	tree := filepath.Join(temporary, "tree")
	// Clean up even if add was interrupted. Never use the cancelled run context
	// for removal, and never prune unrelated worktree registrations.
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, err := replayGit(cleanupCtx, config.root, "worktree", "remove", "--force", tree)
		if err != nil {
			reason := err.Error()
			// A failed add need not have registered a worktree at all.
			listing, err := replayGit(cleanupCtx, config.root, "worktree", "list", "--porcelain", "-z")
			if err != nil || slices.Contains(strings.Split(string(listing), "\x00"), "worktree "+tree) {
				result.Status = "failed"
				result.Stage = "cleanup"
				result.Reason += "\nworktree cleanup failed: " + reason
			}
		}
	}()
	result.Stage = "worktree"
	if _, err := replayGit(ctx, config.root, "worktree", "add", "--detach", tree, event.Parents[0]); err != nil {
		result.Status = "failed"
		result.Reason = err.Error()
		return result
	}
	cache := filepath.Join(temporary, "cache")
	preflight := filepath.Join(temporary, "preflight")
	for _, dir := range []string{cache, preflight} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			result.Status = "failed"
			result.Reason = err.Error()
			return result
		}
	}
	buildOptions := profile.BuildOptions{
		Directory: filepath.Join(tree, filepath.FromSlash(config.cwd)), GoBinary: config.toolchain,
		Target: config.target, BuildFlags: config.flags, CacheDir: cache,
		OutputPath: filepath.Join(temporary, "build-output"), Env: config.env, HashPackages: config.hashPackages,
	}
	preflightOptions := Options{Target: config.target, GoBinary: config.toolchain, BuildFlags: config.flags}
	result.Stage = "baseline-preflight"
	if reason, err := checkReplayModule(ctx, tree, buildOptions.Directory, preflight, config); reason != "" || err != nil {
		setPreflightFailure(result, reason, err)
		recordUncertainConfiguration(result, event, event.Parents[0])
		return result
	}
	parentNodes, _, err := loadGraphWithEnv(ctx, buildOptions.Directory, preflight, preflightOptions, config.env)
	if err != nil {
		result.Status = "failed"
		result.Reason = err.Error()
		recordUncertainConfiguration(result, event, event.Parents[0])
		return result
	}
	if excludeConfigurationChange(result, reviewConfiguration(ctx, tree, buildOptions.Directory, preflight, config, parentNodes, event, event.Parents[0])) {
		return result
	}
	result.Stage = "baseline"
	baseline := profile.CaptureBuild(ctx, buildOptions)
	result.Baseline = baseline
	if baseline.Failure != "" {
		result.Status = "failed"
		result.Reason = baseline.Failure
		return result
	}
	if _, err := os.Stat(buildOptions.OutputPath); err != nil {
		result.Reason = "cold parent did not produce the requested build output (check inherited build flags): " + err.Error()
		return result
	}
	if baseline.Run.Graph.ExecutedActions == 0 {
		result.Reason = "cold parent recorded no commands in its fresh cache; flags, cache or action-graph schema cannot support this replay"
		return result
	}
	result.Stage = "warm"
	warm := profile.CaptureBuild(ctx, buildOptions)
	result.Warm = warm
	if warm.Failure != "" {
		result.Status = "failed"
		result.Reason = warm.Failure
		return result
	}
	if warm.Run.Graph.ExecutedActions != 0 {
		result.Reason = "parent warm build executed commands; baseline is not a verified no-op"
		return result
	}
	result.Stage = "checkout"
	if _, err := replayGit(ctx, tree, "checkout", "--detach", "--force", event.Commit); err != nil {
		result.Status = "failed"
		result.Reason = err.Error()
		return result
	}
	if _, err := replayGit(ctx, tree, "clean", "-ffdx"); err != nil {
		result.Status = "failed"
		result.Reason = err.Error()
		return result
	}
	result.Stage = "rebuild-preflight"
	if reason, err := checkReplayModule(ctx, tree, buildOptions.Directory, preflight, config); reason != "" || err != nil {
		setPreflightFailure(result, reason, err)
		recordUncertainConfiguration(result, event, event.Commit)
		return result
	}
	nodes, _, err := loadGraphWithEnv(ctx, buildOptions.Directory, preflight, preflightOptions, config.env)
	if err != nil {
		result.Status = "failed"
		result.Reason = err.Error()
		recordUncertainConfiguration(result, event, event.Commit)
		return result
	}
	if excludeConfigurationChange(result, reviewConfiguration(ctx, tree, buildOptions.Directory, preflight, config, nodes, event, event.Commit)) {
		return result
	}
	result.PackageImports = make(map[string]string)
	for _, node := range nodes {
		if dir, inside := repositoryDir(tree, node.Dir); inside && slices.Contains(event.PackageDirs, dir) {
			result.PackageImports[dir] = node.ImportPath
		}
	}
	result.Stage = "rebuild"
	rebuild := profile.CaptureBuild(ctx, buildOptions)
	result.Rebuild = rebuild
	if rebuild.Failure != "" {
		result.Status = "failed"
		result.Reason = rebuild.Failure
		return result
	}
	for _, detail := range rebuild.Run.Graph.ExecutedActionDetails {
		if detail.CompileCommands > 0 && detail.Package != "" {
			result.RecompiledPackages = append(result.RecompiledPackages, detail.Package)
		}
	}
	sort.Strings(result.RecompiledPackages)
	result.RecompiledPackages = slices.Compact(result.RecompiledPackages)
	for _, action := range rebuild.Run.Graph.ActionGraph {
		if action.Executed() && action.Package != "" {
			switch action.Mode {
			case "build":
				result.RebuiltPackages = append(result.RebuiltPackages, action.Package)
			case "link":
				result.LinkedPackages = append(result.LinkedPackages, action.Package)
			}
		}
	}
	sort.Strings(result.RebuiltPackages)
	result.RebuiltPackages = slices.Compact(result.RebuiltPackages)
	sort.Strings(result.LinkedPackages)
	result.LinkedPackages = slices.Compact(result.LinkedPackages)
	result.Status = "measured"
	result.Stage = "complete"
	return result
}

func setPreflightFailure(result *ReplayResult, reason string, err error) {
	result.Reason = reason
	if err != nil {
		result.Status = "failed"
		result.Reason = err.Error()
	}
}

// Workspace and external local replacements would read the developer's checkout
// or other mutable source trees instead of a controlled historical snapshot.
func checkReplayModule(ctx context.Context, tree, cwd, cache string, config replayConfig) (string, error) {
	output, err := runGoWithEnv(ctx, cwd, cache, config.toolchain, config.env, "env", "-json", "GOMOD", "GOVERSION")
	if err != nil {
		return "", err
	}
	var envInfo struct{ GOMOD, GOVERSION string }
	if err := json.Unmarshal(output, &envInfo); err != nil {
		return "", err
	}
	if config.version != "" && envInfo.GOVERSION != config.version {
		return "toolchain differs from graph discovery: " + envInfo.GOVERSION, nil
	}
	if envInfo.GOMOD == "" || envInfo.GOMOD == os.DevNull {
		return "replay requires a self-contained module", nil
	}
	if _, inside := repositoryDir(tree, envInfo.GOMOD); !inside {
		return "module file resolves outside the historical worktree", nil
	}
	output, err = runGoWithEnv(ctx, cwd, cache, config.toolchain, config.env, "mod", "edit", "-json")
	if err != nil {
		return "", err
	}
	var module struct {
		Replace []struct {
			New struct{ Path, Version string }
		}
	}
	if err := json.Unmarshal(output, &module); err != nil {
		return "", err
	}
	for _, replacement := range module.Replace {
		if replacement.New.Version != "" {
			continue
		}
		dir := replacement.New.Path
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(filepath.Dir(envInfo.GOMOD), dir)
		}
		if _, inside := repositoryDir(tree, dir); !inside {
			return "local module replacement resolves outside the historical worktree: " + replacement.New.Path, nil
		}
	}
	return "", nil
}

func gitLocationVariable(key string) bool {
	switch key {
	case "GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR", "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES":
		return true
	}
	return false
}

func replayGit(ctx context.Context, cwd string, args ...string) ([]byte, error) {
	args = append([]string{"-c", "core.hooksPath=" + os.DevNull, "-c", "core.fsmonitor=false"}, args...)
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = cwd
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !gitLocationVariable(key) {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return output, ctx.Err()
	}
	if err != nil {
		return output, fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, output)
	}
	return output, nil
}
