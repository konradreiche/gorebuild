package explain

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/konradreiche/gorebuild/internal/history"
	"github.com/konradreiche/gorebuild/internal/profile"
)

const (
	depName      = "example.org/repo/dep"
	consumerName = "example.org/repo/consumer"
)

func fixture(t *testing.T) (history.Report, string) {
	t.Helper()
	root := t.TempDir()
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	action := func(id int, mode, pkg string, elapsed, tool int, deps ...int) profile.Action {
		command := "/tool/compile -p " + pkg
		if mode == "link" {
			command = "/tool/link -o app"
		}
		return profile.Action{ID: id, Mode: mode, Package: pkg, Deps: deps, Cmd: []string{command}, CmdReal: int64(time.Duration(tool) * time.Second), TimeStart: start, TimeDone: start.Add(time.Duration(elapsed) * time.Second)}
	}
	actions := []profile.Action{action(0, "link", "example.org/repo/app", 5, 4, 2, 3, 4), action(1, "build", depName, 40, 30), action(2, "build", consumerName, 35, 30, 1), action(3, "build", "example.org/repo/other", 70, 50), {ID: 4, Mode: "build", Package: "cached", NeedBuild: true, TimeStart: start, TimeDone: start.Add(100 * time.Second)}}
	graph, err := profile.Analyze(actions)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(actions)
	if err != nil {
		t.Fatal(err)
	}
	graph.ExecutedActionChainNS = 999 // Readers must reanalyze actual evidence.
	capture := func() *history.ReplayResult {
		return &history.ReplayResult{Status: "measured", PackageImports: map[string]string{"dep": depName, "other": "example.org/repo/other"}, Warm: &profile.BuildCapture{Run: profile.BuildRun{WallTimeNS: int64(time.Second)}}, Rebuild: &profile.BuildCapture{Run: profile.BuildRun{WallTimeNS: int64(90 * time.Second), Graph: graph}, ActionGraph: raw}}
	}
	report := history.Report{SchemaVersion: 4, Metadata: history.Metadata{Repository: root, ImportPath: "example.org/repo/app", Head: "capture-head", GOOS: "linux", GOARCH: "amd64"}, Graph: []history.PackageNode{
		{ImportPath: depName, Dir: filepath.Join(root, "dep")},
		{ImportPath: consumerName, Dir: filepath.Join(root, "consumer"), Imports: []string{depName}},
		{ImportPath: "example.org/repo/other", Dir: filepath.Join(root, "other")},
	}, Packages: []history.PackageImpact{{Dir: "dep", ImportPath: depName, Changes: 1, BuildChanges: 1}, {Dir: "consumer", ImportPath: consumerName}, {Dir: "other", ImportPath: "example.org/repo/other", Changes: 1, BuildChanges: 1}}, Events: []history.Event{
		{Commit: "abcd1111", Parents: []string{"parent"}, PackageDirs: []string{"dep"}, BuildPackageDirs: []string{"dep"}, Files: []history.FileChange{{Path: "dep/dep.go", Classification: "go_source"}}, Replay: capture()},
		{Commit: "abcd2222", Parents: []string{"parent2"}, PackageDirs: []string{"other"}, BuildPackageDirs: []string{"other"}, Files: []history.FileChange{{Path: "other/other.go", Classification: "go_source"}}, Replay: capture()},
		{Commit: "eeee3333", Replay: &history.ReplayResult{Status: "excluded", Stage: "configuration", Reason: "relevant go.mod changed"}},
		{Commit: "ffff4444", Files: []history.FileChange{{Path: "README.md"}}},
	}}
	return report, root
}

func saveFixture(t *testing.T, report history.Report, compressed bool) string {
	t.Helper()
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	name := "history.json"
	if compressed {
		var buffer bytes.Buffer
		writer := gzip.NewWriter(&buffer)
		if _, err := writer.Write(data); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		data = buffer.Bytes()
		name += ".gz"
	}
	file := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(file, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return file
}

func TestReadExplainAndGzipWithoutBuilds(t *testing.T) {
	report, _ := fixture(t)
	t.Setenv("PATH", t.TempDir()) // No Go, Git, compilers, or helpers can execute.
	for _, compressed := range []bool{false, true} {
		file := saveFixture(t, report, compressed)
		before, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		data, err := Read(file)
		if err != nil {
			t.Fatal(err)
		}
		if data.Events[0].Replay.Graph.ExecutedActionChainNS != int64(80*time.Second) {
			t.Fatal("trusted stale stored critical path instead of raw DAG")
		}
		out, err := Describe(data, Options{Commit: "abcd1", Top: 5})
		if err != nil {
			t.Fatal(err)
		}
		for _, token := range []string{"Warm no-op baseline: 1.000s", "90.000s", "80.000s", "75.000s", "5.000s", "consumer", "Optimistic theoretical"} {
			if !strings.Contains(out, token) {
				t.Errorf("missing %q in event explanation", token)
			}
		}
		if strings.Contains(out, "cached [#4]") || !strings.Contains(out, "not predicted wall-clock savings") || !strings.Contains(out, "recompiled in 1 other successful samples") {
			t.Fatal("cache execution, recurrence or modeled bound mislabeled", out)
		}
		out, err = Describe(data, Options{Package: "./consumer", Top: 5})
		if err != nil {
			t.Fatal(err)
		}
		for _, token := range []string{"2/2 successful events", "30.000s", "35.000s", "dep -> consumer", "overlapping event counts", "not whole-build latency"} {
			if !strings.Contains(out, token) {
				t.Errorf("missing %q in package explanation", token)
			}
		}
		if !strings.Contains(out, "30.000s") || strings.Contains(out, "Typical recorded package build-node work containing Go compilation: 90.000s") {
			t.Fatal("package work conflated with overlapping event wall times")
		}
		after, err := os.ReadFile(file)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatal("explain modified the input artifact", err)
		}
		if _, err := Describe(data, Options{Commit: "abcd"}); err != nil {
			continue
		}
		t.Fatal("ambiguous prefix accepted")
	}
}

