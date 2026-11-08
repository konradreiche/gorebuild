package explain

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/konradreiche/gorebuild/internal/history"
)

func suggestFixture(t *testing.T, provider, consumer string) (*Dataset, string) {
	t.Helper()
	report, root := fixture(t)
	sourceFile(t, root, "dep/utility.go", provider)
	sourceFile(t, root, "dep/churn.go", "package dep\nfunc Churn() int {return 1}\n")
	sourceFile(t, root, "consumer/consumer.go", consumer)
	git := func(args ...string) string {
		t.Helper()
		output, err := sourceGit(context.Background(), root, args...)
		if err != nil {
			t.Fatal(args, err)
		}
		return strings.TrimSpace(string(output))
	}
	git("init")
	git("config", "user.email", "test@example.org")
	git("config", "user.name", "Test")
	git("add", ".")
	git("commit", "-m", "initial")
	parent := git("rev-parse", "HEAD")
	sourceFile(t, root, "dep/churn.go", "package dep\nfunc Churn() int {return 2}\n")
	git("add", ".")
	git("commit", "-m", "independent change")
	head := git("rev-parse", "HEAD")
	report.Metadata.Head = head
	event := report.Events[0]
	event.Commit = head
	event.Parents = []string{parent}
	event.Files = []history.FileChange{{Path: "dep/churn.go", PackageDir: "dep", Classification: "go_source"}}
	report.Events = []history.Event{event}
	data, err := Read(saveFixture(t, report, false))
	if err != nil {
		t.Fatal(err)
	}
	return data, root
}

