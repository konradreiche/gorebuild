package history

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func Run(ctx context.Context, options Options) (Report, error) {
	if options.Target == "" {
		return Report{}, errors.New("empty package target")
	}
	if options.GoBinary == "" {
		options.GoBinary = "go"
	}
	// Graph discovery must not repair go.mod/go.sum in the user's checkout.
	// An explicit vendor mode is also read-only. Record the effective override
	// so a report never claims to have used a different module configuration.
	moduleMode := "-mod=readonly"
	for _, buildFlag := range options.BuildFlags {
		name, value, _ := strings.Cut(strings.TrimPrefix(strings.TrimPrefix(buildFlag, "-"), "-"), "=")
		switch name {
		case "o", "C", "debug-actiongraph", "n", "work":
			return Report{}, fmt.Errorf("-%s is managed by gorebuild and cannot be passed through", name)
		}
		if options.CompareVCS && name == "buildvcs" {
			return Report{}, errors.New("--compare-vcs requires normal default VCS behavior; remove explicit -buildvcs flags")
		}
		if name == "mod" {
			if value != "readonly" && value != "vendor" {
				return Report{}, errors.New("history requires -mod=readonly or -mod=vendor to protect the checkout")
			}
			moduleMode = "-mod=" + value
		}
	}
	options.BuildFlags = append(append([]string{}, options.BuildFlags...), moduleMode)
	if options.Since == "" {
		options.Since = "90d"
	}
	if options.MaxCommits < 0 {
		return Report{}, errors.New("max commits cannot be negative (0 means the entire requested period)")
	}
	if options.Replay < 0 || options.Replay > 100000 {
		return Report{}, errors.New("replay count must be between 0 and 100000")
	}
	if options.CompareVCS && options.Replay == 0 {
		return Report{}, errors.New("--compare-vcs requires --replay N")
	}
	now := time.Now().UTC()
	since, err := ParseSince(options.Since, now)
	if err != nil {
		return Report{}, err
	}
	if options.OutputFile != "" {
		if _, err := os.Lstat(options.OutputFile); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return Report{}, err
			}
		} else {
			return Report{}, fmt.Errorf("output file %q already exists; refusing to overwrite", options.OutputFile)
		}
	}
	cwd, err := os.Getwd()
	if err != nil {
		return Report{}, err
	}
	rootBytes, err := git(ctx, cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return Report{}, err
	}
	root := strings.TrimSuffix(string(rootBytes), "\n")
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		resolved = root // Best effort: retain the original path if resolution fails.
	}
	root = resolved
	headBytes, err := git(ctx, root, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return Report{}, err
	}
	head := strings.TrimSpace(string(headBytes))
	status, err := git(ctx, root, "status", "--porcelain=v1", "-z", "--untracked-files=normal")
	if err != nil {
		return Report{}, err
	}
	shallow, err := git(ctx, root, "rev-parse", "--is-shallow-repository")
	if err != nil {
		return Report{}, err
	}
	events, truncated, err := readEvents(ctx, root, head, since, options.MaxCommits)
	if err != nil {
		return Report{}, err
	}
	cache, err := os.MkdirTemp("", "gorebuild-history-*")
	if err != nil {
		return Report{}, err
	}
	defer os.RemoveAll(cache)
	var envInfo struct {
		GOVERSION string
		GOOS      string
		GOARCH    string
		GOFLAGS   string
		GOWORK    string
		GOROOT    string
	}
	envBytes, err := runGo(ctx, cwd, cache, options.GoBinary, "env", "-json", "GOVERSION", "GOOS", "GOARCH", "GOFLAGS", "GOWORK", "GOROOT", "CGO_ENABLED", "GOEXPERIMENT", "GOAMD64", "GOARM64", "GOARM", "GO386", "CC", "CXX", "CGO_CFLAGS", "CGO_CPPFLAGS", "CGO_CXXFLAGS", "CGO_LDFLAGS", "GOCACHEPROG")
	if err != nil {
		return Report{}, err
	}
	if err := json.Unmarshal(envBytes, &envInfo); err != nil {
		return Report{}, fmt.Errorf("decode go env: %w", err)
	}
	if options.CompareVCS && strings.Contains(envInfo.GOFLAGS, "-buildvcs") {
		return Report{}, errors.New("--compare-vcs requires normal default VCS behavior; remove -buildvcs from GOFLAGS")
	}
	var buildEnv map[string]string
	if err := json.Unmarshal(envBytes, &buildEnv); err != nil {
		return Report{}, fmt.Errorf("decode build environment: %w", err)
	}
	for _, key := range []string{"GOVERSION", "GOOS", "GOARCH", "GOFLAGS", "GOWORK", "GOROOT"} {
		delete(buildEnv, key)
	}
	nodes, target, err := loadGraph(ctx, cwd, cache, options)
	if err != nil {
		return Report{}, err
	}
	report, err := Analyze(root, nodes, events)
	if err != nil {
		return Report{}, err
	}
	report.Metadata = Metadata{
		Target: options.Target, ImportPath: target, Repository: root, Head: head,
		Dirty: len(status) > 0, Shallow: strings.TrimSpace(string(shallow)) == "true",
		SinceUTC: since, SinceRequested: options.Since, CapturedUTC: now, MaxCommits: options.MaxCommits, Truncated: truncated,
		EventsAnalyzed: len(report.Events), CoverageComplete: !truncated && strings.TrimSpace(string(shallow)) != "true",
		GoVersion: envInfo.GOVERSION, GOOS: envInfo.GOOS, GOARCH: envInfo.GOARCH,
		GOFLAGS: envInfo.GOFLAGS, GOWORK: envInfo.GOWORK, BuildFlags: append([]string{}, options.BuildFlags...),
		BuildEnvironment: buildEnv,
	}
	for _, node := range report.Graph {
		if node.ImportPath == report.Metadata.ImportPath {
			if name, inside := repositoryDir(root, node.ModuleGoMod); inside {
				report.Metadata.TargetModuleFile = name
			}
		}
	}
	for _, event := range report.Events {
		if report.Metadata.OldestEventUTC == nil || event.TimeUTC.Before(*report.Metadata.OldestEventUTC) {
			stamp := event.TimeUTC
			report.Metadata.OldestEventUTC = &stamp
			report.Metadata.OldestCommit = event.Commit
		}
	}
	report.Warnings = []string{
		"Estimates use the current working-tree import graph, not historical graphs or measured rebuilds.",
		"Graph discovery defaults to -mod=readonly; use -build-flag=-mod=vendor to select vendored dependencies.",
		"Reverse dependencies may remain cached; static reach is not a latency estimate.",
		"History uses first-parent deltas, including merges; counts are change events, not developer builds.",
		"Package identity is a repository-relative directory. Moves, deleted packages and build-tag changes are not reconstructed.",
		"Non-Go classifications use current embed/build inputs and filename heuristics, not reconstructed historical rules. Unknown/config-wide changes do not automatically invalidate every package.",
		"Test/documentation changes are unlikely to invalidate compilation, but VCS stamping may still cause linking; replay observes actual behavior.",
		"Co-change counts include tests and mapped build inputs. Conditional proportions are descriptive, not evidence of coupling from small counts. Package rows overlap; do not sum them as separate builds.",
	}
	if report.Metadata.Dirty {
		report.Warnings = append(report.Warnings, "Working tree is dirty: graph may differ from recorded HEAD history; no files were checked out or modified.")
	}
	if report.Metadata.Shallow {
		report.Warnings = append(report.Warnings, "Shallow repository: history is incomplete and boundary commits may appear as root additions.")
	}
	if truncated {
		report.Warnings = append(report.Warnings, "Commit limit reached: counts cover only the most recent selected first-parent events.")
	}
	if options.Package != "" {
		report.FocusPackage, err = resolveFocus(report, cwd, options.Package)
		if err != nil {
			return Report{}, err
		}
	}
	if options.Replay > 0 {
		report.Analysis = "current-import-graph-and-measured-historical-events"
		replayHistory(ctx, &report, options, cwd, envInfo.GOROOT)
		report.Warnings = append(
			report.Warnings,
			"Replay cohorts overlap: measured event costs are not independently attributable to co-changing packages. Do not sum package medians.",
			"Medians/P90 describe successful samples only; coverage, failures and exclusions are recorded. Single historical builds are noisy, not stable benchmarks.",
			"Historical builds execute trusted inputs, not a sandbox. Active workspaces, external local replacements and relevant/uncertain configuration changes are excluded; unrelated nested modules are reviewed in both revisions.",
			"Executed-only critical paths use action elapsed times on the full DAG, with zero weight for cache checks. Tool work, critical path and wall time are not additive.",
			"Cgo/assembly action counts are overlapping subsets of executed nodes identified by their recorded commands; per-command timings within a node are unavailable.",
		)
	}
	if options.OutputFile != "" {
		if err := writeReport(options.OutputFile, report); err != nil {
			return Report{}, err
		}
	}
	// Persist partial replay evidence before reporting cancellation to the CLI.
	if err := ctx.Err(); err != nil {
		return Report{}, err
	}
	return report, nil
}

func resolveFocus(report Report, cwd, query string) (string, error) {
	absolute := query
	if !filepath.IsAbs(absolute) {
		absolute = filepath.Join(cwd, query)
	}
	dir, inside := repositoryDir(report.Metadata.Repository, absolute)
	for _, pkg := range report.Packages {
		if query == pkg.ImportPath || query == pkg.Dir || (inside && dir == pkg.Dir) {
			return pkg.Dir, nil
		}
	}
	return "", fmt.Errorf("package %q is neither in the current target graph nor a changed Go directory in the selected history", query)
}

// O_EXCL also guards against a file appearing after the initial check. A
// partial artifact is removed on failure; no sidecar files are created.
func writeReport(path string, report Report) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("create history artifact: %w", err)
	}
	if strings.HasSuffix(path, ".gz") {
		compressed := gzip.NewWriter(file)
		_, err = compressed.Write(append(data, '\n'))
		err = errors.Join(err, compressed.Close())
	} else {
		_, err = file.Write(append(data, '\n'))
	}
	err = errors.Join(err, file.Close())
	if err != nil {
		_ = os.Remove(path)
	}
	return err
}
