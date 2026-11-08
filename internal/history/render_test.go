package history

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/konradreiche/gorebuild/internal/presentation"
)

func TestHistoryCoverageAndEventVersusPackageCosts(t *testing.T) {
	wall := int64(19000000000)
	tool := int64(3000000000)
	report := Report{Metadata: Metadata{ImportPath: "example/main", SinceUTC: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), CapturedUTC: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC), CoverageComplete: true}, Packages: []PackageImpact{{Dir: "api", ImportPath: "example/api", Changes: 20, BuildChanges: 15}}, Replay: &ReplaySummary{Selected: 20, Measured: 18, Excluded: 2, Work: &ReplayWorkSummary{Samples: 18, MedianWallTimeNS: &wall, RebuiltPackages: []RebuiltPackage{{Package: "example/api", RecompiledReplays: 15, MedianToolTimeNS: &tool}}}}}
	before, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, width := range []int{80, 120} {
		text := Render(report, presentation.Options{Width: width})
		for _, want := range []string{"Complete (requested available first-parent period)", "20 selected; 18 successfully replayed", "15/18", "3.000s", "19.000s", "whole-event", "cohorts overlap"} {
			if !strings.Contains(text, want) {
				t.Fatal(want, text)
			}
		}
		if strings.Contains(text, "Pareto") || strings.Contains(text, "impact score") {
			t.Fatal("opaque prioritization leaked")
		}
		for _, line := range strings.Split(text, "\n") {
			if utf8.RuneCountInString(line) > width {
				t.Fatal("line too long", width, line)
			}
		}
		if !strings.Contains(Render(report, presentation.Options{Width: width, Details: true}), "Supporting evidence and diagnostics") {
			t.Fatal("details missing")
		}
	}
	after, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("render mutated history")
	}
	report.Metadata.Shallow = true
	report.Metadata.Truncated = true
	report.Metadata.MaxCommits = 5
	report.Replay = nil
	text := Render(report, presentation.Options{})
	for _, want := range []string{"Partial", "shallow repository", "truncated", "No measured rebuild costs"} {
		if !strings.Contains(text, want) {
			t.Fatal(want, text)
		}
	}
	if strings.Contains(text, "Complete (") {
		t.Fatal("incomplete capture called complete")
	}
}
