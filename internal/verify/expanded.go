package verify

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/konradreiche/gorebuild/internal/profile"
)

type preparedRevision struct {
	work     *tree
	revision *Revision
	cache    string
	binary   string
}

func patchCommand(ctx context.Context, runner *engine, work *tree, scenario Scenario, check bool) error {
	args := []string{"-c", "core.hooksPath=" + os.DevNull, "-c", "core.fsmonitor=false", "-C", work.path, "apply", "--whitespace=nowarn"}
	if check {
		args = append(args, "--check")
	}
	if scenario.Direction == "reverse" {
		args = append(args, "--reverse")
	}
	command := exec.CommandContext(ctx, "git", args...)
	command.Stdin = strings.NewReader(scenario.Patch)
	for _, entry := range runner.env {
		if !strings.HasPrefix(entry, "GIT_") {
			command.Env = append(command.Env, entry)
		}
	}
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("historical patch %s: %w: %s", scenario.Direction, err, output)
	}
	return nil
}

func resetPrepared(ctx context.Context, runner *engine, prepared *preparedRevision) error {
	for _, args := range [][]string{{"-C", prepared.work.path, "reset", "--hard", prepared.revision.Commit}, {"-C", prepared.work.path, "clean", "-ffdx"}} {
		if _, err := runner.git(ctx, args...); err != nil {
			return err
		}
	}
	return nil
}

func compactCapture(capture *profile.BuildCapture) {
	if capture != nil {
		capture.Run.Graph.ActionGraph = nil
	}
} // Raw canonical graph retained, not duplicated.

func editedCorrectness(ctx context.Context, runner *engine, revision *Revision, options Options, result *ScenarioResult) (err error) {
	check := ScenarioCheck{Revision: revision.Ref, Commit: revision.Commit}
	defer func() {
		if err != nil {
			check.Failure = err.Error()
		}
		result.Correctness = append(result.Correctness, check)
	}()
	work, err := runner.worktree(ctx, revision.Commit, "scenario-correctness")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, work.close()) }()
	if err := installTestOverlays(work, revision, options.overlays); err != nil {
		return err
	}
	if err := applyScenario(ctx, runner, work, revision, result.Scenario); err != nil {
		return err
	}
	args := append([]string{"test", "-count=1"}, runner.flags...)
	if options.RaceTests {
		args = append(args, "-race")
	}
	args = append(args, revision.TestPackages...)
	_, check.Tests = runner.command(ctx, filepath.Join(work.path, runner.cwd), revision.correctnessCache, args...)
	if check.Tests.Failure != "" {
		return fmt.Errorf("edited correctness tests failed: %s", check.Tests.Log+check.Tests.Failure)
	}
	return nil
}

