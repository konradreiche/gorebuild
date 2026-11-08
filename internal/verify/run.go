package verify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// Run retains failure evidence just like profile.CaptureBuild. Errors live in
// the report, including correctness, cleanup, cancellation and artifact failures.
func Run(ctx context.Context, options Options) *Report {
	report := &Report{SchemaVersion: 1, Target: options.Target, Before: Revision{Ref: options.Before}, After: Revision{Ref: options.After}, CachePolicy: "independent initially empty GOCACHE per revision/run; separate metadata and correctness caches; module downloads shared; filesystem caches not flushed", Limitations: []string{
		"Only trusted repositories: builds and tests execute code; temporary worktrees are not a sandbox.",
		"Passing selected tests is not proof of behavioral equivalence; initialization, public API and out-of-scope users still require review.",
		"Controlled additive exported function changes compilation/export inputs without executing code; it is not a representative distribution of real edits.",
		"Wall time, aggregate tool work and dependency critical-path estimates are distinct; avoided action durations are not wall-clock savings.",
		"Paired runs alternate order, but filesystem caches, module downloads, host load and scheduling are not eliminated. Small samples establish observations, not statistical significance.",
	}}
	var output *os.File
	if options.OutputFile != "" {
		file, err := os.OpenFile(options.OutputFile, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			report.Errors = append(report.Errors, err.Error())
			return report
		}
		output = file
		defer output.Close()
	}
	if err := run(ctx, options, report); err != nil {
		report.Errors = append(report.Errors, err.Error())
		report.PerformanceValid = false
	}
	if len(report.Scenarios) == 0 {
		report.Summary = summarize(report.Pairs)
	} else {
		report.ColdSummary = summarize(report.ColdPairs)
		for index := range report.Scenarios {
			scenario := &report.Scenarios[index]
			scenario.Summary = summarize(scenario.Pairs)
			scenario.ControlSummary = summarize(scenario.Controls)
			scenario.Noise = noiseConclusion(scenario.Pairs, scenario.Controls)
			scenario.Structure = structuralCounts(report, scenario.Pairs)
			if scenario.Scenario.Kind == "exported" {
				report.Summary = scenario.Summary
			}
		}
	}
	if output != nil {
		if err := encodeReport(output, options.OutputFile, report); err != nil {
			report.Errors = append(report.Errors, "write verification artifact: "+err.Error())
			report.PerformanceValid = false
		}
	}
	return report
}

