package history

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestReplayRejectsInvocationOutsideRepository(t *testing.T) {
	root := t.TempDir()
	report := Report{
		Metadata: Metadata{Repository: root, ImportPath: "example.org/app"},
		Graph:    []PackageNode{{ImportPath: "example.org/app", Dir: filepath.Join(root, "cmd", "app")}},
		Events:   []Event{{Commit: "selected"}},
	}
	replayHistory(context.Background(), &report, Options{Replay: 1}, t.TempDir(), "")
	result := report.Events[0].Replay
	if result == nil || result.Status != "excluded" || result.Stage != "configuration" || !strings.Contains(result.Reason, "invocation directory is outside") {
		t.Fatalf("outside invocation was not excluded: %+v", result)
	}
}

func TestSampleEvents(t *testing.T) {
	var events []Event
	for index := range 10 {
		event := Event{Commit: fmt.Sprint(index)}
		if index%2 == 0 {
			event.PackageDirs = []string{"leaf"}
		}
		events = append(events, event)
	}
	for _, test := range []struct {
		count int
		focus string
		want  []int
	}{
		{3, "", []int{0, 4, 9}},
		{1, "", []int{0}},
		{0, "", nil},
		{3, "leaf", []int{0, 4, 8}},
		{20, "leaf", []int{0, 2, 4, 6, 8}},
		{3, "missing", nil},
	} {
		selections := sampleEvents(&Report{Events: events, FocusPackage: test.focus}, test.count)
		var got []int
		for _, selection := range selections {
			got = append(got, selection.Index)
			if len(selection.Reasons) == 0 {
				t.Fatal("sample lacks selection evidence")
			}
		}
		if !slices.Equal(got, test.want) {
			t.Errorf("sample(%d, %s) = %v, want %v", test.count, test.focus, got, test.want)
		}
	}
}

func commitFiles(t *testing.T, root, message string) string {
	t.Helper()
	testGit(t, root, "add", ".")
	testGit(t, root, "commit", "-m", message)
	return testGit(t, root, "rev-parse", "HEAD")
}

