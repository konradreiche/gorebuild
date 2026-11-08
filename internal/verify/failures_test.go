package verify

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func testEngine(t *testing.T, root, binary string) *engine {
	t.Helper()
	version, err := exec.Command("go", "env", "GOVERSION").Output()
	if err != nil {
		t.Fatal(err)
	}
	return &engine{root: root, temp: t.TempDir(), cwd: ".", goBinary: binary, version: strings.TrimSpace(string(version)), env: append(os.Environ(), "GOWORK=off"), flags: []string{"-mod=readonly", "-buildvcs=false"}}
}

func TestWarmVerificationAndActiveCancellationFailures(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fake Go scripts")
	}
	root, before, _ := repository(t)
	realGo, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	tools := t.TempDir()
	counter := filepath.Join(tools, "count")
	wrapper := filepath.Join(tools, "go")
	script := "#!/bin/sh\nn=0\nif [ -f '" + counter + "' ]; then read n < '" + counter + "'; fi\nn=$((n+1))\nprintf '%s\\n' \"$n\" > '" + counter + "'\ncommand=$1; shift\nif [ \"$n\" = 2 ]; then exec '" + realGo + "' \"$command\" -a \"$@\"; fi\nexec '" + realGo + "' \"$command\" \"$@\"\n"
	if err := os.WriteFile(wrapper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	runner := testEngine(t, root, wrapper)
	trees := testGit(t, root, "worktree", "list", "--porcelain")
	revision := &Revision{Commit: before, ChangedPackage: "example.org/verify/leaf", PackageDirectory: "leaf", PackageName: "leaf"}
	result := measure(context.Background(), runner, revision, "before", "./cmd/app", makeProbe("leaf"))
	if result.Status != "failed" || !strings.Contains(result.Reason, "not a no-op") || result.Warm == nil || result.Warm.Run.Graph.ExecutedActions == 0 || result.Incremental != nil {
		t.Fatal("warm commands were accepted", result.Status, result.Reason)
	}
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nexec sleep 30\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result = measure(ctx, runner, revision, "before", "./cmd/app", makeProbe("leaf"))
	if err := ctx.Err(); err != nil {
		t.Log(err)
	} else {
		t.Fatal("active cancellation did not expire")
	}
	if result.Status != "failed" || result.Cold == nil {
		t.Fatal("active cancellation evidence missing", result)
	}
	if testGit(t, root, "worktree", "list", "--porcelain") != trees {
		t.Fatal("warm/cancellation failure leaked worktrees")
	}
	entries, err := os.ReadDir(runner.temp)
	if err != nil || len(entries) != 0 {
		t.Fatal("temporary caches/trees leaked", entries, err)
	}
}

func TestFailingCorrectnessTestsBlockComparison(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fake Go script")
	}
	root, before, _ := repository(t)
	realGo, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	wrapper := filepath.Join(t.TempDir(), "go")
	script := "#!/bin/sh\nif [ \"$1\" = test ]; then echo 'intentional test failure'; exit 1; fi\nexec '" + realGo + "' \"$@\"\n"
	if err := os.WriteFile(wrapper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	runner := testEngine(t, root, wrapper)
	revision := &Revision{Ref: before, Commit: before}
	trees := testGit(t, root, "worktree", "list", "--porcelain")
	err = correctness(context.Background(), runner, revision, Options{Target: "./cmd/app", Package: "./leaf"}, nil)
	if err != nil {
		t.Log(err)
	} else {
		t.Fatal("intentional correctness failure was ignored")
	}
	if revision.Correct || revision.Build == nil || revision.Build.Failure != "" || revision.Tests == nil || !strings.Contains(revision.Tests.Log, "intentional test failure") {
		t.Fatal("failing tests accepted", err, revision)
	}
	if testGit(t, root, "worktree", "list", "--porcelain") != trees {
		t.Fatal("failed correctness tests leaked worktrees")
	}
}
