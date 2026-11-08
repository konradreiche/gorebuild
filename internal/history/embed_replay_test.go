package history

import (
	"context"
	"slices"
	"strings"
	"testing"
)

func TestEmbeddedAssetReplay(t *testing.T) {
	t.Setenv("GOFLAGS", "")
	t.Setenv("GOWORK", "off")
	root := testRepository(t)
	testFile(t, root, "leaf/lib.go", "package leaf\nimport _ \"embed\"\n//go:embed data.txt\nvar data string\nfunc F(){println(data)}\n")
	testFile(t, root, "leaf/data.txt", "before")
	commitFiles(t, root, "embed data")
	testFile(t, root, "leaf/data.txt", "after")
	changed := commitFiles(t, root, "change embedded data only")
	t.Chdir(root)
	report, err := Run(context.Background(), Options{Target: "./cmd/app", Since: "2025-01-01", MaxCommits: 1, Replay: 1})
	if err != nil {
		t.Fatal(err)
	}
	event := report.Events[0]
	if event.Commit != changed || event.Files[0].Classification != "potential_build" || !strings.Contains(event.Files[0].Reason, "embed") || !slices.Equal(event.BuildPackageDirs, []string{"leaf"}) {
		t.Fatalf("go list embed inputs not used in attribution: %+v", event)
	}
	if event.Replay.Status != "measured" || !slices.Contains(event.Replay.RebuiltPackages, "example.org/history-test/leaf") || event.Replay.Warm.Run.Graph.ExecutedActions != 0 || event.Replay.BuildTarget != "./cmd/app" || len(event.Replay.BuildFlags) == 0 {
		t.Fatalf("embedded change not measured with evidence: %+v", event.Replay)
	}
	for _, pkg := range report.Packages {
		if pkg.Dir == "leaf" && (pkg.BuildChanges != 1 || pkg.Measured.Samples != 1 || pkg.Measured.MedianWallTimeNS != nil) {
			t.Fatal("asset-only event omitted from package measurements", pkg)
		}
	}
}