func TestHistoricalReplaysRealGo(t *testing.T) {
	t.Setenv("GOFLAGS", "")
	t.Setenv("GOWORK", "off")
	root := testRepository(t)
	initial := testGit(t, root, "rev-parse", "HEAD")
	testFile(t, root, "leaf/lib.go", "package leaf\nfunc F() { println(1) }\n")
	testFile(t, root, "consumer/lib.go", "package consumer\nimport \"example.org/history-test/leaf\"\nfunc F(){leaf.F(); println(2)}\n")
	coChanged := commitFiles(t, root, "co-change implementation")
	testFile(t, root, "leaf/lib_test.go", "package leaf\n")
	testOnly := commitFiles(t, root, "test only")
	testFile(t, root, "leaf/lib.go", "package leaf\nfunc F() { DOES_NOT_EXIST() }\n")
	broken := commitFiles(t, root, "temporarily broken")
	testFile(t, root, "leaf/lib.go", "package leaf\nfunc F() { println(3) }\n")
	fixed := commitFiles(t, root, "fix build")
	// A dirty developer tree must never become replay input or be reset.
	testFile(t, root, "leaf/lib.go", "package leaf\nfunc F() { println(999) }\n")
	testFile(t, root, "untracked.txt", "keep this")
	before := testGit(t, root, "status", "--porcelain=v1", "-z")
	worktrees := testGit(t, root, "worktree", "list", "--porcelain")
	t.Chdir(root)
	artifact := filepath.Join(t.TempDir(), "history.json")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	report, err := Run(ctx, Options{Target: "./cmd/app", Since: "2025-01-01", Replay: 5, OutputFile: artifact})
	if err != nil {
		t.Fatal(err)
	}
	if report.SchemaVersion != 4 || report.Replay.Selected != 5 || report.Replay.Measured != 2 || report.Replay.Failed != 2 || report.Replay.Excluded != 1 {
		t.Fatalf("unexpected replay summary: %+v", report.Replay)
	}
	byCommit := make(map[string]*ReplayResult)
	var total int64
	for _, event := range report.Events {
		byCommit[event.Commit] = event.Replay
		if len(event.Replay.SelectionReasons) == 0 {
			t.Fatal("sample selection reason missing from event evidence")
		}
		if event.Replay.Status == "measured" {
			result := event.Replay
			if result.Baseline.Run.Graph.ExecutedBuildActions < 2 || result.Warm.Run.Graph.ExecutedActions != 0 || result.Rebuild.Run.WallTimeNS <= 0 {
				t.Fatalf("uncontrolled cache or missing measurement: %+v", result)
			}
			for _, capture := range []json.RawMessage{result.Baseline.ActionGraph, result.Warm.ActionGraph, result.Rebuild.ActionGraph} {
				if !json.Valid(capture) {
					t.Fatal("raw action evidence missing")
				}
			}
			total += result.Rebuild.Run.WallTimeNS
		}
	}
	if byCommit[initial].Status != "excluded" || byCommit[broken].Status != "failed" || byCommit[fixed].Status != "failed" {
		t.Fatal("failed/root events treated as measurements")
	}
	if !strings.Contains(byCommit[broken].Rebuild.Log, "DOES_NOT_EXIST") || !strings.Contains(byCommit[fixed].Baseline.Log, "DOES_NOT_EXIST") {
		t.Fatal("failed compiler evidence was lost")
	}
	if !slices.Contains(byCommit[coChanged].RebuiltPackages, "example.org/history-test/leaf") || !slices.Contains(byCommit[coChanged].RebuiltPackages, "example.org/history-test/consumer") {
		t.Fatal("executed compilation not captured", byCommit[coChanged].RebuiltPackages)
	}
	if slices.Contains(byCommit[testOnly].RebuiltPackages, "example.org/history-test/leaf") {
		t.Fatal("test-only change unexpectedly recompiled leaf")
	}
	if report.Replay.TotalMeasuredWallTimeNS != total {
		t.Fatal("co-changing packages double-counted in total event latency")
	}
	for _, pkg := range report.Packages {
		switch pkg.Dir {
		case "leaf":
			if pkg.Measured.Samples != 1 || pkg.Measured.Failed != 2 || pkg.Measured.Excluded != 1 || pkg.Measured.MedianWallTimeNS != nil {
				t.Fatalf("incorrect sample coverage: %+v", pkg.Measured)
			}
		case "outside":
			if pkg.Measured.Samples != 0 || pkg.Measured.MedianWallTimeNS != nil {
				t.Fatal("excluded/unmeasured package reported as cheap")
			}
		}
	}
	data, err := os.ReadFile(artifact)
	if err != nil {
		t.Fatal(err)
	}
	var saved Report
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal("invalid replay artifact", err)
	}
	// MarshalIndent reformats embedded RawMessages; compare normalized JSON.
	wantJSON, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	savedJSON, err := json.Marshal(saved)
	if err != nil || string(wantJSON) != string(savedJSON) {
		t.Fatal("replay artifact lost evidence", err)
	}
	if got := testGit(t, root, "status", "--porcelain=v1", "-z"); got != before || testGit(t, root, "rev-parse", "HEAD") != fixed || testGit(t, root, "worktree", "list", "--porcelain") != worktrees {
		t.Fatal("replay mutated developer tree/HEAD or leaked a worktree")
	}
	out := FormatText(report, 10)
	for _, token := range []string{"2 measured / 5 selected", "2 failed, 1 excluded", "Replays/all", "cohorts", "Failed or excluded"} {
		if !strings.Contains(out, token) {
			t.Errorf("missing %q in report", token)
		}
	}
	report.FocusPackage = "leaf"
	out = FormatText(report, 10)
	for _, token := range []string{"rebuild median:", "Measured build events: 1 / 4", "Single successful observation", "Rebuilt packages:", "failed at rebuild", "Rebuild: excluded"} {
		if !strings.Contains(out, token) {
			t.Errorf("missing %q in package report", token)
		}
	}
}

