package explain

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/konradreiche/gorebuild/internal/history"
)

func TestCandidateGraphRejectsNewCycle(t *testing.T) {
	data, root := suggestFixture(t, "package dep\nimport \"example.org/repo/support\"\ntype Stable = support.Type\n", "package consumer\nimport \"example.org/repo/dep\"\nvar Value dep.Stable\n")
	sourceFile(t, root, "support/support.go", "package support\ntype Type struct{}\n")
	// The typed source is legal. Inject an additional captured graph edge to
	// ensure the hypothetical new destination is checked, not just source SCCs.
	data.Graph = append(data.Graph, history.PackageNode{ImportPath: "example.org/repo/support", Imports: []string{consumerName}})
	report, err := Suggest(context.Background(), data, "dep", root, "", TypeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Candidates) != 1 || report.Candidates[0].Status != "rejected" || !strings.Contains(strings.Join(report.Candidates[0].Reasons, " "), "cycle") {
		t.Fatal("proposed cycle not rejected", report.Candidates)
	}
}

func TestConstantValueAndInterfaceAtomicGroups(t *testing.T) {
	data, root := suggestFixture(t, `package dep
const (A=iota;B)
var X,Y=1,2
type Protocol interface {Call()}
`, "package consumer\nimport \"example.org/repo/dep\"\nfunc F(value dep.Protocol){value.Call();_=dep.A;_=dep.X}\n")
	report, err := Suggest(context.Background(), data, "dep", root, "", TypeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var constant, values, protocol bool
	for _, candidate := range report.Candidates {
		if slices.Contains(candidate.Declarations, "A") && len(candidate.Roots) == 1 {
			constant = slices.Contains(candidate.Declarations, "B")
		}
		if slices.Contains(candidate.Declarations, "X") && len(candidate.Roots) == 1 {
			values = slices.Contains(candidate.Declarations, "Y")
		}
		if slices.Contains(candidate.Declarations, "Protocol") && len(candidate.Roots) == 1 {
			protocol = slices.Contains(candidate.Declarations, "Protocol.Call")
		}
	}
	if !constant || !values || !protocol {
		t.Fatal("illegal partial atom extraction", report.Candidates)
	}
}

func TestImpactWithheldForUnknownOrRemainingInputs(t *testing.T) {
	data, root := suggestFixture(t, "package dep\nfunc Stable() int{return 1}\n", "package consumer\nimport \"example.org/repo/dep\"\nvar Value=dep.Stable()\n")
	data.Events[0].Files = append(data.Events[0].Files, history.FileChange{Path: "dep/asset.bin", Classification: "potential_build"})
	report, err := Suggest(context.Background(), data, "dep", root, "", TypeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Candidates[0].ConditionalEvents != 0 || !strings.Contains(report.Candidates[0].Events[0].Reason, "non-Go") {
		t.Fatal("unknown embedded/native input attributed as savings", report.Candidates)
	}
	data.Events[0].Parents = []string{"unavailable-revision"}
	report, err = Suggest(context.Background(), data, "dep", root, "", TypeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Candidates[0].UnknownEvents != 1 || report.Candidates[0].ConditionalEvents != 0 {
		t.Fatal("missing history treated as stable", report.Candidates)
	}
}
