package history

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/konradreiche/gorebuild/internal/profile"
)

func TestMeasuredStatisticsAndNoDoubleCounting(t *testing.T) {
	values := []int64{40, 10, 30, 20}
	if *median(values) != 25 || *p90(values) != 40 || median(nil) != nil || p90(nil) != nil || !reflect.DeepEqual(values, []int64{40, 10, 30, 20}) {
		t.Fatal("bad median/P90 or input was mutated")
	}
	if *median([]int64{0}) != 0 || *p90([]int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}) != 9 {
		t.Fatal("zero samples or nearest-rank P90 misrepresented")
	}
	measured := func(wall int64) *ReplayResult {
		return &ReplayResult{Status: "measured", Rebuild: &profile.BuildCapture{Run: profile.BuildRun{WallTimeNS: wall}}}
	}
	report := Report{
		Replay: &ReplaySummary{Selected: 4},
		Events: []Event{
			{PackageDirs: []string{"a", "b"}, BuildPackageDirs: []string{"a", "b"}, Replay: measured(10)},
			{PackageDirs: []string{"a"}, BuildPackageDirs: []string{"a"}, Replay: measured(30)},
			{PackageDirs: []string{"b", "c"}, BuildPackageDirs: []string{"b", "c"}, Replay: &ReplayResult{Status: "failed"}},
			{PackageDirs: []string{"c"}, BuildPackageDirs: []string{"c"}, Replay: &ReplayResult{Status: "excluded"}},
		},
		Packages: []PackageImpact{{Dir: "a", Changes: 2, BuildChanges: 2}, {Dir: "b", Changes: 2, BuildChanges: 2}, {Dir: "c", Changes: 2, BuildChanges: 2}},
	}
	summarizeMeasurements(&report)
	if report.Replay.TotalMeasuredWallTimeNS != 40 || report.Replay.Measured != 2 || report.Replay.Failed != 1 || report.Replay.Excluded != 1 {
		t.Fatal("event total double-counted or failed/excluded samples counted", report.Replay)
	}
	for _, pkg := range report.Packages {
		switch pkg.Dir {
		case "a":
			if pkg.Measured.Samples != 2 || *pkg.Measured.MedianWallTimeNS != 20 || pkg.Measured.P90WallTimeNS != nil || *pkg.Measured.MinWallTimeNS != 10 || *pkg.Measured.MaxWallTimeNS != 30 {
				t.Fatal("a stats incorrect", pkg.Measured)
			}
		case "b":
			if pkg.Measured.Samples != 1 || pkg.Measured.MedianWallTimeNS != nil {
				t.Fatal("b stats incorrect", pkg.Measured)
			}
		case "c":
			if pkg.Measured.Samples != 0 || pkg.Measured.MedianWallTimeNS != nil {
				t.Fatal("failures turned into zero duration", pkg.Measured)
			}
		}
	}
}

func TestDistributionUsesBuildEventCohorts(t *testing.T) {
	report := Report{Replay: &ReplaySummary{}, Packages: []PackageImpact{{Dir: "a", Changes: 11, BuildChanges: 10}}}
	for index := range 10 {
		report.Events = append(report.Events, Event{PackageDirs: []string{"a"}, BuildPackageDirs: []string{"a"}, Replay: &ReplayResult{
			Status: "measured", Rebuild: &profile.BuildCapture{Run: profile.BuildRun{WallTimeNS: int64((index + 1) * 10)}},
		}})
	}
	// A test-only/no-op change is retained as event evidence, but must not
	// dilute the package's build-event latency distribution.
	report.Events = append(report.Events, Event{PackageDirs: []string{"a"}, Replay: &ReplayResult{Status: "measured", Rebuild: &profile.BuildCapture{Run: profile.BuildRun{WallTimeNS: 1}}}})
	summarizeMeasurements(&report)
	stats := report.Packages[0].Measured
	if stats.Selected != 10 || stats.Samples != 10 || *stats.MedianWallTimeNS != 55 || *stats.P90WallTimeNS != 90 || *stats.MinWallTimeNS != 10 || *stats.MaxWallTimeNS != 100 || report.Replay.TotalMeasuredWallTimeNS != 551 {
		t.Fatal("distribution/cohort or event total incorrect", stats, report.Replay)
	}
	summarizeMeasurements(&report)
	if report.Replay.TotalMeasuredWallTimeNS != 551 {
		t.Fatal("deriving statistics twice double-counted evidence")
	}
}

func TestUnsupportedReplayConfigurationIsExplicit(t *testing.T) {
	root := t.TempDir()
	for _, test := range []struct {
		workspace string
		flags     string
		want      string
	}{
		{filepath.Join(root, "go.work"), "", "workspace"},
		{"off", "-overlay=outside.json", "overlay"},
	} {
		report := Report{
			Metadata: Metadata{Repository: root, ImportPath: "example.org/app", GOWORK: test.workspace, GOFLAGS: test.flags},
			Graph:    []PackageNode{{ImportPath: "example.org/app", Dir: root}},
			Events:   []Event{{Commit: "sample", PackageDirs: []string{"."}}},
			Packages: []PackageImpact{{Dir: ".", Changes: 1}},
		}
		replayHistory(context.Background(), &report, Options{Replay: 1}, root, "")
		result := report.Events[0].Replay
		if result.Status != "excluded" || result.Stage != "configuration" || !strings.Contains(result.Reason, test.want) || result.Baseline != nil || report.Packages[0].Measured.MedianWallTimeNS != nil {
			t.Fatalf("unsupported configuration not explicitly excluded: %+v", result)
		}
	}
}

func TestCancelledSamplesRemainVisible(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	report := Report{Events: []Event{{Commit: "a", PackageDirs: []string{"leaf"}}, {Commit: "b", PackageDirs: []string{"leaf"}}}, Packages: []PackageImpact{{Dir: "leaf", Changes: 2}}}
	replayHistory(ctx, &report, Options{Replay: 2}, ".", "")
	if report.Replay.Excluded != 2 || report.Replay.Measured != 0 {
		t.Fatal("cancelled samples missing", report.Replay)
	}
	for _, event := range report.Events {
		if event.Replay.Stage != "cancelled" {
			t.Fatal("cancelled sample not explicit", event.Replay)
		}
	}
}
