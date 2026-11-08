package history

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestNonGoClassificationsAndEventAttribution(t *testing.T) {
	root := t.TempDir()
	nodes := []PackageNode{
		{ImportPath: "example.org/app", Dir: filepath.Join(root, "app"), Imports: []string{"example.org/lib"}},
		{ImportPath: "example.org/lib", Dir: filepath.Join(root, "lib"), EmbedFiles: []string{"README.md"}, EmbedPatterns: []string{"assets"}, BuildFiles: []string{"native.c"}},
	}
	files := []string{"README.md", "docs/guide.rst", ".github/workflows/test.yml", "lib/testdata/case.json", "lib/README.md", "lib/assets/deleted/nested.txt", "lib/native.c", "lib/new.s", "lib/schema.proto", "go.mod", "lib/config.json", "unknown.dat"}
	var changes []FileChange
	for _, file := range files {
		changes = append(changes, FileChange{Path: file})
	}
	report, err := Analyze(root, nodes, []Event{{Commit: "mixed", Files: changes}})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"unlikely", "unlikely", "unlikely", "unlikely", "potential_build", "potential_build", "potential_build", "potential_build", "potential_build", "potential_build", "unknown", "unknown"}
	for index, file := range report.Events[0].Files {
		if file.Classification != want[index] || file.Reason == "" {
			t.Errorf("%s = %s (%s), want %s", file.Path, file.Classification, file.Reason, want[index])
		}
	}
	if report.NonGoChanges.Files != (ClassificationCounts{Unlikely: 4, PotentialBuild: 6, Unknown: 2}) || report.NonGoChanges.Events != (ClassificationCounts{Unlikely: 1, PotentialBuild: 1, Unknown: 1}) {
		t.Fatalf("file/event classification counts double-counted: %+v", report.NonGoChanges)
	}
	event := report.Events[0]
	if !slices.Equal(event.BuildPackageDirs, []string{"lib"}) || !slices.Equal(event.EstimatedAffectedPackages, []string{"example.org/app", "example.org/lib"}) {
		t.Fatal("known inputs should seed one package event and union its consumers", event)
	}
	for _, pkg := range report.Packages {
		if pkg.Dir == "lib" && (pkg.Changes != 1 || pkg.BuildChanges != 1) {
			t.Fatal("multiple input files incorrectly count as multiple build events", pkg)
		}
	}
	// Unknown and config-wide files do not invalidate the entire target.
	report, err = Analyze(root, nodes, []Event{{Files: []FileChange{{Path: "go.mod"}, {Path: "lib/config.json"}, {Path: "README.md"}}}})
	if err != nil || len(report.Events[0].EstimatedAffectedPackages) != 0 || len(report.Events[0].PackageDirs) != 0 || len(report.Events[0].UnmodeledFiles) != 2 {
		t.Fatal("non-Go heuristics invented target-wide invalidation", report.Events, err)
	}
	out := FormatText(report, 10)
	if !strings.Contains(out, "1 unlikely / 1 potentially build-relevant / 1 unknown") || strings.Contains(out, "Tier") {
		t.Fatal("non-Go uncertainty or simplified ranking missing", out)
	}
}

func TestSharedEmbedInputCountsOneEvent(t *testing.T) {
	root := t.TempDir()
	nodes := []PackageNode{
		{ImportPath: "parent", Dir: filepath.Join(root, "parent"), EmbedFiles: []string{"child/shared.txt"}},
		{ImportPath: "child", Dir: filepath.Join(root, "parent", "child"), EmbedFiles: []string{"shared.txt"}},
	}
	report, err := Analyze(root, nodes, []Event{{Commit: "shared", Files: []FileChange{{Path: "parent/child/shared.txt"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(report.Events[0].BuildPackageDirs, []string{"parent", "parent/child"}) || report.NonGoChanges.Files.PotentialBuild != 1 || report.NonGoChanges.Events.PotentialBuild != 1 || report.CoChanges[0].Commits != 1 {
		t.Fatal("shared asset was lost or counted as multiple events", report)
	}
}

func TestCoChangeConditionalProportions(t *testing.T) {
	report, err := Analyze(t.TempDir(), nil, []Event{
		{Commit: "one", Files: []FileChange{{Path: "a/first.go"}, {Path: "a/second.go"}, {Path: "b/lib.go"}}},
		{Commit: "two", Files: []FileChange{{Path: "a/first.go"}}},
		{Commit: "three", Files: []FileChange{{Path: "a/lib_test.go"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	pair := report.CoChanges[0]
	if pair.Commits != 1 || pair.PackageChanges != [2]int{3, 1} || pair.Shares != [2]float64{1.0 / 3, 1} {
		t.Fatal("conditional co-change proportions are incorrect", pair)
	}
	out := FormatText(report, 10)
	for _, token := range []string{"33% of a [1/3]", "100% of b [1/1]", "small counts are not coupling evidence"} {
		if !strings.Contains(out, token) {
			t.Errorf("missing %q in report", token)
		}
	}
}
