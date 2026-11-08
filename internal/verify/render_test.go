package verify

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/konradreiche/gorebuild/internal/presentation"
	"github.com/konradreiche/gorebuild/internal/profile"
)

// Reference values from the completed ten-pair Prometheus artifact. The
// implementation-only edit never caused API compilation in either revision.
func prometheusPresentationFixture() *Report {
	const root = "github.com/prometheus/prometheus/"
	api := root + "web/api/v1"
	report := &Report{Target: "./cmd/prometheus", PerformanceValid: true, Before: Revision{ChangedPackage: root + "storage/remote", PackageDirectory: "storage/remote"}, DependencyChanges: GraphComparison{Reachability: []Reach{{Package: api, Before: true, After: false}}}}
	for _, revision := range []*Revision{&report.Before, &report.After} {
		revision.Correct = true
		revision.Build = &profile.BuildCapture{}
		revision.Tests = &CommandCheck{Arguments: []string{"test", "-race"}}
		revision.Runtime = &CommandCheck{}
	}
	for _, value := range []struct {
		kind     string
		delta    int64
		compiled int
	}{{"implementation", 392869560, 0}, {"exported", -963465806, 10}, {"historical", -2061084322, 10}} {
		scenario := ScenarioResult{Scenario: Scenario{Name: value.kind, Kind: value.kind}, Status: "measured", Structure: []StructuralCount{{Package: api, BeforeCompiled: value.compiled, Pairs: 10}}, Noise: NoiseConclusion{Confidence: "Inconclusive", Reason: "paired differences span zero"}}
		if value.kind == "historical" {
			scenario.Scenario.Direction = "reverse"
		}
		scenario.Summary.IncrementalWall.PairedDelta = Distribution{Count: 10, MedianAvailable: true, MedianNS: value.delta, MinNS: -12000000000, MaxNS: 23000000000}
		report.Scenarios = append(report.Scenarios, scenario)
	}
	report.ColdSummary.ColdWall.PairedDelta = Distribution{Count: 3, MedianAvailable: true, MedianNS: 12104000000, MinNS: -756000000, MaxNS: 30825000000}
	return report
}
func TestVerifyConclusionsFirstWithoutChangingEvidence(t *testing.T) {
	report := prometheusPresentationFixture()
	before, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, width := range []int{80, 120} {
		text := Render(report, presentation.Options{Width: width})
		for _, want := range []string{"gorebuild verify: storage/remote", "Structural result:", "20/20 applicable runs", "Implementation-only", "+0.393s", "-0.963s", "-2.061s", "+12.104s", "Inconclusive", "Tests and race checks", "Cache isolation verified", "improvement not established", "Do not adopt solely"} {
			if !strings.Contains(text, want) {
				t.Fatalf("missing %q:\n%s", want, text)
			}
		}
		if strings.Contains(text, "30/30") || strings.Contains(text, "Compilation work (not savings)") {
			t.Fatal("default overclaims avoidance or dumps details", text)
		}
		for _, line := range strings.Split(text, "\n") {
			if utf8.RuneCountInString(line) > width {
				t.Fatal("line too long", width, line)
			}
		}
		detailed := Render(report, presentation.Options{Width: width, Details: true})
		if !strings.Contains(detailed, "Supporting evidence and limitations") || !strings.Contains(detailed, "Compilation work (not savings)") {
			t.Fatal("details missing", detailed)
		}
	}
	after, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("rendering mutated analysis")
	}
}
func TestVerifyMixedCountsAndExistingConclusions(t *testing.T) {
	report := prometheusPresentationFixture()
	report.Scenarios[1].Structure[0].BeforeCompiled = 5
	report.Scenarios[1].Structure[0].AfterCompiled = 2
	text := Render(report, presentation.Options{})
	if !strings.Contains(text, "Mixed observations") || strings.Contains(text, "20/20 applicable runs") {
		t.Fatal("mixed counts converted to universal avoidance", text)
	}
	report.Scenarios[0].Noise = NoiseConclusion{Confidence: "Consistent (observed only)", Direction: "after faster"}
	text = Render(report, presentation.Options{})
	if !strings.Contains(text, "Consistent (observed only)") || !strings.Contains(text, "not statistical significance") {
		t.Fatal("existing conclusion hidden or overstated", text)
	}
}

func TestVerifySparseFailedAndSignedResults(t *testing.T) {
	report := &Report{Errors: []string{"correctness failure"}, Before: Revision{Build: &profile.BuildCapture{Failure: "build failed"}}}
	text := Render(report, presentation.Options{})
	for _, want := range []string{"Unavailable (0)", "Not checked", "Failed", "Withheld", "correctness failure"} {
		if !strings.Contains(text, want) {
			t.Fatal("missing sparse/failure status", want, text)
		}
	}
	if strings.Contains(text, "Cache isolation verified") {
		t.Fatal("invalid result overclaimed")
	}
	report = prometheusPresentationFixture()
	report.Scenarios[0].Correctness = []ScenarioCheck{{Failure: "edited test failed"}}
	text = Render(report, presentation.Options{})
	if !strings.Contains(text, "Failed") {
		t.Fatal("edited correctness failure hidden", text)
	}
}
