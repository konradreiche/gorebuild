package verify

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"strings"
)

type TestOverlay struct {
	Path     string `json:"path"`
	Content  string `json:"content"`
	SHA256   string `json:"sha256"`
	Revision string `json:"revision"`
}

func loadTestOverlays(options Options, report *Report) error {
	for _, mapping := range options.AfterTestOverlays {
		target, file, ok := strings.Cut(mapping, "=")
		target = path.Clean(target)
		if !ok || !strings.HasSuffix(target, "_test.go") || path.IsAbs(target) || target == ".." || strings.HasPrefix(target, "../") {
			return fmt.Errorf("--after-test-overlay requires repository-relative new _test.go path=source file")
		}
		content, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		if _, err := parser.ParseFile(token.NewFileSet(), target, content, parser.AllErrors); err != nil {
			return err
		}
		report.TestOverlays = append(report.TestOverlays, TestOverlay{Path: target, Content: string(content), SHA256: digest(string(content)), Revision: report.After.Commit})
	}
	return nil
}

func installTestOverlays(work *tree, revision *Revision, overlays []TestOverlay) error {
	for _, overlay := range overlays {
		if revision.Commit != overlay.Revision {
			continue
		}
		target := filepath.Join(work.path, filepath.FromSlash(overlay.Path))
		parent, err := filepath.EvalSymlinks(filepath.Dir(target))
		if err != nil {
			return err
		}
		if !inside(work.path, parent) {
			return fmt.Errorf("test overlay escapes isolated worktree")
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		if _, err := output.WriteString(overlay.Content); err != nil {
			output.Close()
			return err
		}
		if err := output.Close(); err != nil {
			return err
		}
	}
	return nil
}