func run(ctx context.Context, options Options, report *Report) (err error) {
	if options.Before == "" || options.After == "" || options.Package == "" || options.Target == "" {
		return errors.New("verify requires target, --before, --after and --package")
	}
	if options.Runs < 1 || options.Runs > 100 {
		return errors.New("--runs must be between 1 and 100")
	}
	report.Untested = []string{"other GOOS/GOARCH targets and build-tag configurations; current effective configuration only", "full behavioral equivalence and external consumers not present in this repository"}
	if !options.RaceTests {
		report.Untested = append(report.Untested, "correctness race-detector configuration (enable --race-tests)")
	}
	if len(options.RuntimeArgs) == 0 {
		report.Untested = append(report.Untested, "executable runtime smoke check (explicit --runtime-arg required)")
	}
	if options.GoBinary == "" {
		options.GoBinary = "go"
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	runner := &engine{root: cwd, progress: options.Progress}
	root, err := runner.git(ctx, "rev-parse", "--show-toplevel")
	if err != nil {
		return err
	}
	runner.root = strings.TrimSpace(string(root))
	runner.root, err = filepath.EvalSymlinks(runner.root)
	if err != nil {
		return err
	}
	cwd, err = filepath.EvalSymlinks(cwd)
	if err != nil {
		return err
	}
	runner.cwd, err = filepath.Rel(runner.root, cwd)
	if err != nil {
		return err
	}
	if !inside(runner.root, cwd) {
		return errors.New("invocation outside repository")
	}
	report.Repository = runner.root
	runner.temp, err = os.MkdirTemp("", "gorebuild-verify-*")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(runner.temp)) }()
	runner.goBinary, err = exec.LookPath(options.GoBinary)
	if err != nil {
		return err
	}
	runner.goBinary, err = filepath.Abs(runner.goBinary)
	if err != nil {
		return err
	}
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "GIT_") || strings.HasPrefix(entry, "GOCACHE=") {
			continue
		}
		runner.env = append(runner.env, entry)
	}
	metadataCache, err := runner.cache("configuration")
	if err != nil {
		return err
	}
	bytes, check := runner.command(ctx, cwd, metadataCache, "env", "-json", "GOVERSION", "GOROOT", "GOOS", "GOARCH", "GOFLAGS", "GOWORK", "GOMODCACHE", "GOPATH", "GOPROXY", "CGO_ENABLED", "GOEXPERIMENT", "GOAMD64", "GOARM", "GOARM64", "GO386", "CC", "CXX", "CGO_CFLAGS", "CGO_CPPFLAGS", "CGO_CXXFLAGS", "CGO_LDFLAGS", "GOCACHEPROG", "GODEBUG")
	if check.Failure != "" {
		return fmt.Errorf("Go configuration: %s", check.Failure+check.Log)
	}
	if err := json.Unmarshal(bytes, &report.Configuration); err != nil {
		return err
	}
	if report.Configuration["GOCACHEPROG"] != "" {
		return errors.New("external GOCACHEPROG is unsupported; disable it for independent caches")
	}
	if report.Configuration["GOWORK"] != "" && report.Configuration["GOWORK"] != "off" {
		return errors.New("active workspace is unsupported; use GOWORK=off")
	}
	if err := validateFlags(append(strings.Fields(report.Configuration["GOFLAGS"]), options.BuildFlags...)); err != nil {
		return err
	}
	flags := append([]string(nil), options.BuildFlags...)
	mode := "-mod=readonly"
	if strings.Contains(report.Configuration["GOFLAGS"], "-mod=vendor") || slices.Contains(options.BuildFlags, "-mod=vendor") {
		mode = "-mod=vendor"
	}
	runner.flags = append(flags, mode, "-buildvcs=false")
	report.BuildFlags = runner.flags
	overrides := map[string]string{"GOTOOLCHAIN": "local", "GOROOT": report.Configuration["GOROOT"], "GOWORK": "off", "GOCACHEPROG": ""}
	for _, key := range []string{"GOOS", "GOARCH", "GOFLAGS", "CGO_ENABLED", "GOEXPERIMENT", "GOAMD64", "GOARM", "GOARM64", "GO386", "CC", "CXX", "CGO_CFLAGS", "CGO_CPPFLAGS", "CGO_CXXFLAGS", "CGO_LDFLAGS", "GODEBUG", "GOMODCACHE", "GOPATH", "GOPROXY"} {
		overrides[key] = report.Configuration[key]
	}
	var environment []string
	for _, entry := range runner.env {
		key, _, _ := strings.Cut(entry, "=")
		if _, known := overrides[key]; !known {
			environment = append(environment, entry)
		}
	}
	for key, value := range overrides {
		environment = append(environment, key+"="+value)
	}
	runner.env = environment
	report.Configuration["GOTOOLCHAIN"] = "local"
	report.Configuration["GOWORK"] = "off"
	report.Configuration["GO_BINARY"] = runner.goBinary
	runner.goBinary = filepath.Join(report.Configuration["GOROOT"], "bin", "go")
	runner.version = report.Configuration["GOVERSION"]
	report.Before.Commit, err = resolveRevision(ctx, runner, options.Before)
	if err != nil {
		return err
	}
	report.After.Commit, err = resolveRevision(ctx, runner, options.After)
	if err != nil {
		return err
	}
	if report.Before.Commit == report.After.Commit {
		return errors.New("before and after resolve to the same commit")
	}
	if err := loadTestOverlays(options, report); err != nil {
		return err
	}
	options.overlays = report.TestOverlays
	changedDirs, err := changedDirectories(ctx, runner, report.Before.Commit, report.After.Commit)
	if err != nil {
		return err
	}
	for _, revision := range []*Revision{&report.Before, &report.After} {
		runner.note("Correctness builds/tests: %s", short(revision.Commit))
		if err := correctness(ctx, runner, revision, options, changedDirs); err != nil {
			return err
		}
	}
	if report.Before.ImportPath != report.After.ImportPath || report.Before.ChangedPackage != report.After.ChangedPackage || report.Before.PackageName != report.After.PackageName || report.Before.PackageDirectory != report.After.PackageDirectory {
		return errors.New("target/controlled package identity differs across revisions")
	}
	report.DependencyChanges = compareGraphs(report.Before.Graph, report.After.Graph, report.Before.ChangedPackage)
	if len(options.Scenarios) > 0 {
		if err := auditCallers(ctx, runner, report, options); err != nil {
			return err
		}
	}
	if len(options.Scenarios) > 0 || len(options.HistoricalCommits) > 0 || options.HistoryFile != "" {
		return runExpanded(ctx, runner, report, options)
	}
	report.Probe = makeProbe(report.Before.PackageName)
	for index := range options.Runs {
		pair := Pair{Index: index + 1, Order: []string{"before", "after"}}
		if index%2 == 1 {
			pair.Order = []string{"after", "before"}
		}
		report.Pairs = append(report.Pairs, pair)
		current := &report.Pairs[len(report.Pairs)-1]
		for _, label := range pair.Order {
			revision := &report.Before
			if label == "after" {
				revision = &report.After
			}
			measurement := measure(ctx, runner, revision, label, options.Target, report.Probe)
			if label == "before" {
				current.Before = measurement
			} else {
				current.After = measurement
			}
			if measurement.Status != "measured" {
				return fmt.Errorf("%s run %d: %s", label, index+1, measurement.Reason)
			}
		}
	}
	report.PerformanceValid = true
	return nil
}

