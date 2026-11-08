package verify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/konradreiche/gorebuild/internal/history"
	"github.com/konradreiche/gorebuild/internal/profile"
)

type engine struct {
	root, temp, goBinary, cwd, version string
	env                                []string
	flags                              []string
	progress                           io.Writer
}

type tree struct{ path, root string }

func (runner *engine) note(message string, args ...any) {
	if runner.progress != nil {
		fmt.Fprintf(runner.progress, message+"\n", args...)
	}
}

func (runner *engine) git(ctx context.Context, args ...string) ([]byte, error) {
	args = append([]string{"-c", "core.hooksPath=" + os.DevNull, "-c", "core.fsmonitor=false", "-C", runner.root}, args...)
	command := exec.CommandContext(ctx, "git", args...)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GIT_") {
			command.Env = append(command.Env, entry)
		}
	}
	output, err := command.CombinedOutput()
	if err != nil {
		return output, fmt.Errorf("git: %w: %s", err, output)
	}
	return output, nil
}

func (runner *engine) worktree(ctx context.Context, commit, label string) (*tree, error) {
	temporary, err := os.MkdirTemp(runner.temp, label+"-")
	if err != nil {
		return nil, err
	}
	resolved, err := filepath.EvalSymlinks(temporary)
	if err != nil {
		os.RemoveAll(temporary)
		return nil, err
	}
	temporary = resolved
	path := filepath.Join(temporary, "tree")
	_, err = runner.git(ctx, "worktree", "add", "--detach", path, commit)
	value := &tree{path: path, root: runner.root}
	if err != nil {
		failure := err.Error()
		if err := value.close(); err != nil {
			return nil, fmt.Errorf("%s; cleanup: %w", failure, err)
		}
		return nil, err
	}
	return value, nil
}

func (value *tree) close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	runner := engine{root: value.root}
	_, err := runner.git(ctx, "worktree", "remove", "--force", value.path)
	if err != nil {
		reason := err.Error()
		listing, err := runner.git(ctx, "worktree", "list", "--porcelain", "-z")
		if err != nil || slices.Contains(strings.Split(string(listing), "\x00"), "worktree "+value.path) {
			return fmt.Errorf("remove verification worktree %s: %s", value.path, reason)
		}
	}
	return os.RemoveAll(filepath.Dir(value.path))
}

func (runner *engine) environment(cache string) []string {
	env := make([]string, 0, len(runner.env)+1)
	for _, entry := range runner.env {
		if !strings.HasPrefix(entry, "GOCACHE=") {
			env = append(env, entry)
		}
	}
	return append(env, "GOCACHE="+cache)
}

func (runner *engine) command(ctx context.Context, dir, cache string, args ...string) ([]byte, *CommandCheck) {
	check := &CommandCheck{Arguments: append([]string{runner.goBinary}, args...)}
	command := exec.CommandContext(ctx, runner.goBinary, args...)
	command.Dir = dir
	command.Env = runner.environment(cache)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	started := time.Now()
	err := command.Run()
	check.WallTimeNS = int64(time.Since(started))
	check.Log = stdout.String() + stderr.String()
	if err != nil {
		check.Failure = err.Error()
	}
	if ctx.Err() != nil {
		check.Failure = ctx.Err().Error()
	}
	return stdout.Bytes(), check
}

func (runner *engine) cache(label string) (string, error) {
	return os.MkdirTemp(runner.temp, label+"-cache-")
}

func (runner *engine) capture(ctx context.Context, work *tree, cache, target string) *profile.BuildCapture {
	return profile.CaptureBuild(ctx, profile.BuildOptions{Directory: filepath.Join(work.path, runner.cwd), GoBinary: runner.goBinary, Target: target, BuildFlags: runner.flags, CacheDir: cache, OutputPath: filepath.Join(filepath.Dir(work.path), "binary"), Env: runner.env})
}

