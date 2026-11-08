package history

import (
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSingleCompressedHistoryArtifact(t *testing.T) {
	file := filepath.Join(t.TempDir(), "history.json.gz")
	report := Report{SchemaVersion: 4, Metadata: Metadata{ImportPath: "example.org/app"}, Events: []Event{{Commit: "abc", Files: []FileChange{{Path: "pkg/file.go"}}}}}
	if err := writeReport(file, report); err != nil {
		t.Fatal(err)
	}
	input, err := os.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	compressed, err := gzip.NewReader(input)
	if err != nil {
		t.Fatal(err)
	}
	defer compressed.Close()
	var decoded Report
	if err := json.NewDecoder(compressed).Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.SchemaVersion != 4 || decoded.Events[0].Files[0].Path != "pkg/file.go" {
		t.Fatal("compressed artifact lost evidence")
	}
	entries, err := os.ReadDir(filepath.Dir(file))
	if err != nil || len(entries) != 1 {
		t.Fatal("compression created sidecars", entries, err)
	}
	if err := writeReport(file, report); err != nil {
		return
	}
	t.Fatal("compressed capture overwritten")
}
