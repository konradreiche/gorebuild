package profile

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestCriticalPathViewsAndJSON(t *testing.T) {
	start := time.Now()
	action := func(id int, pkg string, seconds int, deps ...int) Action {
		return Action{ID: id, Mode: "build", Package: pkg, Deps: deps, Cmd: []string{"compile"}, TimeStart: start, TimeDone: start.Add(time.Duration(seconds) * time.Second)}
	}
	actions := []Action{
		{ID: 0, Mode: "root", Deps: []int{1, 3}},
		action(1, "on-path", 8, 2), action(2, "leaf", 1),
		action(3, "off-path", 7),
	}
	graph, err := Analyze(actions)
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.LongestActionChain) != 3 || graph.LongestActionChain[2].ID != 0 {
		t.Fatalf("full chain missing synthetic root: %+v", graph.LongestActionChain)
	}
	report := Report{Cold: BuildRun{Graph: graph}}
	ranked := FormatText(report, 1)
	path, other, ok := strings.Cut(ranked, "Other expensive build actions")
	if !ok || !strings.Contains(path, "on-path") || strings.Contains(path, "off-path") || !strings.Contains(other, "off-path") || strings.Contains(other, "on-path") {
		t.Fatalf("incorrect path separation:\n%s", ranked)
	}
	if !strings.Contains(path, "88.9%") {
		t.Fatalf("share should use full 9s chain, not displayed 8s: %s", path)
	}
	ordered := FormatText(report, 10, true)
	if strings.Index(ordered, "leaf") > strings.Index(ordered, "on-path") {
		t.Fatal("not dependency ordered")
	}
	if strings.Contains(ranked, "warm critical") || strings.Contains(ranked, "warm 0.") {
		t.Fatal("unexpected warm critical path")
	}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Report
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Cold.Graph.ActionGraph) != 4 || len(decoded.Cold.Graph.ActionGraph[0].Deps) != 2 || len(decoded.Cold.Graph.LongestActionChain) != 3 {
		t.Fatal("JSON lost actions, dependencies, or chain steps")
	}
	if graph.LongestActionChain[0].Package != "leaf" {
		t.Fatal("formatting mutated chain")
	}
}

func TestShortPackageLabels(t *testing.T) {
	packages := []string{
		"github.com/aws/aws-sdk-go-v2/service/ec2",
		"github.com/one/shared/pkg", "github.com/two/shared/pkg",
		"net/http", "google.golang.org/api/compute/v1",
		"github.com/one/net/http",
	}
	graph := GraphSummary{}
	for _, pkg := range packages {
		graph.ActionGraph = append(graph.ActionGraph, Action{Package: pkg})
	}
	labels := shortPackageLabels(graph)
	for _, pkg := range packages {
		want := pkg
		if pkg == packages[0] {
			want = "aws-sdk-go-v2/service/ec2"
		}
		if labels[pkg] != want {
			t.Errorf("label for %s = %s, want %s", pkg, labels[pkg], want)
		}
	}
	graph.LongestActionChain = []ChainStep{{ID: 1, Mode: "build", Package: packages[0], ElapsedNS: 1}}
	out := FormatText(Report{Cold: BuildRun{Graph: graph}}, 10)
	if strings.Contains(out, "NaN") || strings.Contains(out, "+Inf") || !strings.Contains(out, "0.0%") {
		t.Fatalf("zero chain denominator not handled: %s", out)
	}
	if strings.Contains(out, packages[0]) || !strings.Contains(out, "aws-sdk-go-v2/service/ec2") {
		t.Fatal("terminal path not shortened")
	}
	data, err := json.Marshal(graph)
	if err != nil || !strings.Contains(string(data), packages[0]) {
		t.Fatal("full JSON import path lost", err)
	}
}

func TestCompactOffPathActions(t *testing.T) {
	graph := GraphSummary{}
	for id := range 8 {
		graph.ActionGraph = append(graph.ActionGraph, Action{ID: id, Mode: "build", Package: "executed", Cmd: []string{"compile"}, CmdReal: int64(time.Second)})
	}
	graph.ActionGraph = append(
		graph.ActionGraph,
		Action{ID: 10, Mode: "build", Package: "cache-check", NeedBuild: true},
		Action{ID: 11, Mode: "copy", Package: "not-build-link", Cmd: []string{"copy"}, CmdReal: int64(time.Hour)},
	)
	out := FormatText(Report{Cold: BuildRun{Graph: graph}}, 10)
	_, other, _ := strings.Cut(out, "Other expensive build actions (not on critical path):")
	if strings.Count(other, "executed\n") != 5 || !strings.Contains(other, "  ...\n") || strings.Contains(other, "more actions") || strings.Contains(other, "cache-check") || strings.Contains(other, "not-build-link") {
		t.Fatalf("off-path list not compact or includes non-build work: %s", other)
	}
}
