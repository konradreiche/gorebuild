package main

import (
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExplainDetailsDoNotChangeJSONAndGzipEvidence(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "history.json")
	if err := os.WriteFile(input, []byte(`{"schema_version":4,"metadata":{"import_path":"example/main"},"packages":[{"dir":"api","import_path":"example/api"}],"events":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	original := os.Stdout
	terminal, err := os.CreateTemp(dir, "terminal-")
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = terminal
	defer func() { os.Stdout = original; terminal.Close() }()
	plain := filepath.Join(dir, "explain.json")
	compressed := plain + ".gz"
	if err := runExplain([]string{"--history", input, "--package=api", "--out", plain}); err != nil {
		t.Fatal(err)
	}
	if err := runExplain([]string{"--history", input, "--package=api", "--details", "--out", compressed}); err != nil {
		t.Fatal(err)
	}
	expected, err := os.ReadFile(plain)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(compressed)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	reader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	actual, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(expected, actual) {
		t.Fatal("details/compression changed explanation evidence")
	}
	text, err := os.ReadFile(terminal.Name())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(text), "gorebuild explain: api") != 2 || strings.Count(string(text), "Supporting evidence and estimated counterfactual bounds") != 1 {
		t.Fatal("progressive CLI detail missing", string(text))
	}
	if err := runExplain([]string{"--history", input, "--package=api", "--out", plain}); err != nil {
		return
	}
	t.Fatal("existing output overwritten")
}