func TestFailureMissingAndCorruptEvidence(t *testing.T) {
	report, _ := fixture(t)
	report.Events[1].Replay.Rebuild = &profile.BuildCapture{ActionGraph: json.RawMessage(`[{"ID":1,"Deps":[99]}]`)}
	data, err := Read(saveFixture(t, report, false))
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Warnings) != 1 {
		t.Fatal("bad graph not reported")
	}
	if _, err := Describe(data, Options{Commit: "abcd2"}); err != nil {
		if !strings.Contains(err.Error(), "corrupt/incomplete") {
			t.Fatal(err)
		}
	} else {
		t.Fatal("bad measured graph treated as zero cost")
	}
	out, err := Describe(data, Options{Commit: "eeee"})
	if err != nil || !strings.Contains(out, "excluded at configuration") || !strings.Contains(out, "not a zero-cost event") {
		t.Fatal(out, err)
	}
	out, err = Describe(data, Options{Commit: "ffff"})
	if err != nil || !strings.Contains(out, "Not replayed") {
		t.Fatal(out, err)
	}
	for _, options := range []Options{{}, {Commit: "abcd1", Package: "dep"}, {Commit: "missing"}, {Package: "missing"}} {
		if _, err := Describe(data, options); err != nil {
			continue
		}
		t.Fatal("invalid selector accepted", options)
	}
}

func TestLegacyGraphFallbackAndPairedVCS(t *testing.T) {
	report, _ := fixture(t)
	report.SchemaVersion = 3
	original := report.Events[0].Replay.Rebuild
	// Legacy captures can omit raw JSON but retain the complete typed graph.
	original.ActionGraph = nil
	cached := []profile.Action{{ID: 0, Mode: "build", NeedBuild: true, TimeStart: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), TimeDone: time.Date(2025, 1, 1, 0, 0, 100, 0, time.UTC)}}
	raw, err := json.Marshal(cached)
	if err != nil {
		t.Fatal(err)
	}
	report.Events[0].NoVCSReplay = &history.ReplayResult{Status: "measured", BuildFlags: []string{"-buildvcs=false"}, Rebuild: &profile.BuildCapture{Run: profile.BuildRun{WallTimeNS: int64(time.Second)}, ActionGraph: raw}}
	data, err := Read(saveFixture(t, report, false))
	if err != nil {
		t.Fatal(err)
	}
	out, err := Describe(data, Options{Commit: "abcd1"})
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"80.000s", "Matched no-VCS observation", "0 build / 0 link", "not proof of VCS-only cost"} {
		if !strings.Contains(out, token) {
			t.Errorf("missing %q", token)
		}
	}
}

func TestRejectMalformedArtifactsAndBadGzip(t *testing.T) {
	for _, text := range []string{`[]`, `{"schema_version":99,"metadata":{"import_path":"app"},"events":[]}`, `{"schema_version":4,"events":[]}`, `{"schema_version":4,"metadata":{"import_path":"app"},"events":[]} {}`, `{"schema_version":4,"metadata":{"import_path":"app"},"events":[],"events":[]}`, `{"schema_version":4,"metadata":{"import_path":"app"},"events":[{"commit":"a"},{"commit":"a"}]}`} {
		file := filepath.Join(t.TempDir(), "bad.json")
		if err := os.WriteFile(file, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Read(file); err != nil {
			continue
		}
		t.Fatal("malformed history accepted", text)
	}
	report, _ := fixture(t)
	file := saveFixture(t, report, true)
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-1] ^= 0xff
	if err := os.WriteFile(file, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(file); err != nil {
		return
	}
	t.Fatal("gzip checksum corruption ignored")
}
