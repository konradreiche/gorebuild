package verify

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/konradreiche/gorebuild/internal/profile"
)

func TestExpandedScenariosAndIndependentControls(t *testing.T) {
	t.Setenv("GOWORK", "off")
	t.Setenv("GOFLAGS", "")
	normal := t.TempDir()
	t.Setenv("GOCACHE", normal)
	root, before, after := repository(t)
	t.Chdir(root)
	status := testGit(t, root, "status", "--porcelain")
	trees := testGit(t, root, "worktree", "list", "--porcelain")
	file := filepath.Join(t.TempDir(), "expanded.json")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	report := Run(ctx, Options{Target: "./cmd/app", Before: before, After: after, Package: "./leaf", Runs: 2, ColdRuns: 1, Controls: true, Scenarios: []string{"implementation", "exported"}, OutputFile: file})
	if len(report.Errors) > 0 || !report.PerformanceValid {
		t.Fatal(report.Errors)
	}
	if len(report.ColdPairs) != 1 || report.ColdSummary.ValidPairs != 1 || len(report.Scenarios) != 2 {
		t.Fatal("cold/incremental scenarios missing")
	}
	for _, scenario := range report.Scenarios {
		if scenario.Status != "measured" || len(scenario.Pairs) != 2 || len(scenario.Controls) != 2 || len(scenario.Correctness) != 2 {
			t.Fatal(scenario.Status, scenario.Diagnostics)
		}
		if !slices.Equal(scenario.Pairs[1].Order, []string{"after", "before"}) || !slices.Equal(scenario.Controls[1].Order, []string{"control-b", "control-a"}) {
			t.Fatal("execution order not alternated")
		}
		for _, pair := range scenario.Controls {
			if pair.Before.Commit != before || pair.After.Commit != before {
				t.Fatal("control compared different revisions")
			}
		}
		for _, pair := range append(append([]Pair(nil), scenario.Pairs...), scenario.Controls...) {
			for _, value := range []*Measurement{pair.Before, pair.After} {
				if value.Cold != nil || value.Warm.Run.Graph.ExecutedActions != 0 || value.ProbeSHA256 != scenario.Scenario.SHA256 || !slices.Contains(value.Recompiled, "example.org/verify/leaf") {
					t.Fatal("cache copy/preparation invalid", value)
				}
			}
		}
	}
	entries, err := os.ReadDir(normal)
	if err != nil || len(entries) != 0 {
		t.Fatal("normal cache touched", entries, err)
	}
	if testGit(t, root, "status", "--porcelain") != status || testGit(t, root, "worktree", "list", "--porcelain") != trees {
		t.Fatal("developer state changed")
	}
	content, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var saved Report
	if err := json.Unmarshal(content, &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.Scenarios) != 2 || len(saved.Scenarios[0].Controls) != 2 {
		t.Fatal("single artifact lost scenario evidence")
	}
}

