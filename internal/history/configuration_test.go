package history

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigurationRelevanceBoundaries(t *testing.T) {
	config := configurationContext{
		mainDir: ".", files: map[string]string{"go.mod": "main module", "go.sum": "main module", "dep/go.mod": "selected local replacement"},
		packageDirs: []string{"cmd/app", "leaf", "dep"},
	}
	for name, want := range map[string]string{
		"go.mod": "relevant", "go.sum": "relevant", "dep/go.mod": "relevant", "leaf/go.mod": "relevant",
		"documentation/examples/remote_storage/go.mod": "unrelated", "compliance/go.mod": "unrelated",
		"go.work": "unrelated", "go.work.sum": "unrelated", "vendor/modules.txt": "unrelated",
		".go-version": "relevant", "documentation/.go-version": "unrelated",
	} {
		relevance, reason := config.relevance(name)
		if relevance != want || reason == "" {
			t.Errorf("%s: %s, %s; want %s", name, relevance, reason, want)
		}
	}
	config.vendor = true
	if relevance, _ := config.relevance("vendor/modules.txt"); relevance != "relevant" {
		t.Fatal("active vendor manifest ignored")
	}
}

func TestNestedTargetDoesNotUseAncestorModuleFiles(t *testing.T) {
	config := configurationContext{
		mainDir: "service", moduleRoots: []string{"service", "dep"},
		files:       map[string]string{"service/go.mod": "main", "service/go.sum": "main", "dep/go.mod": "replacement", "dep/go.sum": "replacement"},
		packageDirs: []string{"service/cmd/app", "service/pkg", "dep/pkg"},
	}
	for name, want := range map[string]string{
		"go.mod": "unrelated", "go.sum": "unrelated", "service/pkg/go.sum": "unrelated",
		"service/go.mod": "relevant", "service/go.sum": "relevant", "service/pkg/go.mod": "relevant", "dep/pkg/go.mod": "relevant",
	} {
		if relevance, _ := config.relevance(name); relevance != want {
			t.Errorf("%s: %s, want %s", name, relevance, want)
		}
	}
	config.vendor = true
	if relevance, _ := config.relevance("service/go.sum"); relevance != "unrelated" {
		t.Fatal("vendor mode should not read module checksum files")
	}
}

func TestUnknownConfigurationRelevanceIsExcluded(t *testing.T) {
	root := t.TempDir()
	event := Event{Files: []FileChange{{Path: "nested/go.mod"}}}
	reviews := reviewConfiguration(context.Background(), root, root, root, replayConfig{toolchain: filepath.Join(root, "missing-go")}, nil, event, "parent")
	result := &ReplayResult{}
	if len(reviews) != 1 || reviews[0].Relevance != "uncertain" || !excludeConfigurationChange(result, reviews) || result.Status != "excluded" {
		t.Fatal("unknown relevance was silently treated as unrelated", reviews, result)
	}
}

func TestUnrelatedNestedModuleChangesReplay(t *testing.T) {
	t.Setenv("GOFLAGS", "")
	t.Setenv("GOWORK", "off")
	root := testRepository(t)
	for _, dir := range []string{"documentation/examples/remote_storage", "compliance"} {
		testFile(t, root, dir+"/go.mod", "module example.org/"+filepath.Base(dir)+"\n\ngo 1.23\n")
	}
	commitFiles(t, root, "add unrelated nested modules")
	for _, dir := range []string{"documentation/examples/remote_storage", "compliance"} {
		testFile(t, root, dir+"/go.mod", "module example.org/"+filepath.Base(dir)+"\n\ngo 1.23\n\n")
	}
	commitFiles(t, root, "edit nested modules only")
	t.Chdir(root)
	report, err := Run(context.Background(), Options{Target: "./cmd/app", Since: "2025-01-01", Replay: 1})
	if err != nil {
		t.Fatal(err)
	}
	result := report.Events[0].Replay
	if result.Status != "measured" || len(result.ConfigurationReview) != 4 || report.Metadata.TargetModuleFile != "go.mod" {
		t.Fatalf("unrelated nested go.mod edits rejected: %+v", result)
	}
	for _, review := range result.ConfigurationReview {
		if review.Relevance != "unrelated" || review.Revision == "" || review.Reason == "" {
			t.Fatal("missing relevance evidence", review)
		}
	}
	// Main module changes are still excluded, before paying for a cold build.
	testFile(t, root, "go.mod", "module example.org/history-test\n\ngo 1.23\n\n")
	commitFiles(t, root, "edit selected module")
	report, err = Run(context.Background(), Options{Target: "./cmd/app", Since: "2025-01-01", Replay: 1})
	if err != nil {
		t.Fatal(err)
	}
	result = report.Events[0].Replay
	if result.Status != "excluded" || result.Baseline != nil || result.ConfigurationReview[0].Relevance != "relevant" || !strings.Contains(result.Reason, "go.mod") {
		t.Fatal("selected module configuration was not protected", result)
	}
}

func TestLocalDependencyModuleConfigurationIsRelevant(t *testing.T) {
	t.Setenv("GOFLAGS", "")
	t.Setenv("GOWORK", "off")
	root := testRepository(t)
	testFile(t, root, "go.mod", "module example.org/history-test\n\ngo 1.23\nrequire example.org/dep v0.0.0\nreplace example.org/dep => ./dep\n")
	testFile(t, root, "dep/go.mod", "module example.org/dep\n\ngo 1.23\n")
	testFile(t, root, "dep/lib.go", "package dep\nfunc F(){}\n")
	testFile(t, root, "leaf/lib.go", "package leaf\nimport \"example.org/dep\"\nfunc F(){dep.F()}\n")
	commitFiles(t, root, "add local dependency")
	testFile(t, root, "dep/go.mod", "module example.org/dep\n\ngo 1.23\n\n")
	commitFiles(t, root, "change dependency module configuration")
	t.Chdir(root)
	report, err := Run(context.Background(), Options{Target: "./cmd/app", Since: "2025-01-01", Replay: 1})
	if err != nil {
		t.Fatal(err)
	}
	result := report.Events[0].Replay
	if result.Status != "excluded" || result.Baseline != nil || result.ConfigurationReview[0].Relevance != "relevant" || !strings.Contains(result.Reason, "dep/go.mod") {
		t.Fatal("used local module configuration was mistaken for an unrelated nested module", result)
	}
}