func runExpanded(ctx context.Context, runner *engine, report *Report, options Options) (err error) {
	report.SchemaVersion = 2
	report.CachePolicy = "cold pairs use independent empty caches; incremental scenarios and same-before-revision controls use byte-copied independent baseline caches, same source/output paths, and verified no-op warm builds; copies/preparation are untimed; shared module downloads; OS caches not flushed"
	if options.ColdRuns < 1 || options.ColdRuns > 100 {
		return fmt.Errorf("--cold-runs must be between 1 and 100")
	}
	if err := prepareScenarios(ctx, runner, report, options); err != nil {
		return err
	}
	for index := range report.Scenarios {
		scenario := &report.Scenarios[index]
		if scenario.Status == "unsupported" {
			continue
		}
		for _, revision := range []*Revision{&report.Before, &report.After} {
			if err := editedCorrectness(ctx, runner, revision, options, scenario); err != nil {
				scenario.Status = "correctness-failed"
				scenario.Diagnostics = append(scenario.Diagnostics, err.Error())
				break
			}
		}
	}
	if !slices.ContainsFunc(report.Scenarios, func(value ScenarioResult) bool { return value.Status == "prepared" }) {
		return fmt.Errorf("no scenario passed applicability and edited correctness checks")
	}
	prepared := make(map[string]*preparedRevision)
	defer func() {
		for _, value := range prepared {
			err = errors.Join(err, value.work.close())
		}
	}()
	for _, label := range []string{"before", "after"} {
		revision := &report.Before
		if label == "after" {
			revision = &report.After
		}
		work, err := runner.worktree(ctx, revision.Commit, "prepared-"+label)
		if err != nil {
			return err
		}
		prepared[label] = &preparedRevision{work: work, revision: revision}
	}
	// Cold repetitions are separate from incremental repetitions. Retain only
	// the first verified warm baseline as an immutable cache-copy template.
	for index := range options.ColdRuns {
		pair := Pair{Index: index + 1, Order: pairedOrder(index, "before", "after")}
		report.ColdPairs = append(report.ColdPairs, pair)
		current := &report.ColdPairs[len(report.ColdPairs)-1]
		for _, label := range pair.Order {
			runner.note("Cold pair %d/%d: %s", index+1, options.ColdRuns, label)
			value := prepared[label]
			capture := &Measurement{Revision: label, Commit: value.revision.Commit, Status: "failed", CachePolicy: "initially empty cold cache; verified warm baseline"}
			if label == "before" {
				current.Before = capture
			} else {
				current.After = capture
			}
			cache, err := runner.cache("cold-" + label)
			if err != nil {
				return err
			}
			capture.Cold = runner.capture(ctx, value.work, cache, options.Target)
			compactCapture(capture.Cold)
			if capture.Cold.Failure != "" || capture.Cold.Run.Graph.ExecutedBuildActions == 0 {
				capture.Reason = "cold build failed or empty cache executed no build commands"
				return fmt.Errorf("%s: %s", label, capture.Reason)
			}
			capture.Warm = runner.capture(ctx, value.work, cache, options.Target)
			compactCapture(capture.Warm)
			if capture.Warm.Failure != "" || capture.Warm.Run.Graph.ExecutedActions != 0 {
				capture.Reason = "cold baseline did not warm to verified no-op"
				return fmt.Errorf("%s: %s", label, capture.Reason)
			}
			capture.Status = "measured"
			if index == 0 {
				value.cache = cache
				value.binary = filepath.Join(runner.temp, "baseline-binary-"+label)
				if err := copyCacheFile(filepath.Join(filepath.Dir(value.work.path), "binary"), value.binary); err != nil {
					return err
				}
			} else {
				if err := os.RemoveAll(cache); err != nil {
					return err
				}
			}
		}
	}
	report.ColdSummary = summarize(report.ColdPairs)
	for run := range options.Runs {
		for index := range report.Scenarios {
			scenario := &report.Scenarios[index]
			if scenario.Status == "unsupported" || scenario.Status == "correctness-failed" {
				continue
			}
			pair := Pair{Index: run + 1, Order: pairedOrder(run, "before", "after")}
			control := Pair{Index: run + 1, Order: pairedOrder(run, "control-a", "control-b")}
			scenario.Pairs = append(scenario.Pairs, pair)
			if options.Controls {
				scenario.Controls = append(scenario.Controls, control)
			}
			// Bracket treatment/control order across runs instead of always measuring
			// controls at the end. Both control arms use the SAME immutable before ref.
			order := append([]string(nil), pair.Order...)
			if options.Controls {
				if run%2 == 0 {
					order = append(order, control.Order...)
				} else {
					order = append(append([]string(nil), control.Order...), order...)
				}
			}
			for _, label := range order {
				runner.note("Scenario %s pair %d/%d: %s", scenario.Scenario.Name, run+1, options.Runs, label)
				value := prepared["before"]
				if label == "after" {
					value = prepared["after"]
				}
				scenario.Schedule = append(scenario.Schedule, ExecutionStep{Run: run + 1, Label: label, Commit: value.revision.Commit})
				measured := measureScenario(ctx, runner, value, label, options.Target, scenario.Scenario)
				current := &scenario.Pairs[len(scenario.Pairs)-1]
				if strings.HasPrefix(label, "control-") {
					current = &scenario.Controls[len(scenario.Controls)-1]
				}
				if label == "before" || label == "control-a" {
					current.Before = measured
				} else {
					current.After = measured
				}
				if measured.Status != "measured" {
					scenario.Status = "failed"
					scenario.Diagnostics = append(scenario.Diagnostics, measured.Reason)
					return fmt.Errorf("scenario %s run %d %s: %s", scenario.Scenario.Name, run+1, label, measured.Reason)
				}
			}
			scenario.Status = "measured"
		}
	}
	report.PerformanceValid = true
	for index := range report.Scenarios {
		scenario := &report.Scenarios[index]
		scenario.Summary = summarize(scenario.Pairs)
		scenario.ControlSummary = summarize(scenario.Controls)
		scenario.Noise = noiseConclusion(scenario.Pairs, scenario.Controls)
		scenario.Structure = structuralCounts(report, scenario.Pairs)
		if scenario.Status != "measured" {
			report.PerformanceValid = false
		}
	}
	if !report.PerformanceValid {
		return fmt.Errorf("one or more requested scenarios are unsupported or failed correctness; inspect per-scenario results")
	}
	return nil
}

