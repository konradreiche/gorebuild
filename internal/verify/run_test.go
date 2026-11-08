package verify

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func writeTestFile(t *testing.T, root, name, content string) {
	t.Helper()
	file := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
func testGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	runner := engine{root: root}
	output, err := runner.git(context.Background(), args...)
	if err != nil {
		t.Fatal(args, err)
	}
	return strings.TrimSpace(string(output))
}

func repository(t *testing.T) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	testGit(t, root, "init")
	testGit(t, root, "config", "user.name", "Verify")
	testGit(t, root, "config", "user.email", "verify@example.org")
	writeTestFile(t, root, "go.mod", "module example.org/verify\n\ngo 1.23\n")
	writeTestFile(t, root, "leaf/leaf.go", "package leaf\nfunc Value() int{return 7}\n")
	writeTestFile(t, root, "api/api.go", "package api\nimport \"example.org/verify/leaf\"\nfunc Value()int{return leaf.Value()}\n")
	writeTestFile(t, root, "web/web.go", "package web\nimport \"example.org/verify/api\"\nfunc Value()int{return api.Value()}\n")
	writeTestFile(t, root, "cmd/app/main.go", "package main\nimport \"example.org/verify/web\"\nfunc main(){println(web.Value())}\n")
	testGit(t, root, "add", ".")
	testGit(t, root, "commit", "-m", "before")
	before := testGit(t, root, "rev-parse", "HEAD")
	writeTestFile(t, root, "api/api.go", "package api\nfunc Value(input int)int{return input}\n")
	writeTestFile(t, root, "web/web.go", "package web\nimport \"example.org/verify/api\"\nimport \"example.org/verify/leaf\"\nfunc Value()int{return api.Value(leaf.Value())}\n")
	testGit(t, root, "add", ".")
	testGit(t, root, "commit", "-m", "after")
	after := testGit(t, root, "rev-parse", "HEAD")
	return root, before, after
}

func TestRealPairedVerificationIsolationAndArtifact(t *testing.T) {
	t.Setenv("GOWORK", "off")
	t.Setenv("GOFLAGS", "")
	normal := t.TempDir()
	t.Setenv("GOCACHE", normal)
	root, before, after := repository(t)
	t.Chdir(root)
	writeTestFile(t, root, "api/api.go", "package api\nDO_NOT_BUILD_DEVELOPER_TREE\n")
	writeTestFile(t, root, "untracked", "preserve")
	status := testGit(t, root, "status", "--porcelain")
	trees := testGit(t, root, "worktree", "list", "--porcelain")
	artifact := filepath.Join(t.TempDir(), "verify.json.gz")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	report := Run(ctx, Options{Target: "./cmd/app", Before: before, After: after, Package: "./leaf", Runs: 2, OutputFile: artifact})
	if len(report.Errors) > 0 || !report.PerformanceValid || !report.Before.Correct || !report.After.Correct {
		t.Fatal(report.Errors)
	}
	if len(report.Pairs) != 2 || !slices.Equal(report.Pairs[0].Order, []string{"before", "after"}) || !slices.Equal(report.Pairs[1].Order, []string{"after", "before"}) {
		t.Fatal("paired order not alternated", report.Pairs)
	}
	if !slices.Contains(report.DependencyChanges.Removed, Edge{From: "example.org/verify/api", To: "example.org/verify/leaf"}) || !slices.Contains(report.DependencyChanges.Added, Edge{From: "example.org/verify/web", To: "example.org/verify/leaf"}) {
		t.Fatal(report.DependencyChanges)
	}
	for _, pair := range report.Pairs {
		for _, value := range []*Measurement{pair.Before, pair.After} {
			if value.Status != "measured" || value.Cold.Run.Graph.ExecutedBuildActions < 4 || value.Warm.Run.Graph.ExecutedActions != 0 || value.ProbeSHA256 != report.Probe.SHA256 || !slices.Contains(value.Recompiled, "example.org/verify/leaf") {
				t.Fatal("cache preparation or probe invalid", value.Status, value.Reason)
			}
			if value.Incremental.Run.Graph.ExecutedActionChainNS <= 0 || value.Incremental.Run.Graph.CompileCommands == 0 {
				t.Fatal("critical-path/executed-command evidence absent")
			}
			if value.Revision == "before" && !slices.Contains(value.Recompiled, "example.org/verify/api") {
				t.Fatal("before API compilation not detected")
			}
			if value.Revision == "after" && slices.Contains(value.Recompiled, "example.org/verify/api") {
				t.Fatal("removed dependency still recompiled API")
			}
		}
	}
	if report.Summary.ValidPairs != 2 || !report.Summary.IncrementalWall.PairedDelta.MedianAvailable {
		t.Fatal(report.Summary)
	}
	if testGit(t, root, "status", "--porcelain") != status || testGit(t, root, "worktree", "list", "--porcelain") != trees {
		t.Fatal("developer state/worktrees changed")
	}
	entries, err := os.ReadDir(normal)
	if err != nil || len(entries) != 0 {
		t.Fatal("normal cache used", entries, err)
	}
	input, err := os.Open(artifact)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	compressed, err := gzip.NewReader(input)
	if err != nil {
		t.Fatal(err)
	}
	defer compressed.Close()
	var saved Report
	if err := json.NewDecoder(compressed).Decode(&saved); err != nil {
		t.Fatal(err)
	}
	if !saved.PerformanceValid || saved.Pairs[0].Before.ProbeSHA256 != report.Probe.SHA256 || saved.Summary.ValidPairs != 2 {
		t.Fatal("single artifact lost evidence")
	}
	existing := Run(ctx, Options{Target: "./cmd/app", Before: before, After: after, Package: "./leaf", Runs: 1, OutputFile: artifact})
	if len(existing.Errors) == 0 || len(existing.Pairs) > 0 {
		t.Fatal("existing artifact overwritten")
	}
}

