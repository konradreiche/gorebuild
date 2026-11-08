package verify

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/konradreiche/gorebuild/internal/explain"
	"github.com/konradreiche/gorebuild/internal/history"
	"github.com/konradreiche/gorebuild/internal/presentation"
	"github.com/konradreiche/gorebuild/internal/profile"
)

// Skip raw captures while decoding the frozen real benchmark. No target code,
// Git commands, source inspection, cache replay, or new measurements are run.
func TestOptionalPrometheusTerminalReports(t *testing.T) {
	root := os.Getenv("GOREBUILD_REPORTING_PROMETHEUS")
	if root == "" {
		t.Skip("set GOREBUILD_REPORTING_PROMETHEUS to validate existing real evidence artifacts offline")
	}
	type revision struct {
		PackageDirectory string        `json:"controlled_package_directory"`
		ChangedPackage   string        `json:"controlled_package"`
		Correct          bool          `json:"correctness_checks_passed"`
		Tests            *CommandCheck `json:"correctness_tests"`
		Runtime          *CommandCheck `json:"runtime_check"`
		Build            *struct {
			Failure string `json:"failure"`
		} `json:"correctness_build"`
	}
	type scenario struct {
		Scenario  Scenario          `json:"scenario"`
		Status    string            `json:"status"`
		Summary   Summary           `json:"summary"`
		Noise     NoiseConclusion   `json:"timing_conclusion"`
		Structure []StructuralCount `json:"structural_observations"`
	}
	var projection struct {
		Target      string          `json:"target"`
		Before      revision        `json:"before"`
		After       revision        `json:"after"`
		Changes     GraphComparison `json:"dependency_changes"`
		Scenarios   []scenario      `json:"scenarios"`
		ColdSummary Summary         `json:"cold_summary"`
		Valid       bool            `json:"performance_comparison_valid"`
		Errors      []string        `json:"errors"`
	}
	decodeExisting(t, filepath.Join(root, "prometheus-verification-expanded.json"), &projection)
	report := &Report{Target: projection.Target, DependencyChanges: projection.Changes, ColdSummary: projection.ColdSummary, PerformanceValid: projection.Valid, Errors: projection.Errors}
	convert := func(value revision) Revision {
		result := Revision{ChangedPackage: value.ChangedPackage, PackageDirectory: value.PackageDirectory, Correct: value.Correct, Tests: value.Tests, Runtime: value.Runtime}
		if value.Build != nil {
			result.Build = &profile.BuildCapture{Failure: value.Build.Failure}
		}
		return result
	}
	report.Before = convert(projection.Before)
	report.After = convert(projection.After)
	for _, value := range projection.Scenarios {
		report.Scenarios = append(report.Scenarios, ScenarioResult{Scenario: value.Scenario, Status: value.Status, Summary: value.Summary, Noise: value.Noise, Structure: value.Structure})
	}
	data, err := explain.Read(filepath.Join(root, "prometheus-history.json"))
	if err != nil {
		t.Fatal(err)
	}
	var suggestions explain.Suggestions
	decodeExisting(t, filepath.Join(root, "prometheus-suggestions-cache.json"), &suggestions)
	for _, width := range []int{80, 120} {
		text := Render(report, presentation.Options{Width: width})
		for _, want := range []string{"20/20 applicable runs", "+0.393s", "-0.963s", "-2.061s", "+12.104s"} {
			if !strings.Contains(text, want) {
				t.Fatal("real verify finding missing", want, text)
			}
		}
		if strings.Count(text, "Inconclusive") != 4 {
			t.Fatal("real uncertainty changed", text)
		}
		t.Logf("Real verify (%d columns):\n%s", width, text)
		summary := suggestions.Render(data, presentation.Options{Width: width})
		if !strings.Contains(summary, "Outgoing groups: 12") || !strings.Contains(summary, "Structurally supported candidate") {
			t.Fatal("real suggestion missing", summary)
		}
		t.Logf("Real suggestions (%d columns):\n%s", width, summary)
		explanation, err := explain.Render(data, explain.Options{Commit: "503180645ef0", Top: 3}, nil, nil, presentation.Options{Width: width, Top: 3})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(explanation, "19.263s") {
			t.Fatal("real observed duration changed", explanation)
		}
		t.Logf("Real explain (%d columns):\n%s", width, explanation)
	}
	// Decode history summaries without retaining raw action graphs/cold logs.
	var saved struct {
		Metadata history.Metadata        `json:"metadata"`
		Packages []history.PackageImpact `json:"packages"`
		Replay   *history.ReplaySummary  `json:"replay"`
		Warnings []string                `json:"warnings"`
	}
	decodeExisting(t, filepath.Join(root, "prometheus-history.json"), &saved)
	events := make([]history.Event, len(data.Events))
	for index, event := range data.Events {
		events[index].Commit = event.Commit
		if event.Replay != nil {
			events[index].Replay = &history.ReplayResult{Status: event.Replay.Status, Reason: event.Replay.Reason}
		}
	}
	for _, width := range []int{80, 120} {
		text := history.Render(history.Report{Metadata: saved.Metadata, Packages: saved.Packages, Replay: saved.Replay, Warnings: saved.Warnings, Events: events}, presentation.Options{Width: width, Top: 3})
		if !strings.Contains(text, "477 analyzed; 20 selected; 18 successfully replayed") {
			t.Fatal("real coverage changed", text)
		}
		t.Logf("Real history (%d columns):\n%s", width, text)
	}
}
func decodeExisting(t *testing.T, path string, value any) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := json.NewDecoder(file).Decode(value); err != nil {
		t.Fatal(err)
	}
}