func TestReplayRejectsNonNoopBaseline(t *testing.T) {
	t.Setenv("GOFLAGS", "")
	t.Setenv("GOWORK", "off")
	root := testRepository(t)
	testFile(t, root, "leaf/lib.go", "package leaf\nfunc F(){ println(1) }\n")
	commitFiles(t, root, "edit")
	t.Chdir(root)
	report, err := Run(context.Background(), Options{Target: "./cmd/app", Since: "2025-01-01", Replay: 1, BuildFlags: []string{"-a"}})
	if err != nil {
		t.Fatal(err)
	}
	result := report.Events[0].Replay
	if report.Replay.Measured != 0 || result.Status != "excluded" || result.Stage != "warm" || result.Warm.Run.Graph.ExecutedActions == 0 || result.Rebuild != nil {
		t.Fatalf("non-no-op baseline accepted: %+v", result)
	}
}

func TestReplayExcludesConfigAndExternalReplacements(t *testing.T) {
	t.Setenv("GOFLAGS", "")
	t.Setenv("GOWORK", "off")
	root := testRepository(t)
	testFile(t, root, "go.mod", "module example.org/history-test\n\ngo 1.23\n\nreplace example.org/external => ../external\n")
	commitFiles(t, root, "module change")
	testFile(t, root, "leaf/lib.go", "package leaf\nfunc F(){println(1)}\n")
	commitFiles(t, root, "edit source")
	t.Chdir(root)
	report, err := Run(context.Background(), Options{Target: "./cmd/app", Since: "2025-01-01", Replay: 3})
	if err != nil {
		t.Fatal(err)
	}
	if report.Replay.Excluded != 3 || report.Events[0].Replay.Stage != "baseline-preflight" || !strings.Contains(report.Events[0].Replay.Reason, "outside") || !strings.Contains(report.Events[1].Replay.Reason, "configuration changed") {
		t.Fatalf("external/config exclusions missing: %+v", report.Events)
	}
}

func TestReplayCancellationCleansWorktree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test wrapper uses a POSIX shell")
	}
	t.Setenv("GOFLAGS", "")
	t.Setenv("GOWORK", "off")
	root := testRepository(t)
	testFile(t, root, "leaf/lib.go", "package leaf\nfunc F(){println(1)}\n")
	commitFiles(t, root, "edit")
	before := testGit(t, root, "worktree", "list", "--porcelain")
	goBinary, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "started")
	fakeRoot := t.TempDir()
	wrapper := filepath.Join(fakeRoot, "bin", "go")
	if err := os.MkdirAll(filepath.Dir(wrapper), 0o755); err != nil {
		t.Fatal(err)
	}
	// Let Run select this wrapper for replay, but use the real toolchain for
	// graph/module preflights. Block only at the first measured build phase.
	envJSON, err := json.Marshal(map[string]string{"GOROOT": fakeRoot, "GOVERSION": runtime.Version(), "GOOS": runtime.GOOS, "GOARCH": runtime.GOARCH, "GOWORK": "off"})
	if err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf("#!/bin/sh\nexport GOROOT=%q\nif [ \"$1\" = env ] && [ \"$3\" = GOVERSION ]; then printf '%%s\\n' %q; exit; fi\nif [ \"$1\" = build ]; then touch %q; exec sleep 30; fi\nexec %q \"$@\"\n", runtime.GOROOT(), string(envJSON), marker, goBinary)
	if err := os.WriteFile(wrapper, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := os.Stat(marker); err != nil {
					continue
				}
				cancel()
				return
			}
		}
	}()
	t.Chdir(root)
	artifact := filepath.Join(t.TempDir(), "cancelled.json")
	_, err = Run(ctx, Options{Target: "./cmd/app", Since: "2025-01-01", Replay: 1, GoBinary: wrapper, OutputFile: artifact})
	<-stopped
	if !errors.Is(err, context.Canceled) {
		t.Fatal("Run did not report cancellation", err)
	}
	data, err := os.ReadFile(artifact)
	if err != nil {
		t.Fatal("partial replay artifact missing", err)
	}
	var saved Report
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	result := saved.Events[0].Replay
	if saved.Replay.Failed != 1 || result.Status != "failed" || result.Stage != "baseline" || !strings.Contains(result.Reason, "context canceled") {
		t.Fatalf("cancellation not retained: %+v", result)
	}
	if testGit(t, root, "worktree", "list", "--porcelain") != before {
		t.Fatal("cancelled replay leaked a registered worktree")
	}
}