func TestSuggestIndependentClosedGroupAndCompetingPathBound(t *testing.T) {
	data, root := suggestFixture(t, "package dep\nfunc Stable() int {return helper()}\nfunc helper() int {return 7}\n", "package consumer\nimport \"example.org/repo/dep\"\nvar Value=dep.Stable()\n")
	cache := t.TempDir()
	t.Setenv("GOCACHE", cache)
	before, err := sourceGit(context.Background(), root, "status", "--porcelain")
	if err != nil {
		t.Fatal(err)
	}
	module, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	report, err := Suggest(context.Background(), data, "dep", root, "", TypeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Candidates) != 1 {
		t.Fatal(report)
	}
	candidate := report.Candidates[0]
	if candidate.Status != "requires validation" || !slices.Equal(candidate.Declarations, []string{"Stable", "helper"}) || len(candidate.RemovedEdges) != 1 {
		t.Fatal(candidate)
	}
	if candidate.ProviderChangeEvents != 1 || candidate.GroupChangeEvents != 0 || candidate.IndependentlyChangingEvents != 1 || candidate.ConditionalEvents != 1 {
		t.Fatal("history attribution wrong", candidate)
	}
	evidence := candidate.Events[0]
	if evidence.MaximumSavingNS != int64(5*time.Second) || evidence.WithoutActionNS != int64(75*time.Second) {
		t.Fatal("consumer duration was mistaken for savings", evidence)
	}
	after, err := sourceGit(context.Background(), root, "status", "--porcelain")
	if err != nil || string(before) != string(after) {
		t.Fatal("suggest modified source", err)
	}
	unchanged, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil || string(unchanged) != string(module) {
		t.Fatal("suggest modified module", err)
	}
	if err := filepath.WalkDir(cache, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.HasPrefix(string(content), "!<arch>\n") {
			t.Error("suggest compiled archive", path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(report.Describe(data, 5), "Single-event theoretical bound: 5.000s") {
		t.Fatal("sparse estimate mislabeled")
	}
}

func TestSuggestRejectsGroupsThatLeaveProviderEdges(t *testing.T) {
	data, root := suggestFixture(t, "package dep\nfunc A() int {return 1}\nfunc B() int {return 2}\n", "package consumer\nimport \"example.org/repo/dep\"\nvar A=dep.A()\nvar B=dep.B()\n")
	report, err := Suggest(context.Background(), data, "dep", root, "", TypeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Candidates) != 3 {
		t.Fatal(report.Candidates)
	}
	for _, candidate := range report.Candidates {
		if candidate.Status != "rejected" {
			t.Fatal("disconnected exports offered as cohesive extraction", candidate)
		}
	}
}

func TestSuggestStatePrivateIncomingAndIncompleteAnalysis(t *testing.T) {
	for _, test := range []struct {
		name, provider string
		diagnostics    bool
	}{
		{"state", "package dep\nvar state=1\nfunc Stable() int {return state}\n", false},
		{"external", "package dep\nfunc Stable() int\n", false},
		{"incomplete", "package dep\nfunc Stable() int {return Missing}\n", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			data, root := suggestFixture(t, test.provider, "package consumer\nimport \"example.org/repo/dep\"\nvar Value=dep.Stable()\n")
			report, err := Suggest(context.Background(), data, "dep", root, "", TypeOptions{})
			if err != nil {
				t.Fatal(err)
			}
			for _, candidate := range report.Candidates {
				if candidate.Status == "requires validation" {
					t.Fatal("constraint ignored", candidate)
				}
			}
			if test.diagnostics && len(report.Diagnostics) == 0 {
				t.Fatal("incomplete analysis not reported")
			}
		})
	}
	data, root := suggestFixture(t, "package dep\nfunc Stable() int {return helper()}\nfunc helper() int {return 3}\n", "package consumer\nimport \"example.org/repo/dep\"\nvar Value=dep.Stable()\n")
	sourceFile(t, root, "dep/churn.go", "package dep\nfunc Churn() int {return helper()}\n")
	report, err := Suggest(context.Background(), data, "dep", root, "", TypeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Candidates[0].UnexportedBoundary) != 1 || report.Candidates[0].Status != "requires investigation" {
		t.Fatal("private incoming reference missed", report.Candidates)
	}
}

func TestAtomicGenericReceiverFamiliesAndBuildConstraints(t *testing.T) {
	data, root := suggestFixture(t, `//go:build chosen

// Code generated by test; DO NOT EDIT.
package dep
type Embed struct { N int }
type Box[T any] struct { Embed; Value T }
func (box Box[T]) Get() T { return box.Value }
func (box Box[T]) private() int { return box.N }
type Protocol interface { Call() }
`, "package consumer\nimport \"example.org/repo/dep\"\nvar Value=dep.Box[int]{}.Get()\n")
	data.Metadata.BuildFlags = []string{"-tags=chosen"}
	report, err := Suggest(context.Background(), data, "dep", root, "", TypeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, candidate := range report.Candidates {
		if slices.Contains(candidate.Declarations, "Box") {
			found = true
			for _, required := range []string{"Box.Get", "Box.private", "Box.Value", "Box.Embed", "Embed", "Embed.N"} {
				if !slices.Contains(candidate.Declarations, required) {
					t.Error("missing atomic type/method member", required, candidate.Declarations)
				}
			}
			if candidate.Status == "requires validation" {
				t.Fatal("generated/build variants not constrained")
			}
		}
	}
	if !found {
		t.Fatal("generic receiver group not found", report)
	}
}

func TestTransitiveExposureAndImportCycles(t *testing.T) {
	graph := map[string][]string{"a": {"b"}, "b": {"c"}}
	if !importReachable(graph, "a", "c") || importReachable(graph, "c", "a") {
		t.Fatal("reachability wrong")
	}
	graph["c"] = []string{"a"}
	if !importReachable(graph, "c", "b") {
		t.Fatal("cycle undetected")
	}
	data, root := suggestFixture(t, "package dep\nfunc Stable() int {return 7}\n", "package consumer\nimport \"example.org/repo/dep\"\nvar Value=dep.Stable()\n")
	for index := range data.Graph {
		if data.Graph[index].ImportPath == consumerName {
			data.Graph[index].Imports = append(data.Graph[index].Imports, "example.org/repo/alternate")
		}
	}
	data.Graph = append(data.Graph, history.PackageNode{ImportPath: "example.org/repo/alternate", Imports: []string{depName}})
	report, err := Suggest(context.Background(), data, "dep", root, "", TypeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Candidates[0].Status != "rejected" || !strings.Contains(strings.Join(report.Candidates[0].Reasons, " "), "transitively reachable") {
		t.Fatal("edge removal mistaken for isolation", report.Candidates)
	}
}

func TestDeclarationFingerprintAttribution(t *testing.T) {
	before, err := declarationFingerprints([]byte("package p\nconst(A=iota;B)\nfunc Stable() int{return 1}\nfunc Churn() int{return 1}\n"))
	if err != nil {
		t.Fatal(err)
	}
	after, err := declarationFingerprints([]byte("package p\nconst(A=iota;B;C)\nfunc Stable() int{return 1}\nfunc Churn() int{return 2}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if before["Stable"] != after["Stable"] || before["Churn"] == after["Churn"] || before["A"] == after["A"] {
		t.Fatal("declaration/const-block changes misattributed")
	}
	if _, err := declarationFingerprints([]byte("not Go")); err != nil {
		return
	}
	t.Fatal("invalid source silently attributed")
}
