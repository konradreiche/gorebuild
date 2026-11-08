package explain

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sourceFile(t *testing.T, root, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.org/repo\n\ngo 1.27.3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestTargetedTypesResolveDeclarationsAndConstraints(t *testing.T) {
	report, root := fixture(t)
	sourceFile(t, root, "dep/dep.go", `package dep
//go:generate do-not-execute-generator
var secret = 1
func init(){ secret++ }
func helper() int {return secret}
type Item struct { Public int; hidden int }
func New() Item {return Item{Public:helper()}}
func (item Item) Value() int {return item.hidden+secret}
`)
	sourceFile(t, root, "consumer/consumer.go", `package consumer
import alias "example.org/repo/dep"
func Use(){ value:=alias.New(); _=value.Value(); _=value.Public }
`)
	data, err := Read(saveFixture(t, report, false))
	if err != nil {
		t.Fatal(err)
	}
	out, err := InspectTypes(data, depName, "./consumer", root, 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"Partial: false", "New (function/method)", "Item.Value", "Item.Public", "helper [unexported]", "secret [unexported]", "Initialization/package variables/blank imports", "go:generate/go:linkname", "import cycle", "Cache-only package loading", "not proof of a removable/narrowable dependency"} {
		if !strings.Contains(out, token) {
			t.Errorf("missing typed evidence %q in %s", token, out)
		}
	}
	// Omitted imports must be surfaced, not presented as a full type proof.
	sourceFile(t, root, "dep/extra.go", `package dep
import external "example.org/unavailable"
var _ external.Missing
`)
	out, err = InspectTypes(data, depName, "consumer", root, 20)
	if err != nil || !strings.Contains(out, "Partial: true") || !strings.Contains(out, "Incomplete type checking forbids a removal conclusion") {
		t.Fatal("incomplete types overclaimed", out, err)
	}
}

func TestTypesInspectOnlyMeasuredDirectLocalPairs(t *testing.T) {
	report, root := fixture(t)
	sourceFile(t, root, "dep/dep.go", "package dep\nconst X=1\n")
	sourceFile(t, root, "consumer/consumer.go", "package consumer\nconst Y=2\n")
	data, err := Read(saveFixture(t, report, false))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := InspectTypes(data, depName, "consumer", root, 5); err != nil {
		if !strings.Contains(err.Error(), "does not directly import") {
			t.Fatal(err)
		}
	} else {
		t.Fatal("unrelated current source inspected as a direct dependency")
	}
	for index := range data.Events {
		data.Events[index].Replay = nil
	}
	if _, err := InspectTypes(data, depName, "consumer", root, 5); err != nil {
		return
	}
	t.Fatal("unmeasured relationship accepted for targeted inspection")
}
