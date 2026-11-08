package verify

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestOptionalPrometheusHistoricalSelection(t *testing.T) {
	root := os.Getenv("GOREBUILD_VERIFY_PROMETHEUS")
	if root == "" {
		t.Skip("set GOREBUILD_VERIFY_PROMETHEUS for read-only real Git/history selection")
	}
	runner := &engine{root: root, temp: t.TempDir()}
	report := &Report{Before: Revision{Commit: "1d4faeaf10281aa16c4a5e73e4bfda92f81bc366", PackageDirectory: "storage/remote"}, After: Revision{Commit: "180c9be6164b076ca31081a0f013b67bfa6f8c63", PackageDirectory: "storage/remote"}}
	if err := prepareScenarios(context.Background(), runner, report, Options{HistoryFile: filepath.Join(root, "prometheus-history.json")}); err != nil {
		t.Fatal(err)
	}
	if len(report.Scenarios) != 1 || report.Scenarios[0].Status != "prepared" {
		t.Fatal(report.ScenarioSelection, report.Scenarios)
	}
	t.Logf("selected historical commit %s, direction %s, files %v", report.Scenarios[0].Scenario.Commit, report.Scenarios[0].Scenario.Direction, report.Scenarios[0].Scenario.SelectedFiles)
}
