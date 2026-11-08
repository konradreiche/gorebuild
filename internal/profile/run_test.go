package profile

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunColdAndWarmOnRealGoModule(t *testing.T) {
	// The fixture only uses Go's standard library. This is a real end-to-end
	// test of -debug-actiongraph, not a mocked JSON fixture.
	t.Setenv("GOFLAGS", "")
	work := t.TempDir()
	for path, body := range map[string]string{
		"go.mod":                "module example.org/gorebuild-test\n\ngo 1.23\n",
		"cmd/server/main.go":    "package main\nimport \"example.org/gorebuild-test/internal/leaf\"\nfunc main(){ _ = leaf.Message() }\n",
		"internal/leaf/leaf.go": "package leaf\nfunc Message() string { return \"ok\" }\n",
	} {
		file := filepath.Join(work, path)
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(work); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	artifacts := filepath.Join(t.TempDir(), "captures", "prometheus.profile.json")
	report, err := Run(ctx, Options{Target: "./cmd/server", OutputFile: artifacts})
	if err != nil {
		t.Fatal(err)
	}
	if report.Metadata.ImportPath != "example.org/gorebuild-test/cmd/server" {
		t.Fatalf("wrong target package: %s", report.Metadata.ImportPath)
	}
	if report.Cold.Graph.ExecutedBuildActions < 2 || report.Cold.Graph.ExecutedLinkActions < 1 {
		t.Errorf("cold build unexpectedly did little work: %+v", report.Cold.Graph)
	}
	if report.WarmNoop.Graph.ExecutedBuildActions != 0 || report.WarmNoop.Graph.ExecutedLinkActions != 0 {
		t.Errorf("warm no-op unexpectedly compiled/linked: %+v", report.WarmNoop.Graph)
	}
	if len(report.LocalPackages) != 2 {
		t.Errorf("local packages = %v, want two", report.LocalPackages)
	}
	data, err := os.ReadFile(artifacts)
	if err != nil {
		t.Fatal(err)
	}
	var saved ProfileFile
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatalf("invalid saved profile JSON: %v", err)
	}
	if saved.FormatVersion != 1 || saved.Report.SchemaVersion != 1 {
		t.Errorf("unexpected schema versions: capture=%d report=%d", saved.FormatVersion, saved.Report.SchemaVersion)
	}
	if saved.Report.Metadata.ImportPath != report.Metadata.ImportPath || saved.Report.Cold.WallTimeNS != report.Cold.WallTimeNS {
		t.Error("saved report does not match Run result")
	}
	for name, raw := range map[string]json.RawMessage{"cold": saved.ActionGraphs.Cold, "warm_noop": saved.ActionGraphs.WarmNoop} {
		var actions []Action
		if err := json.Unmarshal(raw, &actions); err != nil || len(actions) == 0 {
			t.Errorf("%s raw graph is missing or invalid: %v", name, err)
		}
	}
	entries, err := os.ReadDir(filepath.Dir(artifacts))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(artifacts) {
		t.Errorf("expected exactly one capture file, found %v", entries)
	}

	if _, err := os.Stat(filepath.Join(work, "cmd/server/build-output")); !os.IsNotExist(err) {
		t.Errorf("build unexpectedly left output in source tree: %v", err)
	}
	_, err = Run(ctx, Options{Target: "./cmd/server", OutputFile: artifacts})
	if err != nil && strings.Contains(err.Error(), "already exists") {
		return
	}
	t.Errorf("existing output should not be overwritten; got %v", err)
}
