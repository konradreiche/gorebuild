package profile

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/konradreiche/gorebuild/internal/presentation"
)

func TestProfileRenderedSharesWidthsAndPreservedGraph(t *testing.T) {
	start := time.Now()
	actions := []Action{{ID: 0, Mode: "root", Deps: []int{1, 3}}, {ID: 1, Mode: "build", Package: "on-path", Deps: []int{2}, Cmd: []string{"compile"}, TimeStart: start, TimeDone: start.Add(8 * time.Second)}, {ID: 2, Mode: "build", Package: "leaf", Cmd: []string{"compile"}, TimeStart: start, TimeDone: start.Add(time.Second)}, {ID: 3, Mode: "build", Package: "off-path", Cmd: []string{"compile"}, TimeStart: start, TimeDone: start.Add(7 * time.Second)}}
	graph, err := Analyze(actions)
	if err != nil {
		t.Fatal(err)
	}
	report := Report{Cold: BuildRun{Graph: graph, WallTimeNS: int64(10 * time.Second)}}
	before, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, width := range []int{80, 120} {
		text := Render(report, presentation.Options{Width: width, Top: 1}, false)
		for _, want := range []string{"Estimated cold critical path", "88.9%", "off-path", "10.000s", "not achievable wall improvement"} {
			if !strings.Contains(text, want) {
				t.Fatal(want, text)
			}
		}
		for _, line := range strings.Split(text, "\n") {
			if utf8.RuneCountInString(line) > width {
				t.Fatal("line too long", width, line)
			}
		}
		detailed := Render(report, presentation.Options{Width: width, Details: true}, true)
		if !strings.Contains(detailed, "leaf to root") || !strings.Contains(detailed, "Supporting evidence") {
			t.Fatal("details/order missing", detailed)
		}
	}
	after, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("render mutated graph")
	}
}
func TestProfileGzipPreservesCompleteEvidence(t *testing.T) {
	capture := ProfileFile{Report: Report{Cold: BuildRun{WallTimeNS: 1234}}}
	dir := t.TempDir()
	plain := filepath.Join(dir, "profile.json")
	compressed := plain + ".gz"
	for _, file := range []string{plain, compressed} {
		if err := writeProfileFile(file, capture); err != nil {
			t.Fatal(err)
		}
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
		t.Fatal("gzip changed JSON evidence")
	}
	if err := writeProfileFile(compressed, capture); err != nil {
		return
	}
	t.Fatal("overwrote existing gzip")
}
