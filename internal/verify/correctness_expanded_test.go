package verify

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"
)

func TestCorrectnessOnlyOverlayAndCallerCensus(t *testing.T) {
	source := filepath.Join(t.TempDir(), "supplement_test.go")
	if err := os.WriteFile(source, []byte("package api\nfunc Example_extra(){}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	report := &Report{After: Revision{Commit: "after"}}
	if err := loadTestOverlays(Options{AfterTestOverlays: []string{"api/supplement_test.go=" + source}}, report); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	writeTestFile(t, root, "api/api.go", "package api\n")
	if err := installTestOverlays(&tree{path: root}, &Revision{Commit: "before"}, report.TestOverlays); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "api/supplement_test.go")); !os.IsNotExist(err) {
		t.Fatal("after-only test installed before")
	}
	if err := installTestOverlays(&tree{path: root}, &Revision{Commit: "after"}, report.TestOverlays); err != nil {
		t.Fatal(err)
	}
	if err := loadTestOverlays(Options{AfterTestOverlays: []string{"../escape_test.go=" + source}}, &Report{}); err != nil {
		t.Log(err)
	} else {
		t.Fatal("test overlay escape accepted")
	}
	syntax, err := parser.ParseFile(token.NewFileSet(), "external_test.go", "//go:build custom\n\npackage external\nimport v1 \"example/api\"\nvar _=v1.NewAPI\n", parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	sites := callerSites(syntax, "external/external_test.go", map[string]bool{"example/api": true}, false)
	if len(sites) != 1 || sites[0].InTarget || !sites[0].TestFile || !sites[0].BuildConstraint || len(sites[0].Symbols) != 1 || sites[0].Symbols[0] != "NewAPI" {
		t.Fatal("outside graph/tagged caller not inventoried", sites)
	}
}
