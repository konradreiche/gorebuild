package verify

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/konradreiche/gorebuild/internal/explain"
)

func digest(content string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(content))) }

func implementationScenario(ctx context.Context, runner *engine, before, after *Revision) (Scenario, error) {
	files := append([]string(nil), before.SourceFiles...)
	sort.Strings(files)
	for _, name := range files {
		file := path.Join(before.PackageDirectory, name)
		left, err := runner.git(ctx, "show", before.Commit+":"+file)
		if err != nil {
			return Scenario{}, err
		}
		right, err := runner.git(ctx, "show", after.Commit+":"+file)
		if err != nil {
			continue
		}
		if string(left) != string(right) {
			continue
		}
		fset := token.NewFileSet()
		syntax, err := parser.ParseFile(fset, name, left, parser.ParseComments)
		if err != nil {
			return Scenario{}, err
		}
		if ast.IsGenerated(syntax) {
			continue
		}
		for _, node := range syntax.Decls {
			function, ok := node.(*ast.FuncDecl)
			if !ok || function.Body == nil || function.Name.Name == "init" {
				continue
			}
			offset := fset.Position(function.Body.Lbrace).Offset + 1
			// An empty block on the existing line changes the compilation input without
			// changing declarations, runtime work or subsequent source line numbers.
			content := string(left[:offset]) + " /* gorebuild implementation-only */ {}; " + string(left[offset:])
			if _, err := parser.ParseFile(token.NewFileSet(), name, content, 0); err != nil {
				return Scenario{}, err
			}
			return Scenario{Name: "implementation", Kind: "implementation", File: name, OriginalSHA256: digest(string(left)), Content: content, SHA256: digest(content), SelectedFiles: []string{file}, Behavior: "same-line empty block inside existing " + function.Name.Name + "; exported declarations unchanged, no executable work added; downstream propagation is measured, not assumed"}, nil
		}
	}
	return Scenario{}, fmt.Errorf("no identical non-generated function source in both revisions for an implementation-only edit")
}

func prepareScenarios(ctx context.Context, runner *engine, report *Report, options Options) error {
	for _, kind := range options.Scenarios {
		switch kind {
		case "implementation":
			value, err := implementationScenario(ctx, runner, &report.Before, &report.After)
			result := ScenarioResult{Scenario: value, Status: "prepared"}
			if err != nil {
				result.Scenario = Scenario{Name: kind, Kind: kind}
				result.Status = "unsupported"
				result.Diagnostics = []string{err.Error()}
			}
			report.Scenarios = append(report.Scenarios, result)
		case "historical":
			continue
		case "exported":
			probe := makeProbe(report.Before.PackageName)
			report.Scenarios = append(report.Scenarios, ScenarioResult{Scenario: Scenario{Name: "exported", Kind: "exported", File: probe.File, Content: probe.Content, SHA256: probe.SHA256, SelectedFiles: []string{path.Join(report.Before.PackageDirectory, probe.File)}, Behavior: probe.Purpose}, Status: "prepared"})
		default:
			return fmt.Errorf("unsupported edit scenario %q", kind)
		}
	}
	candidates := append([]string(nil), options.HistoricalCommits...)
	automatic := len(candidates) == 0 && options.HistoryFile != ""
	if automatic {
		data, err := explain.Read(options.HistoryFile)
		if err != nil {
			return err
		}
		for _, event := range data.Events {
			for _, file := range event.Files {
				if path.Dir(file.Path) == report.Before.PackageDirectory && strings.HasSuffix(file.Path, ".go") && !strings.HasSuffix(file.Path, "_test.go") {
					candidates = append(candidates, event.Commit)
					break
				}
			}
		}
	}
	for _, ref := range candidates {
		value, err := historicalScenario(ctx, runner, report, ref, options)
		if err != nil {
			if automatic {
				report.ScenarioSelection = append(report.ScenarioSelection, ref+": "+err.Error())
				continue
			}
			report.Scenarios = append(report.Scenarios, ScenarioResult{Scenario: Scenario{Name: "historical-" + ref, Kind: "historical", Commit: ref}, Status: "unsupported", Diagnostics: []string{err.Error()}})
			continue
		}
		report.Scenarios = append(report.Scenarios, ScenarioResult{Scenario: value, Status: "prepared"})
		if automatic {
			break
		}
	}
	if automatic && !slices.ContainsFunc(report.Scenarios, func(result ScenarioResult) bool { return result.Scenario.Kind == "historical" }) {
		report.Scenarios = append(report.Scenarios, ScenarioResult{Scenario: Scenario{Name: "historical", Kind: "historical"}, Status: "unsupported", Diagnostics: []string{"no isolated, applicable historical source patch in the supplied history"}})
	}
	return nil
}