func TestHistoricalSelectionScopeAndConflicts(t *testing.T) {
	t.Setenv("GOWORK", "off")
	root, before, after := repository(t)
	// Add an isolated source change to both experiment revisions' source basis.
	writeTestFile(t, root, "leaf/leaf.go", "package leaf\nfunc Value() int{return 8}\n")
	testGit(t, root, "add", ".")
	testGit(t, root, "commit", "-m", "historical leaf change")
	change := testGit(t, root, "rev-parse", "HEAD")
	runner := testEngine(t, root, "go")
	report := &Report{Before: Revision{Commit: change, PackageDirectory: "leaf"}, After: Revision{Commit: change, PackageDirectory: "leaf"}}
	scenario, err := historicalScenario(context.Background(), runner, report, change, Options{HistoricalDirection: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	if scenario.Direction != "reverse" || !scenario.IsolatedHistoricalEvent || len(scenario.SelectedFiles) != 1 || scenario.Commit != change || scenario.Parent != after {
		t.Fatal(scenario)
	}
	work, err := runner.worktree(context.Background(), change, "historical-test")
	if err != nil {
		t.Fatal(err)
	}
	defer work.close()
	if err := applyScenario(context.Background(), runner, work, &report.Before, scenario); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(work.path, "leaf/leaf.go"))
	if err != nil || !strings.Contains(string(data), "return 7") {
		t.Fatal("inverse patch not applied", err)
	}
	writeTestFile(t, root, "leaf/leaf.go", "package leaf\nfunc Value() int{return 9}\n")
	writeTestFile(t, root, "api/api.go", "package api\nfunc Value(input int)int{return input+1}\n")
	testGit(t, root, "add", ".")
	testGit(t, root, "commit", "-m", "multi package")
	multi := testGit(t, root, "rev-parse", "HEAD")
	report.Before.Commit = multi
	report.After.Commit = multi
	if _, err := historicalScenario(context.Background(), runner, report, multi, Options{}); err != nil {
		if !strings.Contains(err.Error(), "unrelated") {
			t.Fatal(err)
		}
	} else {
		t.Fatal("multi-package event mislabeled isolated")
	}
	scenario, err = historicalScenario(context.Background(), runner, report, multi, Options{HistoricalSubset: true})
	if err != nil {
		t.Fatal(err)
	}
	if scenario.IsolatedHistoricalEvent || len(scenario.EventFiles) != 2 || len(scenario.SelectedFiles) != 1 {
		t.Fatal("subset provenance lost", scenario)
	}
	report.Before.Commit = before
	report.After.Commit = multi
	if _, err := historicalScenario(context.Background(), runner, report, change, Options{HistoricalDirection: "forward"}); err != nil {
		return
	}
	t.Fatal("conflicting/non-equivalent historical bases accepted")
}

func TestSameRevisionNoiseAndCachedDownstreamReporting(t *testing.T) {
	observation := func(wall int64, compiled []string) *Measurement {
		return &Measurement{Status: "measured", Incremental: &profile.BuildCapture{Run: profile.BuildRun{WallTimeNS: wall}}, Recompiled: compiled}
	}
	var treatment, controls []Pair
	for range 10 {
		treatment = append(treatment, Pair{Before: observation(100, nil), After: observation(80, nil)})
		controls = append(controls, Pair{Before: observation(100, nil), After: observation(125, nil)})
	}
	if result := noiseConclusion(treatment, controls); result.Confidence != "Inconclusive" {
		t.Fatal("normal timing variation overclaimed", result)
	}
	for index := range controls {
		controls[index].After.Incremental.Run.WallTimeNS = 101
	}
	if result := noiseConclusion(treatment, controls); !strings.HasPrefix(result.Confidence, "Consistent") {
		t.Fatal(result)
	}
	treatment[0].After.Incremental.Run.WallTimeNS = 110
	if result := noiseConclusion(treatment, controls); result.Confidence != "Inconclusive" {
		t.Fatal("mixed-sign timing treated as significant", result)
	}
	report := &Report{Before: Revision{ImportPath: "app", ChangedPackage: "leaf"}}
	counts := structuralCounts(report, treatment)
	if counts[0].BeforeCompiled != 0 || !strings.Contains(counts[0].Conclusion, "cached in both") {
		t.Fatal("no propagation mislabeled avoided compilation", counts)
	}
}

func TestCacheCopyIsNotHardLinked(t *testing.T) {
	source := t.TempDir()
	destination := t.TempDir()
	writeTestFile(t, source, "aa/data", "original")
	if err := copyCache(context.Background(), source, destination); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, "aa/data"), []byte("mutated"), 0o600); err != nil {
		t.Fatal(err)
	}
	value, err := os.ReadFile(filepath.Join(source, "aa/data"))
	if err != nil || string(value) != "original" {
		t.Fatal("cache template mutated", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := copyCache(ctx, source, t.TempDir()); err != nil {
		return
	}
	t.Fatal("cancelled cache copy continued")
}
