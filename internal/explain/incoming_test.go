package explain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/konradreiche/gorebuild/internal/history"
	"github.com/konradreiche/gorebuild/internal/profile"
)

const (
	upstreamName = "example.org/repo/upstream"
	contractName = "example.org/repo/contract"
)

func incomingFixture(t *testing.T, target, consumer string) (*Dataset, string) {
	t.Helper()
	report, root := fixture(t)
	sourceFile(t, root, "contract/contract.go", "package contract\ntype Handler interface{Serve()}\n")
	sourceFile(t, root, "upstream/upstream.go", `package upstream
import "example.org/repo/contract"
type handler struct{}
func(handler) Serve(){}
func New() contract.Handler {return handler{}}
func Value() int {return 1}
`)
	sourceFile(t, root, "dep/dep.go", target)
	sourceFile(t, root, "consumer/consumer.go", consumer)
	git := func(args ...string) string {
		t.Helper()
		out, err := sourceGit(context.Background(), root, args...)
		if err != nil {
			t.Fatal(args, err)
		}
		return strings.TrimSpace(string(out))
	}
	git("init")
	git("config", "user.name", "Test")
	git("config", "user.email", "test@example.org")
	git("add", ".")
	git("commit", "-m", "initial")
	parent := git("rev-parse", "HEAD")
	sourceFile(t, root, "upstream/change.go", "package upstream\nfunc Independent() int{return 2}\n")
	git("add", ".")
	git("commit", "-m", "upstream implementation change")
	head := git("rev-parse", "HEAD")
	report.Metadata.Head = head
	for index := range report.Graph {
		if report.Graph[index].ImportPath == depName {
			report.Graph[index].Imports = []string{upstreamName, contractName}
		}
	}
	report.Graph = append(report.Graph, history.PackageNode{ImportPath: upstreamName, Dir: filepath.Join(root, "upstream"), Imports: []string{contractName}}, history.PackageNode{ImportPath: contractName, Dir: filepath.Join(root, "contract")})
	report.Packages = append(report.Packages, history.PackageImpact{ImportPath: upstreamName, Dir: "upstream"})
	event := report.Events[0]
	event.Commit = head
	event.Parents = []string{parent}
	event.PackageDirs = []string{"upstream"}
	event.BuildPackageDirs = []string{"upstream"}
	event.Files = []history.FileChange{{Path: "upstream/change.go", PackageDir: "upstream", Classification: "go_source"}}
	event.Replay.PackageImports["upstream"] = upstreamName
	var actions []profile.Action
	if err := json.Unmarshal(event.Replay.Rebuild.ActionGraph, &actions); err != nil {
		t.Fatal(err)
	}
	for index := range actions {
		if actions[index].ID == 1 {
			actions[index].Deps = []int{5}
		}
	}
	actions = append(actions, profile.Action{ID: 5, Mode: "build", Package: upstreamName, Cmd: []string{"/tool/compile -p " + upstreamName}})
	raw, err := json.Marshal(actions)
	if err != nil {
		t.Fatal(err)
	}
	event.Replay.Rebuild.ActionGraph = raw
	report.Events = []history.Event{event}
	data, err := Read(saveFixture(t, report, false))
	if err != nil {
		t.Fatal(err)
	}
	return data, root
}

func factoryFixture(t *testing.T) (*Dataset, string) {
	t.Helper()
	return incomingFixture(t, `package dep
import "example.org/repo/upstream"
import "example.org/repo/contract"
type API struct{handler contract.Handler}
func NewAPI(enabled bool)*API {a:=&API{};if enabled {a.handler=upstream.New()};return a}
`, "package consumer\nimport \"example.org/repo/dep\"\nvar API=dep.NewAPI(true)\n")
}

func incomingByName(t *testing.T, report *Suggestions, name string) *IncomingDependency {
	t.Helper()
	for index := range report.Incoming {
		if report.Incoming[index].Import == name {
			return &report.Incoming[index]
		}
	}
	t.Fatalf("incoming import %s absent", name)
	return nil
}

