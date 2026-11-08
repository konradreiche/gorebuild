package verify

import (
	"strings"
	"testing"

	"github.com/konradreiche/gorebuild/internal/profile"
)

func TestColdOnlySummaryInterpretation(t *testing.T) {
	observation := func(wall int64) *Measurement {
		return &Measurement{Status: "measured", Cold: &profile.BuildCapture{Run: profile.BuildRun{WallTimeNS: wall}}}
	}
	result := summarize([]Pair{{Before: observation(100), After: observation(50)}, {Before: observation(100), After: observation(60)}})
	if result.IncrementalWall.PairedDelta.Count != 0 || !strings.Contains(result.Interpretation, "lower wall time") || strings.Contains(result.Interpretation, "span zero") {
		t.Fatal("cold-only interpretation used absent incremental observations", result)
	}
}

func TestPairedStatisticsUseIndividualDeltasAndVariability(t *testing.T) {
	observation := func(wall int64) *Measurement {
		return &Measurement{Status: "measured", Cold: &profile.BuildCapture{}, Incremental: &profile.BuildCapture{Run: profile.BuildRun{WallTimeNS: wall}}}
	}
	pairs := []Pair{{Before: observation(1), After: observation(100)}, {Before: observation(10), After: observation(2)}, {Before: observation(100), After: observation(101)}}
	summary := summarize(pairs)
	if summary.IncrementalWall.PairedDelta.MedianNS != 1 || summary.IncrementalWall.After.MedianNS-summary.IncrementalWall.Before.MedianNS == 1 {
		t.Fatal("paired deltas confused with aggregate medians", summary)
	}
	if !strings.Contains(summary.Interpretation, "span zero") {
		t.Fatal("variation overclaimed", summary.Interpretation)
	}
	single := distribution([]int64{3})
	if single.MedianAvailable || single.Count != 1 {
		t.Fatal("sparse median invented")
	}
	pairs = append(pairs, Pair{Before: &Measurement{Status: "failed"}, After: observation(1)})
	if summarize(pairs).ValidPairs != 3 {
		t.Fatal("failed observations became zero-cost data")
	}
	if !strings.Contains(Format(&Report{Summary: summary}), "withheld") {
		t.Fatal("invalid report presented valid performance")
	}
}
