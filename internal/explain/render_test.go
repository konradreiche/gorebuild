package explain

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/konradreiche/gorebuild/internal/history"
	"github.com/konradreiche/gorebuild/internal/presentation"
)

func TestSuggestionSummaryPrioritizesSupportedBoundary(t *testing.T) {
	data := &Dataset{Packages: []history.PackageImpact{{ImportPath: "example/api", Dir: "web/api/v1"}, {ImportPath: "example/remote", Dir: "storage/remote"}, {ImportPath: "example/web", Dir: "web"}}}
	report := &Suggestions{Provider: "example/api", Incoming: []IncomingDependency{{Import: "example/remote", DirectChangeEvents: 24, TargetRecompiled: 10, WithoutTargetSource: 9, Boundaries: []IncomingBoundary{{Strategy: "inject neutral factory results at composition root", StructurallySupported: true, Status: "requires investigation", Constraints: []string{"preserve handler initialization order"}, RemovedEdges: []DependencyEdge{{From: "example/api", To: "example/remote"}}, AddedEdges: []DependencyEdge{{From: "example/web", To: "example/remote"}}}}}}}
	for range 12 {
		report.Candidates = append(report.Candidates, Candidate{Status: "rejected", Roots: []string{"large closure"}, Reasons: []string{"still retains provider dependency"}})
	}
	before, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, width := range []int{80, 120} {
		text := report.Render(data, presentation.Options{Width: width})
		for _, want := range []string{"Structurally supported candidate", "web/api/v1 -> storage/remote", "web -> storage/remote", "Outgoing groups: 12", "10 relevant target recompilations", "latency benefit unverified"} {
			if !strings.Contains(text, want) {
				t.Fatal(want, text)
			}
		}
		if strings.Contains(text, "still retains provider dependency") || strings.Contains(text, "large closure") {
			t.Fatal("rejection details leaked", text)
		}
		detailed := report.Render(data, presentation.Options{Width: width, Details: true})
		if !strings.Contains(detailed, "still retains provider dependency") {
			t.Fatal("reasons missing", detailed)
		}
		assertTerminalWidth(t, text, width)
		assertTerminalWidth(t, detailed, width)
	}
	after, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("suggestion rendering mutated evidence")
	}
}
func TestExplainEvidenceLevelsAndSparseMeasurements(t *testing.T) {
	data := &Dataset{Packages: []history.PackageImpact{{ImportPath: "example/api", Dir: "api"}}}
	inspection := &Inspection{Provider: "example/remote", Consumer: "example/api", Complete: false, Declarations: []DeclarationUse{{Name: "NewHandler", References: 3}}, Text: "FULL DECLARATION GRAPH\n"}
	for _, width := range []int{80, 120} {
		text, err := Render(data, Options{Package: "api"}, inspection, nil, presentation.Options{Width: width})
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"gorebuild explain: api", "No usable executed-action", "Observed cache inputs", "Not captured here", "Resolved typed-source", "Incomplete typed evidence", "models, not measured"} {
			if !strings.Contains(text, want) {
				t.Fatal(want, text)
			}
		}
		if strings.Contains(text, "FULL DECLARATION GRAPH") {
			t.Fatal("default dumped graph")
		}
		assertTerminalWidth(t, text, width)
		detailed, err := Render(data, Options{Package: "api"}, inspection, nil, presentation.Options{Width: width, Details: true})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(detailed, "FULL DECLARATION GRAPH") {
			t.Fatal("details lost evidence")
		}
		assertTerminalWidth(t, detailed, width)
	}
}
func TestCacheInputRenderingPrioritizesSelectedPackage(t *testing.T) {
	data := &Dataset{Packages: []history.PackageImpact{{ImportPath: "example/api", Dir: "api"}}}
	cache := &CacheDiagnostic{Package: "example/api", Complete: true, Status: "measured", Changes: []CacheInputChange{{Block: "build example/main", Category: "main source inputs", Added: []string{"file main.go MAIN_HASH"}}, {Block: "build example/api", Category: "Go source inputs", Added: []string{"file json_codec.go FULL_HASH"}}}}
	text, err := Render(data, Options{Package: "api"}, nil, cache, presentation.Options{Top: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "build api: Go source inputs") || strings.Contains(text, "main source inputs") || strings.Contains(text, "FULL_HASH") {
		t.Fatal("cache blocks conflated or detailed inputs leaked", text)
	}
	text, err = Render(data, Options{Package: "api"}, nil, cache, presentation.Options{Top: 1, Details: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "Added input: file json_codec.go FULL_HASH") || cache.Changes[0].Block != "build example/main" {
		t.Fatal("detailed input missing or evidence mutated", text)
	}
}

func assertTerminalWidth(t *testing.T, text string, width int) {
	t.Helper()
	for _, line := range strings.Split(text, "\n") {
		if utf8.RuneCountInString(line) > width {
			t.Fatalf("%d-column output exceeded: %s", width, line)
		}
	}
}