func pairedOrder(index int, left, right string) []string {
	if index%2 == 0 {
		return []string{left, right}
	}
	return []string{right, left}
}

func measureScenario(ctx context.Context, runner *engine, prepared *preparedRevision, label, target string, scenario Scenario) *Measurement {
	result := &Measurement{Revision: label, Commit: prepared.revision.Commit, Status: "failed", CachePolicy: "independent byte-copy of same revision's immutable warmed baseline; source/output paths unchanged; verified warm no-op; scenario applied after warm"}
	if err := measureScenarioBuilds(ctx, runner, prepared, target, scenario, result); err != nil {
		result.Reason = err.Error()
	} else {
		result.Status = "measured"
	}
	return result
}

func measureScenarioBuilds(ctx context.Context, runner *engine, prepared *preparedRevision, target string, scenario Scenario, result *Measurement) (err error) {
	if err := resetPrepared(ctx, runner, prepared); err != nil {
		return err
	}
	cache, err := runner.cache("scenario")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(cache)) }()
	if err := copyCache(ctx, prepared.cache, cache); err != nil {
		return err
	}
	output := filepath.Join(filepath.Dir(prepared.work.path), "binary")
	if err := os.Remove(output); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := copyCacheFile(prepared.binary, output); err != nil {
		return err
	}
	result.Warm = runner.capture(ctx, prepared.work, cache, target)
	compactCapture(result.Warm)
	if result.Warm.Failure != "" || result.Warm.Run.Graph.ExecutedActions != 0 {
		return fmt.Errorf("scenario cache copy did not produce no-op warm build")
	}
	if err := applyScenario(ctx, runner, prepared.work, prepared.revision, scenario); err != nil {
		return err
	}
	result.ProbeSHA256 = scenario.SHA256
	result.Incremental = runner.capture(ctx, prepared.work, cache, target)
	compactCapture(result.Incremental)
	if result.Incremental.Failure != "" {
		return fmt.Errorf("scenario incremental build: %s", result.Incremental.Failure)
	}
	for _, action := range result.Incremental.Run.Graph.ExecutedActionDetails {
		if action.CompileCommands > 0 {
			result.Recompiled = append(result.Recompiled, action.Package)
		}
		if action.LinkCommands > 0 {
			result.Linked = append(result.Linked, action.Package)
		}
	}
	slices.Sort(result.Recompiled)
	slices.Sort(result.Linked)
	if !slices.Contains(result.Recompiled, prepared.revision.ChangedPackage) {
		return fmt.Errorf("scenario did not compile controlled package")
	}
	return nil
}

func copyCache(ctx context.Context, source, destination string) error {
	// No hard links: cache entries and metadata in one observation cannot mutate
	// the immutable template or another observation. Check cancellation per file.
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("unexpected cache symlink")
		}
		return copyCacheFile(path, target)
	})
}
