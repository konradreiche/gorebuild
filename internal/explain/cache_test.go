package explain

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestOptionalSavedPrometheusCacheEvidence(t *testing.T) {
	file := os.Getenv("GOREBUILD_PROMETHEUS_CACHE")
	if file == "" {
		t.Skip("set GOREBUILD_PROMETHEUS_CACHE for external real-replay fixture")
	}
	content, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Cache CacheDiagnostic `json:"cache_diagnostic"`
	}
	if err := json.Unmarshal(content, &result); err != nil {
		t.Fatal(err)
	}
	cache := result.Cache
	if !completeCacheBlock(cache.WarmLog, "build "+cache.Package) || !completeCacheBlock(cache.ChildLog, "build "+cache.Package) {
		t.Fatal("incomplete selected real hash evidence")
	}
	if changes := compareCacheInputs(cache.WarmLog, cache.ChildLog); !reflect.DeepEqual(changes, cache.Changes) {
		t.Fatal("real replay input changes disagree with normalized evidence", changes)
	}
}

func TestRepeatedHashInvocationsAreNotRemovedInputs(t *testing.T) {
	block := "HASH[link example/main]\nHASH[link example/main]: \"go1.27\"\nHASH[link example/main]: \"packagefile p=abc\\n\"\nHASH[link example/main]: " + strings.Repeat("a", 64) + "\n"
	if changes := compareCacheInputs(block+block, block); len(changes) != 0 {
		t.Fatal("repeated warm hash interpreted as input/config change", changes)
	}
	if !completeCacheBlock(block+block, "link example/main") {
		t.Fatal("identical repeated hash is incomplete")
	}
	if completeCacheBlock(block+strings.ReplaceAll(block, strings.Repeat("a", 64), strings.Repeat("b", 64)), "link example/main") {
		t.Fatal("ambiguous invocations called complete")
	}
	if completeCacheBlock(block+"HASH[link example/main]\nHASH[link example/main]: \"partial\"\n", "link example/main") {
		t.Fatal("truncated last invocation called complete")
	}
}

func TestCacheInputChangesAreObservedNotReverseDependencyGuesses(t *testing.T) {
	parent := `HASH[build example/p]: "go1.27\n"
HASH[build example/p]: "file p.go abc\n"
HASH[build example/p]: "file data.json abc\n"
HASH[build example/p]: "import example/dep abc\n"
HASH[build example/p]: "modinfo vcs.modified=false\n"
HASH[build example/p]: 0000
`
	child := strings.ReplaceAll(parent, "abc", "def")
	child = strings.ReplaceAll(child, "vcs.modified=false", "vcs.modified=true")
	changes := compareCacheInputs(parent, child)
	if len(changes) != 4 {
		t.Fatal(changes)
	}
	for _, change := range changes {
		if len(change.Removed) != 1 || len(change.Added) != 1 {
			t.Fatal(change)
		}
	}
	if result := compareCacheInputs(parent, parent); len(result) != 0 {
		t.Fatal("unchanged inputs treated as invalidations", result)
	}
	doubled := parent + "HASH[build example/p]: \"file p.go abc\\n\"\n"
	changes = compareCacheInputs(doubled, parent)
	if len(changes) != 1 || len(changes[0].Removed) != 1 || len(changes[0].Added) != 0 {
		t.Fatal("multiset inputs lost", changes)
	}
	diagnostic := (&CacheDiagnostic{Status: "failed", Complete: false}).Describe(5)
	if !strings.Contains(diagnostic, "Incomplete") || !strings.Contains(diagnostic, "do not prove") {
		t.Fatal("incomplete evidence overclaimed", diagnostic)
	}
}