func (runner *engine) inspect(ctx context.Context, work *tree, cache string, revision *Revision, options Options, changedDirs []string) error {
	dir := filepath.Join(work.path, runner.cwd)
	output, check := runner.command(ctx, dir, cache, "env", "-json", "GOVERSION", "GOMOD")
	if check.Failure != "" {
		return fmt.Errorf("revision Go configuration: %s", check.Log+check.Failure)
	}
	var info struct{ GOVERSION, GOMOD string }
	if err := json.Unmarshal(output, &info); err != nil {
		return err
	}
	if info.GOVERSION != runner.version {
		return fmt.Errorf("revision toolchain differs: %s, expected %s", info.GOVERSION, runner.version)
	}
	if info.GOMOD == "" || info.GOMOD == os.DevNull {
		return fmt.Errorf("revision requires a module inside its isolated worktree")
	}
	moduleFile, err := filepath.EvalSymlinks(info.GOMOD)
	if err != nil {
		return err
	}
	if !inside(work.path, moduleFile) {
		return fmt.Errorf("revision module resolves outside its isolated worktree")
	}
	output, check = runner.command(ctx, dir, cache, "mod", "edit", "-json")
	if check.Failure != "" {
		return fmt.Errorf("revision module configuration: %s", check.Log+check.Failure)
	}
	var module struct {
		Replace []struct {
			New struct{ Path, Version string }
		}
	}
	if err := json.Unmarshal(output, &module); err != nil {
		return err
	}
	for _, replacement := range module.Replace {
		if replacement.New.Version != "" {
			continue
		}
		path := replacement.New.Path
		if !filepath.IsAbs(path) {
			path = filepath.Join(filepath.Dir(info.GOMOD), path)
		}
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return err
		}
		if !inside(work.path, resolved) {
			return fmt.Errorf("external local replacement is not isolated: %s", path)
		}
	}
	nodes, target, err := history.InspectBuildGraph(ctx, dir, cache, history.Options{Target: options.Target, GoBinary: runner.goBinary, BuildFlags: runner.flags}, runner.env)
	if err != nil {
		return err
	}
	revision.ImportPath = target
	for _, node := range nodes {
		if node.ImportPath == target || inside(work.path, node.ModuleGoMod) {
			resolved, err := filepath.EvalSymlinks(node.Dir)
			if err != nil {
				return err
			}
			if !inside(work.path, resolved) {
				return fmt.Errorf("repository source resolves outside isolated worktree: %s", node.Dir)
			}
		}
	}
	args := append([]string{"list", "-json"}, runner.flags...)
	args = append(args, options.Package)
	output, check = runner.command(ctx, dir, cache, args...)
	if check.Failure != "" {
		return fmt.Errorf("controlled package: %s", check.Log+check.Failure)
	}
	var pkg struct {
		ImportPath, Name, Dir string
		GoFiles, CgoFiles     []string
	}
	if err := json.Unmarshal(output, &pkg); err != nil {
		return err
	}
	resolved, err := filepath.EvalSymlinks(pkg.Dir)
	if err != nil {
		return err
	}
	if !inside(work.path, resolved) {
		return fmt.Errorf("controlled package is outside isolated worktree")
	}
	if !slices.ContainsFunc(nodes, func(node history.PackageNode) bool { return node.ImportPath == pkg.ImportPath }) {
		return fmt.Errorf("controlled package is not in target dependency closure")
	}
	relative, err := filepath.Rel(work.path, pkg.Dir)
	if err != nil {
		return err
	}
	revision.ChangedPackage = pkg.ImportPath
	revision.PackageName = pkg.Name
	revision.PackageDirectory = filepath.ToSlash(relative)
	revision.SourceFiles = append([]string(nil), pkg.GoFiles...)
	if len(options.TestPackages) > 0 {
		revision.TestPackages = append([]string(nil), options.TestPackages...)
	} else {
		tests := map[string]bool{pkg.ImportPath: true}
		for _, node := range nodes {
			if !inside(work.path, node.Dir) {
				continue
			}
			relative, err := filepath.Rel(work.path, node.Dir)
			if err != nil {
				return err
			}
			if slices.Contains(changedDirs, filepath.ToSlash(relative)) {
				tests[node.ImportPath] = true
			}
		}
		for name := range tests {
			revision.TestPackages = append(revision.TestPackages, name)
		}
		sort.Strings(revision.TestPackages)
	}
	for index := range nodes {
		if inside(work.path, nodes[index].Dir) {
			relative, err := filepath.Rel(work.path, nodes[index].Dir)
			if err != nil {
				return err
			}
			nodes[index].Dir = filepath.ToSlash(relative)
		}
		if inside(work.path, nodes[index].ModuleGoMod) {
			relative, err := filepath.Rel(work.path, nodes[index].ModuleGoMod)
			if err != nil {
				return err
			}
			nodes[index].ModuleGoMod = filepath.ToSlash(relative)
		}
	}
	revision.Graph = nodes
	return nil
}

func inside(root, path string) bool {
	if path == "" {
		return false
	}
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
