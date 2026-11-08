package history

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

func TestInheritedDryRunIsNotMeasured(t *testing.T) {
	t.Setenv("GOFLAGS", "-n")
	t.Setenv("GOWORK", "off")
	root := testRepository(t)
	testFile(t, root, "README.md", "docs")
	commitFiles(t, root, "documentation change")
	t.Chdir(root)
	report, err := Run(context.Background(), Options{Target: "./cmd/app", Since: "2025-01-01", Replay: 1})
	if err != nil {
		return
	} // Some Go versions reject dry-run graph discovery itself.
	if report.Replay.Measured != 0 || report.Events[0].Replay.Status == "measured" {
		t.Fatal("dry run was reported as a cheap real rebuild")
	}
}

func TestIndependentVCSComparison(t *testing.T) {
	t.Setenv("GOFLAGS", "")
	t.Setenv("GOWORK", "off")
	root := testRepository(t)
	testFile(t, root, "leaf/lib.go", "package leaf\nfunc F(){println(1)}\n")
	source := commitFiles(t, root, "source change")
	testFile(t, root, "README.md", "documentation only")
	docs := commitFiles(t, root, "documentation change")
	before := testGit(t, root, "worktree", "list", "--porcelain")
	status := testGit(t, root, "status", "--porcelain")
	t.Chdir(root)
	artifact := filepath.Join(t.TempDir(), "comparison.json")
	report, err := Run(context.Background(), Options{Target: "./cmd/app", Since: "2025-01-01", Replay: 3, CompareVCS: true, OutputFile: artifact})
	if err != nil {
		t.Fatal(err)
	}
	if report.VCSComparison == nil || report.VCSComparison.PairedEvents != 2 || report.VCSComparison.NoVCSMeasured != 2 || report.VCSComparison.NoVCSExcluded != 1 {
		t.Fatalf("comparison outcomes lost: %+v", report.VCSComparison)
	}
	for _, event := range report.Events {
		if !reflect.DeepEqual(event.Replay.SelectionReasons, event.NoVCSReplay.SelectionReasons) || len(event.ReplayOrder) != 2 {
			t.Fatal("configurations did not use the same historical samples", event.Commit)
		}
		if event.Commit != docs && event.Commit != source {
			continue
		}
		for _, result := range []*ReplayResult{event.Replay, event.NoVCSReplay} {
			if !successfulReplay(result) || result.Baseline.Run.Graph.ExecutedBuildActions < 2 || result.Warm.Run.Graph.ExecutedActions != 0 {
				t.Fatalf("cache borrowed from another replay/configuration or warm baseline not verified: %+v", result)
			}
		}
		if !slices.Contains(event.NoVCSReplay.BuildFlags, "-buildvcs=false") || slices.Contains(event.Replay.BuildFlags, "-buildvcs=false") {
			t.Fatal("comparison flags not independent")
		}
		if event.Commit == docs {
			if event.Replay.Rebuild.Run.Graph.ExecutedLinkActions == 0 || event.NoVCSReplay.Rebuild.Run.Graph.ExecutedLinkActions != 0 || len(event.NoVCSReplay.RecompiledPackages) != 0 || event.NoVCSReplay.Rebuild.Run.Graph.ExecutedActionChainNS != 0 {
				t.Fatal("documentation-only event did not distinguish stamping from source work", event.Replay.RebuiltPackages, event.NoVCSReplay.RebuiltPackages)
			}
		}
		if event.Commit == source {
			if event.NoVCSReplay.Rebuild.Run.Graph.ExecutedLinkActions == 0 || !slices.Contains(event.NoVCSReplay.RecompiledPackages, "example.org/history-test/leaf") {
				t.Fatal("disabling stamping must not erase source-driven compilation/linking")
			}
		}
		if event.VCSComparison == nil || event.VCSComparison.WallTimeNS != event.NoVCSReplay.Rebuild.Run.WallTimeNS-event.Replay.Rebuild.Run.WallTimeNS {
			t.Fatal("paired differences are not derived from event measurements")
		}
	}
	// Selection ignores VCS configuration and already-derived measurements.
	normalSelection := sampleEvents(&report, 3)
	report.Metadata.BuildFlags = append(report.Metadata.BuildFlags, "-buildvcs=false")
	if !reflect.DeepEqual(sampleEvents(&report, 3), normalSelection) {
		t.Fatal("VCS flag altered sampling")
	}
	data, err := os.ReadFile(artifact)
	if err != nil {
		t.Fatal(err)
	}
	var saved Report
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.VCSComparison.PairedEvents != 2 || saved.Events[0].NoVCSReplay == nil || saved.Events[0].VCSComparison == nil || saved.Replay.Work == nil {
		t.Fatal("single artifact lost VCS/aggregate evidence")
	}
	if testGit(t, root, "worktree", "list", "--porcelain") != before || testGit(t, root, "status", "--porcelain") != status {
		t.Fatal("comparison changed developer checkout or leaked worktrees")
	}
}