func historicalScenario(ctx context.Context, runner *engine, report *Report, ref string, options Options) (Scenario, error) {
	commit, err := resolveRevision(ctx, runner, ref)
	if err != nil {
		return Scenario{}, err
	}
	output, err := runner.git(ctx, "rev-list", "--parents", "-n", "1", commit)
	if err != nil {
		return Scenario{}, err
	}
	parents := strings.Fields(string(output))
	if len(parents) != 2 {
		return Scenario{}, fmt.Errorf("historical scenario requires a single available parent (root/merges unsupported)")
	}
	parent := parents[1]
	output, err = runner.git(ctx, "diff", "--name-only", "--no-renames", "-z", parent, commit, "--")
	if err != nil {
		return Scenario{}, err
	}
	var all, selected []string
	for _, file := range strings.Split(string(output), "\x00") {
		if file == "" {
			continue
		}
		all = append(all, file)
		if path.Dir(file) == report.Before.PackageDirectory && strings.HasSuffix(file, ".go") && !strings.HasSuffix(file, "_test.go") {
			selected = append(selected, file)
		}
	}
	if len(selected) == 0 {
		return Scenario{}, fmt.Errorf("no regular Go source changes in controlled package")
	}
	isolated := len(all) == len(selected)
	if !isolated && !options.HistoricalSubset {
		return Scenario{}, fmt.Errorf("historical event also changes unrelated/test/configuration files; explicit --historical-subset required, not an isolated package event")
	}
	args := append([]string{"diff", "--name-status", "--no-renames", parent, commit, "--"}, selected...)
	output, err = runner.git(ctx, args...)
	if err != nil {
		return Scenario{}, err
	}
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if !strings.HasPrefix(line, "M\t") {
			return Scenario{}, fmt.Errorf("historical scenario supports modifications to existing source files only")
		}
	}
	for _, file := range selected {
		left, err := runner.git(ctx, "show", report.Before.Commit+":"+file)
		if err != nil {
			return Scenario{}, err
		}
		right, err := runner.git(ctx, "show", report.After.Commit+":"+file)
		if err != nil {
			return Scenario{}, err
		}
		if string(left) != string(right) {
			return Scenario{}, fmt.Errorf("historical patch preimages differ across revisions")
		}
	}
	args = append([]string{"diff", "--binary", "--no-ext-diff", "--no-renames", parent, commit, "--"}, selected...)
	patch, err := runner.git(ctx, args...)
	if err != nil {
		return Scenario{}, err
	}
	value := Scenario{Name: "historical-" + short(commit), Kind: "historical", Patch: string(patch), SHA256: digest(string(patch)), Commit: commit, Parent: parent, EventFiles: all, SelectedFiles: selected, IsolatedHistoricalEvent: isolated, Behavior: "real historical source delta; behavioral equivalence NOT assumed; edited-revision tests required; inverse direction is recorded explicitly"}
	directions := []string{"forward", "reverse"}
	if options.HistoricalDirection != "" && options.HistoricalDirection != "auto" {
		directions = []string{options.HistoricalDirection}
	}
	for _, direction := range directions {
		if direction != "forward" && direction != "reverse" {
			return Scenario{}, fmt.Errorf("historical direction must be auto, forward or reverse")
		}
		value.Direction = direction
		applicable := true
		for _, revision := range []*Revision{&report.Before, &report.After} {
			if err := checkHistoricalPatch(ctx, runner, revision, value); err != nil {
				applicable = false
				break
			}
		}
		if applicable {
			return value, nil
		}
	}
	return Scenario{}, fmt.Errorf("historical patch conflicts or is inapplicable in one/both revisions in requested directions")
}

func checkHistoricalPatch(ctx context.Context, runner *engine, revision *Revision, scenario Scenario) (err error) {
	work, err := runner.worktree(ctx, revision.Commit, "patch-check")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, work.close()) }()
	return patchCommand(ctx, runner, work, scenario, true)
}

func applyScenario(ctx context.Context, runner *engine, work *tree, revision *Revision, scenario Scenario) error {
	if scenario.Kind == "historical" {
		return patchCommand(ctx, runner, work, scenario, false)
	}
	if scenario.Kind == "exported" {
		return applyProbe(work, revision, Probe{File: scenario.File, Content: scenario.Content, SHA256: scenario.SHA256})
	}
	file := filepath.Join(work.path, filepath.FromSlash(revision.PackageDirectory), scenario.File)
	resolved, err := filepath.EvalSymlinks(file)
	if err != nil {
		return err
	}
	if !inside(work.path, resolved) {
		return fmt.Errorf("scenario source resolves outside worktree")
	}
	content, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	if digest(string(content)) != scenario.OriginalSHA256 {
		return fmt.Errorf("implementation source differs from frozen scenario input")
	}
	return os.WriteFile(file, []byte(scenario.Content), 0o600)
}
