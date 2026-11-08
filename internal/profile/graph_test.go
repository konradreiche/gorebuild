package profile

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestAnalyzeExecutesCommandsNotNeedBuild(t *testing.T) {
	start := time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC)
	timed := func(id int, mode, pkg string, ns int64, deps ...int) Action {
		return Action{
			ID: id, Mode: mode, Package: pkg, Deps: deps,
			TimeStart: start, TimeDone: start.Add(time.Duration(ns)),
		}
	}
	root := timed(0, "link", "example.org/app", 2_000_000_000, 1, 2)
	root.Cmd = []string{"link ..."}
	root.CmdReal = 1_500_000_000
	one := timed(1, "build", "example.org/a", 4_000_000_000, 3)
	one.Cmd = []string{"compile ..."}
	one.CmdReal = 3_000_000_000
	two := timed(2, "build", "example.org/b", 7_000_000_000)
	two.NeedBuild = true // Warmed but no command ran.
	leaf := timed(3, "build", "example.org/leaf", 5_000_000_000)
	leaf.Cmd = []string{"compile ..."}
	leaf.CmdReal = 5_000_000_000

	got, err := Analyze([]Action{root, one, two, leaf})
	if err != nil {
		t.Fatal(err)
	}
	if got.ExecutedActions != 3 || got.ExecutedBuildActions != 2 || got.ExecutedLinkActions != 1 {
		t.Errorf("executed actions = %d / %d / %d", got.ExecutedActions, got.ExecutedBuildActions, got.ExecutedLinkActions)
	}
	if got.BuildToolTimeNS != 8_000_000_000 || got.ToolTimeNS != 9_500_000_000 {
		t.Errorf("incorrect command time totals: %+v", got)
	}
	if got.LongestActionChainNS != 11_000_000_000 {
		t.Errorf("longest dependency chain = %d, want 11s", got.LongestActionChainNS)
	}
	var packages []string
	for _, p := range got.PackageBuildToolTimes {
		packages = append(packages, p.Package)
	}
	if !reflect.DeepEqual(packages, []string{"example.org/leaf", "example.org/a"}) {
		t.Errorf("package ranking = %v", packages)
	}
	var path []string
	for _, s := range got.LongestActionChain {
		path = append(path, s.Package)
	}
	if !reflect.DeepEqual(path, []string{"example.org/leaf", "example.org/a", "example.org/app"}) {
		t.Errorf("longest chain = %v", path)
	}
}

func TestAnalyzeRejectsBadGraph(t *testing.T) {
	for _, test := range []struct {
		name    string
		actions []Action
		want    string
	}{
		{"duplicate", []Action{{ID: 1}, {ID: 1}}, "duplicate"},
		{"missing dependency", []Action{{ID: 1, Deps: []int{9}}}, "unknown action"},
		{"cycle", []Action{{ID: 1, Deps: []int{2}}, {ID: 2, Deps: []int{1}}}, "cycle"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := Analyze(test.actions)
			if err != nil && strings.Contains(err.Error(), test.want) {
				return
			}
			t.Errorf("Analyze() error = %v, want %q", err, test.want)
		})
	}
}

func TestToolDurationFallback(t *testing.T) {
	start := time.Now()
	action := Action{NeedBuild: true, TimeStart: start, TimeDone: start.Add(3 * time.Second)}
	if action.ToolDuration() != 0 {
		t.Error("NeedBuild is not proof of execution")
	}
	action.Cmd = []string{"compile"}
	if action.ToolDuration() != 3*time.Second {
		t.Errorf("CmdReal fallback = %s", action.ToolDuration())
	}
	action.CmdReal = int64(time.Second)
	if action.ToolDuration() != time.Second {
		t.Errorf("CmdReal override = %s", action.ToolDuration())
	}
}

func TestFormatTextReportsScope(t *testing.T) {
	report := Report{
		Metadata:      Metadata{ImportPath: "example.org/app", GoVersion: "go1.23.2", GOOS: "linux", GOARCH: "amd64"},
		LocalPackages: []string{"example.org/local"},
		Cold: BuildRun{Graph: GraphSummary{LongestActionChain: []ChainStep{
			{ID: 1, Mode: "build", Package: "runtime", ElapsedNS: 10_000_000_000},
			{ID: 2, Mode: "build", Package: "example.org/local", ElapsedNS: 1_000_000_000},
		}}},
	}
	out := FormatText(report, 10)
	for _, token := range []string{"Cold", "Warm (no-op)", "example.org/local", "runtime", "not an exact breakdown", "does not estimate"} {
		if !strings.Contains(out, token) {
			t.Errorf("missing %q from report", token)
		}
	}
}
