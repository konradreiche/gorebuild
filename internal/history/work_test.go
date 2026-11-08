package history

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/konradreiche/gorebuild/internal/profile"
)

func TestExecutedWorkSummaryIsNotChangedPackageLatency(t *testing.T) {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	action := func(id int, mode, pkg string, elapsed, tool int, executed bool, deps ...int) profile.Action {
		result := profile.Action{ID: id, Mode: mode, Package: pkg, Deps: deps, NeedBuild: true, TimeStart: start, TimeDone: start.Add(time.Duration(elapsed) * time.Second), CmdReal: int64(time.Duration(tool) * time.Second)}
		if executed {
			command := "/tool/compile -p " + pkg
			if mode == "link" {
				command = "/tool/link -o app"
			}
			result.Cmd = []string{command}
		}
		return result
	}
	graph, err := profile.Analyze([]profile.Action{
		action(0, "link", "app", 1, 1, true, 1, 3, 4), action(1, "build", "a", 2, 1, true, 2),
		action(2, "build", "leaf", 3, 2, true), action(3, "build", "b", 4, 3, true), action(4, "build", "cached", 90, 0, false),
	})
	if err != nil {
		t.Fatal(err)
	}
	replay := func(wall, warm time.Duration) *ReplayResult {
		return &ReplayResult{Status: "measured", Rebuild: &profile.BuildCapture{Run: profile.BuildRun{WallTimeNS: int64(wall), Graph: graph}}, Warm: &profile.BuildCapture{Run: profile.BuildRun{WallTimeNS: int64(warm)}}, PackageImports: map[string]string{"a": "a", "b": "b"}}
	}
	report := Report{
		Replay: &ReplaySummary{}, Events: []Event{
			{Commit: "first", PackageDirs: []string{"a", "b"}, BuildPackageDirs: []string{"a", "b"}, Replay: replay(10*time.Second, 500*time.Millisecond)},
			{Commit: "second", PackageDirs: []string{"a"}, BuildPackageDirs: []string{"a"}, Replay: replay(12*time.Second, 700*time.Millisecond)},
		}, Packages: []PackageImpact{{Dir: "a", Changes: 2, BuildChanges: 2}, {Dir: "b", Changes: 1, BuildChanges: 1}},
	}
	summarizeMeasurements(&report)
	work := report.Replay.Work
	if work.Samples != 2 || *work.MedianWallTimeNS != int64(11*time.Second) || *work.MedianWarmWallTimeNS != int64(600*time.Millisecond) || *work.MedianCompilationToolNS != int64(6*time.Second) || *work.MedianLinkToolNS != int64(time.Second) || *work.MedianCriticalPathNS != int64(6*time.Second) || work.TotalBuildActions != 6 || work.TotalLinkActions != 2 {
		t.Fatalf("work, path, wall or warm baseline conflated: %+v", work)
	}
	for _, pkg := range work.RebuiltPackages {
		if pkg.Package == "cached" || pkg.Replays != 2 || pkg.RecompiledReplays != 2 {
			t.Fatal("cache checks counted or rebuilt event counts double-counted", pkg)
		}
		if pkg.Package == "a" && (*pkg.MedianToolTimeNS != int64(time.Second) || *pkg.MedianActionElapsedNS != int64(2*time.Second)) {
			t.Fatal("individual action duration conflated with event wall time", pkg)
		}
	}
	if report.Replay.TotalMeasuredWallTimeNS != int64(22*time.Second) || report.Packages[1].Measured.MedianWallTimeNS != nil {
		t.Fatal("overlapping cohorts double-counted or sparse median invented")
	}
	out := FormatText(report, 10)
	for _, token := range []string{"Median event wall", "warmed no-op baseline", "Most frequently rebuilt packages", "Most expensive replayed events", "Median action work"} {
		if !strings.Contains(out, token) {
			t.Errorf("missing %q", token)
		}
	}
	report.FocusPackage = "a"
	out = FormatText(report, 10)
	for _, token := range []string{"Critical path (estimated; executed action elapsed time)", "Warm no-op baseline:", "Other executed actions", "recorded tool work"} {
		if !strings.Contains(out, token) {
			t.Errorf("missing replay evidence %q", token)
		}
	}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var saved Report
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Replay.Work.TotalBuildActions != 6 || saved.Events[0].Replay.Rebuild.Run.Graph.ExecutedActionChainNS != int64(6*time.Second) || len(saved.Events[0].Replay.Rebuild.Run.Graph.ExecutedActionDetails) != 4 {
		t.Fatal("event/aggregate JSON evidence lost")
	}
}
