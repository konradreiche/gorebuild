package history

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestShallowHistoryIsIncomplete(t *testing.T) {
	t.Setenv("GOFLAGS", "")
	t.Setenv("GOWORK", "off")
	original := testRepository(t)
	testFile(t, original, "leaf/lib.go", "package leaf\nfunc F(){println(1)}\n")
	head := commitFiles(t, original, "change source")
	clone := filepath.Join(t.TempDir(), "clone")
	testGit(t, original, "clone", "--depth=1", "file://"+original, clone)
	t.Chdir(clone)
	report, err := Run(context.Background(), Options{Target: "./cmd/app", Since: "2025-01-01"})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Metadata.Shallow || report.Metadata.CoverageComplete || report.Metadata.Truncated || report.Metadata.OldestCommit != head || report.Metadata.OldestEventUTC == nil {
		t.Fatalf("shallow coverage misreported: %+v", report.Metadata)
	}
	if !strings.Contains(FormatText(report, 10), "Coverage:          Incomplete (shallow repository)") {
		t.Fatal("shallow coverage missing from terminal output")
	}
}

func TestFullPeriodAndOptionalHistoryLimit(t *testing.T) {
	t.Setenv("GOFLAGS", "")
	t.Setenv("GOWORK", "off")
	root := testRepository(t)
	initial := testGit(t, root, "rev-parse", "HEAD")
	// Fast-import keeps a >200-event regression fixture cheap to construct.
	var input strings.Builder
	for index := range 215 {
		message := fmt.Sprintf("change %d\n", index)
		body := fmt.Sprintf("package leaf\nfunc F(){println(%d)}\n", index)
		fmt.Fprintf(&input, "commit refs/heads/main\ncommitter History Test <history@example.org> %d +0000\ndata %d\n%s", 1735905600+index, len(message), message)
		if index == 0 {
			fmt.Fprintf(&input, "from %s\n", initial)
		}
		fmt.Fprintf(&input, "M 100644 inline leaf/lib.go\ndata %d\n%s\n", len(body), body)
	}
	fmt.Fprintln(&input, "done")
	cmd := exec.Command("git", "fast-import", "--quiet")
	cmd.Dir = root
	cmd.Stdin = strings.NewReader(input.String())
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fast-import failed: %v\n%s", err, output)
	}
	t.Chdir(root)
	full, err := Run(context.Background(), Options{Target: "./cmd/app", Since: "2025-01-01"})
	if err != nil {
		t.Fatal(err)
	}
	if len(full.Events) != 216 || full.Metadata.EventsAnalyzed != 216 || full.Metadata.MaxCommits != 0 || full.Metadata.Truncated || !full.Metadata.CoverageComplete || full.Metadata.OldestCommit != initial || full.Metadata.OldestEventUTC == nil {
		t.Fatalf("default history was truncated or coverage lost: %+v", full.Metadata)
	}
	limited, err := Run(context.Background(), Options{Target: "./cmd/app", Since: "2025-01-01", MaxCommits: 200})
	if err != nil {
		t.Fatal(err)
	}
	if len(limited.Events) != 200 || !limited.Metadata.Truncated || limited.Metadata.CoverageComplete || !limited.Metadata.OldestEventUTC.After(*full.Metadata.OldestEventUTC) || limited.Metadata.OldestCommit == initial {
		t.Fatalf("explicit limit/oldest coverage misreported: %+v", limited.Metadata)
	}
	out := FormatText(limited, 2)
	for _, token := range []string{"History requested: 2025-01-01", "Events analyzed:   200", "Oldest event:", "Coverage:          Incomplete (--max-commits=200)"} {
		if !strings.Contains(out, token) {
			t.Errorf("missing %q in report", token)
		}
	}
	if !strings.Contains(FormatText(full, 2), "Coverage:          Complete") {
		t.Fatal("unlimited coverage not explicit")
	}
	head := testGit(t, root, "rev-parse", "HEAD")
	events, truncated, err := readEvents(context.Background(), root, head, mustSince(t, "2025-01-01"), 216)
	if err != nil || len(events) != 216 || truncated {
		t.Fatal("limit equal to event count is not truncation", len(events), truncated, err)
	}
	data, err := json.Marshal(limited)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Report
	if err := json.Unmarshal(data, &decoded); err != nil || decoded.Metadata.SinceRequested != "2025-01-01" || decoded.Metadata.OldestCommit != limited.Metadata.OldestCommit || !decoded.Metadata.OldestEventUTC.Equal(*limited.Metadata.OldestEventUTC) {
		t.Fatal("coverage metadata lost in JSON", err)
	}
	empty, err := Run(context.Background(), Options{Target: "./cmd/app", Since: "2026-01-01"})
	if err != nil || empty.Metadata.OldestEventUTC != nil || len(empty.Events) != 0 || !empty.Metadata.CoverageComplete {
		t.Fatal("empty history coverage misreported", empty.Metadata, err)
	}
}