func TestCorrectnessFailureAndCancelledRunRetainDiagnostics(t *testing.T) {
	t.Setenv("GOWORK", "off")
	t.Setenv("GOFLAGS", "")
	root, before, after := repository(t)
	t.Chdir(root)
	writeTestFile(t, root, "api/api.go", "package api\nfunc Value(int)int{return DoesNotExist}\n")
	testGit(t, root, "add", ".")
	testGit(t, root, "commit", "-m", "broken")
	broken := testGit(t, root, "rev-parse", "HEAD")
	trees := testGit(t, root, "worktree", "list", "--porcelain")
	file := filepath.Join(t.TempDir(), "failed.json")
	// Broken baseline fails the correctness build before any benchmarks run.
	report := Run(context.Background(), Options{Target: "./cmd/app", Before: broken, After: after, Package: "./leaf", Runs: 1, OutputFile: file})
	if len(report.Errors) == 0 || report.PerformanceValid || len(report.Pairs) != 0 || report.Before.Build == nil || !strings.Contains(report.Before.Build.Log, "DoesNotExist") {
		t.Fatal("failed correctness treated as performance evidence", report.Errors)
	}
	content, err := os.ReadFile(file)
	if err != nil || !json.Valid(content) {
		t.Fatal("failed artifact lost", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	report = Run(ctx, Options{Target: "./cmd/app", Before: before, After: after, Package: "./leaf", Runs: 1, OutputFile: filepath.Join(t.TempDir(), "cancel.json")})
	if len(report.Errors) == 0 || report.PerformanceValid {
		t.Fatal("cancelled verification accepted")
	}
	if testGit(t, root, "worktree", "list", "--porcelain") != trees {
		t.Fatal("error/cancellation leaked worktrees")
	}
}

func TestProbeRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writeTestFile(t, outside, "leaf.go", "package leaf\n")
	if err := os.Symlink(outside, filepath.Join(root, "leaf")); err != nil {
		t.Skip(err)
	}
	if err := applyProbe(&tree{path: root}, &Revision{PackageDirectory: "leaf"}, makeProbe("leaf")); err != nil {
		if !strings.Contains(err.Error(), "escapes") {
			t.Fatal(err)
		}
	} else {
		t.Fatal("probe followed source symlink outside isolated tree")
	}
	if _, err := os.Stat(filepath.Join(outside, makeProbe("leaf").File)); !os.IsNotExist(err) {
		t.Fatal("external source was modified", err)
	}
}

func TestProbeCollisionAndManagedFlags(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "leaf/leaf.go", "package leaf\nconst "+probeName+"=1\n")
	if err := applyProbe(&tree{path: root}, &Revision{PackageDirectory: "leaf"}, makeProbe("leaf")); err != nil {
		if !strings.Contains(err.Error(), "already exists") {
			t.Fatal(err)
		}
	} else {
		t.Fatal("probe collided with existing declaration")
	}
	for _, flag := range []string{"-n", "-a", "-buildvcs=true", "-mod=mod", "-overlay=x", "-toolexec=x"} {
		if err := validateFlags([]string{flag}); err != nil {
			continue
		}
		t.Fatal("uncontrolled flag accepted", flag)
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Fatal(err)
	}
}
