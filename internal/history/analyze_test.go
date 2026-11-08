package history

import (
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestEventUnionsAndCoChanges(t *testing.T) {
	root := t.TempDir()
	node := func(name string, imports ...string) PackageNode {
		return PackageNode{ImportPath: "example.org/" + name, Dir: filepath.Join(root, name), Imports: imports}
	}
	nodes := []PackageNode{
		node("app", "example.org/a", "example.org/shared", "example.org/b"),
		node("a", "example.org/shared"), node("shared"), node("b"),
	}
	stamp := time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC)
	events := []Event{
		{Commit: "one", TimeUTC: stamp, Files: []FileChange{{Path: "a/one.go"}, {Path: "a/two.go"}, {Path: "shared/lib.go"}}},
		{Commit: "two", TimeUTC: stamp, Files: []FileChange{{Path: "a/lib_test.go"}, {Path: "shared/lib_test.go"}}},
		{Commit: "three", TimeUTC: stamp.AddDate(0, 1, 0), Files: []FileChange{{Path: "outside/deleted.go"}, {Path: "go.mod"}}},
	}
	report, err := Analyze(root, nodes, events)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"example.org/a", "example.org/app", "example.org/shared"}; !reflect.DeepEqual(report.Events[0].EstimatedAffectedPackages, want) {
		t.Fatalf("event union = %v, want %v", report.Events[0].EstimatedAffectedPackages, want)
	}
	if len(report.Events[1].EstimatedAffectedPackages) != 0 || len(report.Events[1].BuildPackageDirs) != 0 {
		t.Fatal("test-only event should not predict go build invalidation")
	}
	if !reflect.DeepEqual(report.Events[2].UnmodeledFiles, []string{"go.mod"}) {
		t.Fatal("config changes not retained as unmodeled")
	}
	if len(report.CoChanges) != 1 || report.CoChanges[0].Commits != 2 || report.CoChanges[0].Packages != [2]string{"a", "shared"} || report.CoChanges[0].Shares != [2]float64{1, 1} || report.CoChanges[0].PackageChanges != [2]int{2, 2} {
		t.Fatalf("co-changes counted per file rather than event: %+v", report.CoChanges)
	}
	byDir := make(map[string]PackageImpact)
	for _, pkg := range report.Packages {
		byDir[pkg.Dir] = pkg
	}
	if pkg := byDir["a"]; pkg.Changes != 2 || pkg.BuildChanges != 1 || len(pkg.Commits) != 2 || pkg.Monthly[0].Changes != 2 {
		t.Fatalf("incorrect a stats: %+v", pkg)
	}
	if pkg := byDir["shared"]; !reflect.DeepEqual(pkg.PotentialPath, []string{"example.org/shared", "example.org/a", "example.org/app"}) {
		t.Fatalf("incorrect shared path/tier: %+v", pkg)
	}
	if pkg := byDir["outside"]; pkg.InTarget || pkg.Changes != 1 {
		t.Fatalf("outside package lost or ranked: %+v", pkg)
	}
	if events[0].Files[0].PackageDir != "" {
		t.Fatal("analyzer mutated input events")
	}
	repeated, err := Analyze(root, nodes, events)
	if err != nil || !reflect.DeepEqual(repeated, report) {
		t.Fatal("analysis is not deterministic", err)
	}
	report.FocusPackage = "a"
	out := FormatText(report, 1)
	for _, token := range []string{"estimated, not measured", "no replay", "one", "event union", "a/one.go", "a/two.go", "shared/lib.go", "a + shared"} {
		if !strings.Contains(out, token) {
			t.Errorf("missing %q in %s", token, out)
		}
	}
}

func TestFrequencyRankingIgnoresReach(t *testing.T) {
	packages := []PackageImpact{
		{Dir: "frequent-leaf", InTarget: true, BuildChanges: 10},
		{Dir: "broad", InTarget: true, BuildChanges: 2, ReverseDependencies: []string{"a", "b"}},
		{Dir: "rare-leaf", InTarget: true, BuildChanges: 1},
		{Dir: "equal-broad", InTarget: true, BuildChanges: 2, ReverseDependencies: []string{"a", "b"}},
		{Dir: "test-only", InTarget: true, Changes: 100},
		{Dir: "outside", BuildChanges: 100},
	}
	rankPackages(packages)
	var dirs []string
	for _, pkg := range packages {
		dirs = append(dirs, pkg.Dir)
	}
	if !slices.Equal(dirs, []string{"outside", "frequent-leaf", "broad", "equal-broad", "rare-leaf", "test-only"}) {
		t.Fatal("ranking should use build-event frequency, not reach or a tier", dirs)
	}
}

func TestGraphValidationAndReach(t *testing.T) {
	for _, nodes := range [][]PackageNode{
		{{ImportPath: "a"}, {ImportPath: "a"}},
		{{ImportPath: "a", Imports: []string{"missing"}}},
		{{ImportPath: "a", Imports: []string{"b"}}, {ImportPath: "b", Imports: []string{"a"}}},
	} {
		if _, err := analyzeGraph(nodes); err != nil {
			continue
		}
		t.Fatalf("accepted bad graph: %+v", nodes)
	}
	impact, err := analyzeGraph([]PackageNode{
		{ImportPath: "leaf"},
		{ImportPath: "a", Imports: []string{"leaf"}},
		{ImportPath: "b", Imports: []string{"leaf"}},
		{ImportPath: "app", Imports: []string{"a", "b"}},
	})
	if err != nil || !slices.Equal(impact.consumers["leaf"], []string{"a", "app", "b"}) {
		t.Fatal("diamond reach should be deduplicated", impact, err)
	}
}

func TestParseSince(t *testing.T) {
	now := time.Date(2025, 3, 1, 12, 0, 0, 0, time.UTC)
	for input, want := range map[string]string{
		"1d": "2025-02-28T12:00:00Z", "90d": "2024-12-01T12:00:00Z",
		"2025-01-01": "2025-01-01T00:00:00Z", "2025-01-01T12:00:00+02:00": "2025-01-01T10:00:00Z",
	} {
		got, err := ParseSince(input, now)
		if err != nil || got.Format(time.RFC3339) != want {
			t.Errorf("ParseSince(%s) = %v, %v; want %s", input, got, err, want)
		}
	}
	for _, input := range []string{"", "0d", "-1d", "1.5d", "36501d", "999999999999999999999d", "yesterday", "2025-02-30"} {
		if _, err := ParseSince(input, now); err != nil {
			continue
		}
		t.Errorf("accepted %q", input)
	}
}