func validateFlags(flags []string) error {
	for _, flag := range flags {
		name, value, _ := strings.Cut(strings.TrimLeft(flag, "-"), "=")
		switch name {
		case "o", "C", "debug-actiongraph", "n", "work", "a", "overlay", "modfile", "pkgdir", "toolexec", "buildvcs":
			return fmt.Errorf("-%s is managed or unsupported for controlled verification", name)
		case "mod":
			if value != "readonly" && value != "vendor" {
				return errors.New("verify permits only -mod=readonly or -mod=vendor")
			}
		}
	}
	return nil
}

func resolveRevision(ctx context.Context, runner *engine, ref string) (string, error) {
	output, err := runner.git(ctx, "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func changedDirectories(ctx context.Context, runner *engine, before, after string) ([]string, error) {
	output, err := runner.git(ctx, "diff", "--name-only", "--no-renames", "-z", before, after, "--")
	if err != nil {
		return nil, err
	}
	var dirs []string
	for _, file := range strings.Split(string(output), "\x00") {
		if strings.HasSuffix(file, ".go") {
			dir := path.Dir(file)
			if !slices.Contains(dirs, dir) {
				dirs = append(dirs, dir)
			}
		}
	}
	return dirs, nil
}

func correctness(ctx context.Context, runner *engine, revision *Revision, options Options, changedDirs []string) (err error) {
	work, err := runner.worktree(ctx, revision.Commit, "correctness")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, work.close()) }()
	cache, err := runner.cache("correctness")
	if err != nil {
		return err
	}
	revision.correctnessCache = cache
	if err := runner.inspect(ctx, work, cache, revision, options, changedDirs); err != nil {
		return err
	}
	if err := installTestOverlays(work, revision, options.overlays); err != nil {
		return err
	}
	revision.Build = runner.capture(ctx, work, cache, options.Target)
	if revision.Build.Failure != "" {
		return fmt.Errorf("%s correctness build: %s", revision.Ref, revision.Build.Failure)
	}
	args := append([]string{"test", "-count=1"}, runner.flags...)
	if options.RaceTests {
		args = append(args, "-race")
	}
	args = append(args, revision.TestPackages...)
	_, revision.Tests = runner.command(ctx, filepath.Join(work.path, runner.cwd), cache, args...)
	if revision.Tests.Failure != "" {
		return fmt.Errorf("%s correctness tests: %s\n%s", revision.Ref, revision.Tests.Failure, revision.Tests.Log)
	}
	if len(options.RuntimeArgs) > 0 {
		revision.Runtime = runner.runtimeCheck(ctx, work, options.RuntimeArgs)
		if revision.Runtime.Failure != "" {
			return fmt.Errorf("runtime correctness check failed: %s", revision.Runtime.Log+revision.Runtime.Failure)
		}
	}
	revision.Correct = true
	return nil
}