func TestIncomingFactoryInversionHasTypedNeutralBoundaryAndCompetingPath(t *testing.T) {
	data, root := factoryFixture(t)
	before, err := sourceGit(context.Background(), root, "status", "--porcelain")
	if err != nil {
		t.Fatal(err)
	}
	report, err := Suggest(context.Background(), data, "dep", root, "", TypeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	dependency := incomingByName(t, report, upstreamName)
	if dependency.WithoutTargetSource != 1 || dependency.OnCriticalPath != 1 || dependency.DirectChangeEvents != 1 {
		t.Fatal(dependency)
	}
	if len(dependency.Requirements) != 1 || dependency.Requirements[0].Declaration != "NewAPI" || !slices.Contains(dependency.Requirements[0].Symbols, "New") {
		t.Fatal("typed import requirements wrong", dependency.Requirements)
	}
	extraction := dependency.Boundaries[0]
	inversion := dependency.Boundaries[1]
	if extraction.Status != "rejected" {
		t.Fatal("type-family extraction sold as inversion", extraction)
	}
	if !inversion.StructurallySupported || inversion.Status != "requires investigation" || len(inversion.RemovedEdges) != 1 || len(inversion.AddedEdges) != 1 || inversion.AddedEdges[0].From != consumerName {
		t.Fatal("inversion not structurally supported", inversion)
	}
	if len(inversion.FactoryCalls) != 1 || inversion.FactoryCalls[0].ResultType != "example.org/repo/contract.Handler" || inversion.DeclarationUnchanged != 1 {
		t.Fatal(inversion)
	}
	if inversion.ConditionalEvents != 1 || inversion.AffectedActions[0].MaximumSavingNS != int64(5*time.Second) {
		t.Fatal("competing path ignored", inversion.AffectedActions)
	}
	after, err := sourceGit(context.Background(), root, "status", "--porcelain")
	if err != nil || string(after) != string(before) {
		t.Fatal("incoming analysis modified source", err)
	}
	if report.Complexity == nil || !strings.Contains(report.Complexity.Status, "profiling required") {
		t.Fatal("specific compiler optimization inferred")
	}
}

func TestIncomingDirectExtractionMustRemoveTransitiveExposure(t *testing.T) {
	data, root := incomingFixture(t, "package dep\nimport \"example.org/repo/upstream\"\nfunc Volatile()int{return upstream.Value()}\nfunc Stable()int{return 7}\n", "package consumer\nimport \"example.org/repo/dep\"\nvar V=dep.Volatile()+dep.Stable()\n")
	report, err := Suggest(context.Background(), data, "dep", root, "", TypeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	extraction := incomingByName(t, report, upstreamName).Boundaries[0]
	if !extraction.StructurallySupported || len(extraction.RemovedEdges) != 1 || !slices.Equal(extraction.Declarations, []string{"Volatile"}) {
		t.Fatal(extraction)
	}
	// A still-retained target wrapper would defeat isolation.
	sourceFile(t, root, "dep/wrapper.go", "package dep\nfunc Wrapper()int{return Volatile()}\n")
	report, err = Suggest(context.Background(), data, "dep", root, "", TypeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	extraction = incomingByName(t, report, upstreamName).Boundaries[0]
	if extraction.Status != "rejected" || len(extraction.RemainingDeclarations) == 0 {
		t.Fatal("forwarding dependency hidden", extraction)
	}
}

func TestIncomingInversionRejectsRetainedRoutesAndPrivateConstructionInputs(t *testing.T) {
	data, root := factoryFixture(t)
	for index := range data.Graph {
		if data.Graph[index].ImportPath == contractName {
			data.Graph[index].Imports = []string{upstreamName}
		}
	}
	report, err := Suggest(context.Background(), data, "dep", root, "", TypeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	boundary := incomingByName(t, report, upstreamName).Boundaries[1]
	if boundary.Status != "rejected" || !strings.Contains(strings.Join(boundary.Reasons, " "), "still reach") {
		t.Fatal("retained exposure ignored", boundary)
	}
	data, root = factoryFixture(t)
	sourceFile(t, root, "dep/dep.go", `package dep
import "example.org/repo/upstream"
import "example.org/repo/contract"
var privateHandler contract.Handler
func NewAPI(enabled bool) contract.Handler {privateHandler=upstream.New();return privateHandler}
`)
	// The return type is neutral, but state/ownership is explicitly uncertain.
	report, err = Suggest(context.Background(), data, "dep", root, "", TypeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	boundary = incomingByName(t, report, upstreamName).Boundaries[1]
	if boundary.Status == "requires validation" || len(boundary.Constraints) == 0 {
		t.Fatal("state/incomplete migration called safe", boundary)
	}
}

func saveIncomingCache(t *testing.T, data *Dataset, complete, changedFile bool) string {
	t.Helper()
	header := "HASH[build " + depName + "]\n"
	parent := header + "HASH[build " + depName + "]: \"import " + upstreamName + " old\\n\"\n"
	child := header + "HASH[build " + depName + "]: \"import " + upstreamName + " new\\n\"\n"
	if changedFile {
		parent += "HASH[build " + depName + "]: \"file dep.go old\\n\"\n"
		child += "HASH[build " + depName + "]: \"file dep.go new\\n\"\n"
	}
	parent += "HASH[build " + depName + "]: " + strings.Repeat("a", 64) + "\n"
	child += "HASH[build " + depName + "]: " + strings.Repeat("b", 64) + "\n"
	if !complete {
		child = header + "HASH[build " + depName + "]: \"partial\"\n"
	}
	evidence := struct {
		SchemaVersion int              `json:"schema_version"`
		Cache         *CacheDiagnostic `json:"cache_diagnostic"`
	}{1, &CacheDiagnostic{Package: depName, Commit: data.Events[0].Commit, Complete: complete, Status: "measured", WarmLog: parent, ChildLog: child}}
	bytes, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "cache.json")
	if err := os.WriteFile(file, bytes, 0o600); err != nil {
		t.Fatal(err)
	}
	return file
}

func TestIncomingObservedInputsAndDirectSourceNeverBecomeAvoidance(t *testing.T) {
	data, root := factoryFixture(t)
	file := saveIncomingCache(t, data, true, true)
	report, err := Suggest(context.Background(), data, "dep", root, "", TypeOptions{}, file)
	if err != nil {
		t.Fatal(err)
	}
	dependency := incomingByName(t, report, upstreamName)
	if !report.CacheEvidence.Complete || dependency.ObservedInputEvents != 1 || dependency.Boundaries[1].ConditionalEvents != 0 {
		t.Fatal("observed file changes ignored", dependency)
	}
	data.Events[0].Files = append(data.Events[0].Files, history.FileChange{Path: "dep/dep.go", Classification: "go_source", PackageDir: "dep"})
	report, err = Suggest(context.Background(), data, "dep", root, "", TypeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	dependency = incomingByName(t, report, upstreamName)
	if !dependency.Events[0].TargetSourceChanged || dependency.Boundaries[1].ConditionalEvents != 0 || !strings.Contains(dependency.Boundaries[1].AffectedActions[0].Reason, "source changed directly") {
		t.Fatal("own source changes modeled as avoidable", dependency)
	}
}

func TestIncomingIncompleteCacheAndTypeAnalysisWithholdSupport(t *testing.T) {
	data, root := factoryFixture(t)
	report, err := Suggest(context.Background(), data, "dep", root, "", TypeOptions{}, saveIncomingCache(t, data, false, false))
	if err != nil {
		t.Fatal(err)
	}
	dependency := incomingByName(t, report, upstreamName)
	if report.CacheEvidence.Complete || dependency.ObservedInputEvents != 0 || dependency.Boundaries[1].ConditionalEvents != 0 {
		t.Fatal("partial cache evidence trusted", report.CacheEvidence, dependency)
	}
	sourceFile(t, root, "dep/broken.go", "package dep\nvar _=Undefined\n")
	report, err = Suggest(context.Background(), data, "dep", root, "", TypeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, dependency := range report.Incoming {
		for _, boundary := range dependency.Boundaries {
			if boundary.StructurallySupported {
				t.Fatal("partial type checking called structurally supported", boundary)
			}
		}
	}
}
