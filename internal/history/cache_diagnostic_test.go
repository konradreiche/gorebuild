package history

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSelectedCacheHashReplayIsolation(t *testing.T) {
	t.Setenv("GOFLAGS", "")
	t.Setenv("GOWORK", "off")
	normal := t.TempDir()
	t.Setenv("GOCACHE", normal)
	root := testRepository(t)
	testFile(t, root, "leaf/lib.go", "package leaf\nfunc F(){println(17)}\n")
	commit := commitFiles(t, root, "change leaf input")
	testFile(t, root, "untracked.txt", "preserve")
	before := testGit(t, root, "status", "--porcelain=v1", "-z")
	trees := testGit(t, root, "worktree", "list", "--porcelain")
	t.Chdir(root)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	report, err := Run(ctx, Options{Target: "./cmd/app", Since: "2025-01-01"})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range report.Events {
		if event.Commit == commit {
			report.Events = []Event{event}
			break
		}
	}
	result, err := DiagnoseCache(ctx, &report, root, "example.org/history-test/leaf", ".")
	if err != nil || result.Status != "measured" {
		t.Fatal(result, err)
	}
	for _, capture := range []string{result.Baseline.Log, result.Warm.Log, result.Rebuild.Log} {
		if !strings.Contains(capture, "HASH[build example.org/history-test/leaf]") || !strings.Contains(capture, "file lib.go") {
			t.Fatal("selected hash inputs missing", capture)
		}
		if strings.Contains(capture, "HASH[moduleIndex]") || strings.Contains(capture, "HASH[build example.org/history-test/consumer]") {
			t.Fatal("unselected hashes retained")
		}
	}
	if result.Warm.Run.Graph.ExecutedActions != 0 {
		t.Fatal("warm verification lost")
	}
	if testGit(t, root, "status", "--porcelain=v1", "-z") != before || testGit(t, root, "worktree", "list", "--porcelain") != trees {
		t.Fatal("diagnostic changed developer checkout or leaked worktrees")
	}
	entries, err := os.ReadDir(filepath.Clean(normal))
	if err != nil || len(entries) != 0 {
		t.Fatal("normal cache changed", entries, err)
	}
}
