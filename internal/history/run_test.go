package history

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func mustSince(t *testing.T, value string) time.Time {
	t.Helper()
	stamp, err := ParseSince(value, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return stamp
}

func testGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=History Test", "-c", "user.email=history@example.org", "-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null"}, args...)...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE=2025-01-02T12:00:00Z", "GIT_COMMITTER_DATE=2025-01-02T12:00:00Z")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

func testFile(t *testing.T, root, name, body string) {
	t.Helper()
	file := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func testRepository(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	testGit(t, root, "init", "-b", "main")
	for name, body := range map[string]string{
		"go.mod":          "module example.org/history-test\n\ngo 1.23\n",
		"cmd/app/main.go": "package main\nimport \"example.org/history-test/consumer\"\nfunc main(){consumer.F()}\n",
		"consumer/lib.go": "package consumer\nimport \"example.org/history-test/leaf\"\nfunc F(){leaf.F()}\n",
		"leaf/lib.go":     "package leaf\nfunc F() {}\n",
		"outside/lib.go":  "package outside\nfunc F() {}\n",
	} {
		testFile(t, root, name, body)
	}
	testGit(t, root, "add", ".")
	testGit(t, root, "commit", "-m", "initial")
	return root
}

func TestRunHistoryAndArtifact(t *testing.T) {
	t.Setenv("GOFLAGS", "")
	t.Setenv("GOWORK", "off")
	root := testRepository(t)
	testFile(t, root, "leaf/lib.go", "package leaf\nfunc F() { println(1) }\n")
	testFile(t, root, "consumer/more.go", "package consumer\nconst More = 1\n")
	testGit(t, root, "add", ".")
	testGit(t, root, "commit", "-m", "co-change")
	coChange := testGit(t, root, "rev-parse", "HEAD")
	testFile(t, root, "leaf/lib_test.go", "package leaf\n")
	testGit(t, root, "add", ".")
	testGit(t, root, "commit", "-m", "test only")
	// Dirty state must be reported and preserved, including untracked files.
	testFile(t, root, "untracked.txt", "leave me alone")
	before := testGit(t, root, "status", "--porcelain=v1", "-z")
	head := testGit(t, root, "rev-parse", "HEAD")
	t.Chdir(filepath.Join(root, "cmd"))
	artifact := filepath.Join(t.TempDir(), "capture", "history.json")
	report, err := Run(context.Background(), Options{Target: "./app", Since: "2025-01-01", MaxCommits: 2, Package: "../leaf", OutputFile: artifact})
	if err != nil {
		t.Fatal(err)
	}
	if report.Metadata.ImportPath != "example.org/history-test/cmd/app" || !report.Metadata.Dirty || !report.Metadata.Truncated || report.Metadata.Head != head || report.FocusPackage != "leaf" {
		t.Fatalf("incorrect metadata/focus: %+v", report)
	}
	if len(report.Events) != 2 || report.Events[1].Commit != coChange || len(report.Events[0].EstimatedAffectedPackages) != 0 || len(report.Events[1].EstimatedAffectedPackages) != 3 {
		t.Fatalf("incorrect event selection or union: %+v", report.Events)
	}
	data, err := os.ReadFile(artifact)
	if err != nil {
		t.Fatal(err)
	}
	var saved Report
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(saved, report) {
		t.Fatal("JSON artifact does not preserve complete report")
	}
	entries, err := os.ReadDir(filepath.Dir(artifact))
	if err != nil || len(entries) != 1 {
		t.Fatal("expected a single artifact", entries, err)
	}
	if after := testGit(t, root, "status", "--porcelain=v1", "-z"); before != after || testGit(t, root, "rev-parse", "HEAD") != head {
		t.Fatal("history changed the working tree or HEAD")
	}
	if _, err := Run(context.Background(), Options{Target: "./app", OutputFile: artifact}); err != nil {
		if !strings.Contains(err.Error(), "already exists") {
			t.Fatal("existing artifact not protected", err)
		}
	} else {
		t.Fatal("existing artifact not protected")
	}
	for _, query := range []string{"leaf", "../leaf", "example.org/history-test/leaf"} {
		focus, err := resolveFocus(report, filepath.Join(root, "cmd"), query)
		if err != nil || focus != "leaf" {
			t.Errorf("resolve %q: %q, %v", query, focus, err)
		}
	}
	if _, err := resolveFocus(report, root, "not/a/package"); err != nil {
		return
	}
	t.Fatal("unknown package accepted")
}

func TestGitEventsRootRenamesMergesAndLimits(t *testing.T) {
	root := testRepository(t)
	ctx := context.Background()
	initial := testGit(t, root, "rev-parse", "HEAD")
	events, truncated, err := readEvents(ctx, root, initial, mustSince(t, "2025-01-01"), 20)
	if err != nil || truncated || len(events) != 1 || len(events[0].Files) != 5 || len(events[0].Parents) != 0 {
		t.Fatalf("root commit lost: %+v, %v, %v", events, truncated, err)
	}
	testGit(t, root, "checkout", "-b", "feature")
	// NUL-delimited paths must preserve newlines and spaces verbatim.
	testFile(t, root, "odd dir/name\nfile.go", "package odd\n")
	testGit(t, root, "add", ".")
	testGit(t, root, "commit", "-m", "side branch")
	side := testGit(t, root, "rev-parse", "HEAD")
	testGit(t, root, "checkout", "main")
	testFile(t, root, "README.md", "docs")
	testGit(t, root, "add", ".")
	testGit(t, root, "commit", "-m", "main work")
	testGit(t, root, "merge", "--no-ff", "feature", "-m", "merge side")
	merge := testGit(t, root, "rev-parse", "HEAD")
	events, _, err = readEvents(ctx, root, merge, mustSince(t, "2025-01-01"), 20)
	if err != nil || len(events) != 3 || len(events[0].Parents) != 2 || len(events[0].Files) != 1 || events[0].Files[0].Path != "odd dir/name\nfile.go" {
		t.Fatalf("merge should be one first-parent delta: %+v, %v", events, err)
	}
	for _, event := range events {
		if event.Commit == side {
			t.Fatal("side commit double-counted with merge event")
		}
	}
	testGit(t, root, "mv", "leaf/lib.go", "leaf/renamed.go")
	testGit(t, root, "commit", "-m", "rename")
	head := testGit(t, root, "rev-parse", "HEAD")
	events, truncated, err = readEvents(ctx, root, head, mustSince(t, "2025-01-01"), 1)
	if err != nil || !truncated || len(events) != 1 {
		t.Fatalf("limit not reported: %+v %v %v", events, truncated, err)
	}
	var paths []string
	for _, file := range events[0].Files {
		paths = append(paths, file.Path)
	}
	if !slices.Equal(paths, []string{"leaf/lib.go", "leaf/renamed.go"}) {
		t.Fatal("rename must retain both paths", paths)
	}
	events, truncated, err = readEvents(ctx, root, head, mustSince(t, "2026-01-01"), 1)
	if err != nil || truncated || len(events) != 0 {
		t.Fatal("since filter ignored", events, truncated, err)
	}
}

func TestRunFailuresLeaveNoArtifact(t *testing.T) {
	t.Setenv("GOFLAGS", "")
	t.Setenv("GOWORK", "off")
	root := testRepository(t)
	t.Chdir(root)
	artifact := filepath.Join(t.TempDir(), "failed.json")
	if _, err := Run(context.Background(), Options{Target: "./cmd/app", BuildFlags: []string{"-mod=mod"}, OutputFile: artifact}); err != nil {
		if !strings.Contains(err.Error(), "protect the checkout") {
			t.Fatal("unexpected module mode error", err)
		}
	} else {
		t.Fatal("unsafe module mode accepted")
	}
	if _, err := Run(context.Background(), Options{Target: "./missing", OutputFile: artifact}); err != nil {
		if !strings.Contains(err.Error(), "go list") {
			t.Fatal("unexpected graph discovery error", err)
		}
	} else {
		t.Fatal("go list error ignored")
	}
	if _, err := os.Stat(artifact); !os.IsNotExist(err) {
		t.Fatal("failed run left an artifact", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Run(ctx, Options{Target: "./cmd/app"}); err != nil {
		return
	}
	t.Fatal("cancellation ignored")
}

func TestEmptyHistoryAndUnchangedPackage(t *testing.T) {
	t.Setenv("GOFLAGS", "-mod=mod") // Effective command flags must override this.
	t.Setenv("GOWORK", "off")
	root := testRepository(t)
	t.Chdir(root)
	report, err := Run(context.Background(), Options{Target: "./cmd/app", Since: "2026-01-01", Package: "./leaf"})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Events) != 0 || report.Metadata.Truncated || report.FocusPackage != "leaf" || !slices.Contains(report.Metadata.BuildFlags, "-mod=readonly") {
		t.Fatalf("empty history or effective flags misreported: %+v", report)
	}
	if out := FormatText(report, 10); !strings.Contains(out, "Change events: 0") || !strings.Contains(out, "(none in selected history)") {
		t.Fatal("unchanged package not handled", out)
	}
}
