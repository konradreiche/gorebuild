package profile

import (
	"slices"
	"testing"
	"time"
)

func TestExecutedCriticalPathAndCommandKinds(t *testing.T) {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	action := func(id int, mode, pkg string, elapsed, tool time.Duration, commands []string, deps ...int) Action {
		return Action{ID: id, Mode: mode, Package: pkg, TimeStart: start, TimeDone: start.Add(elapsed), CmdReal: int64(tool), Cmd: commands, Deps: deps}
	}
	cache := action(4, "build", "cached", 100*time.Second, 0, nil)
	cache.NeedBuild = true
	actions := []Action{
		action(0, "link", "app", 2*time.Second, time.Second, []string{"/tool/link -o app"}, 1, 3, 4, 6),
		action(1, "build", "a", 4*time.Second, 3*time.Second, []string{"/tool/compile -p a"}, 7),
		action(2, "build", "leaf", 5*time.Second, 4*time.Second, []string{"/tool/compile -p leaf"}),
		action(3, "build", "other", 6*time.Second, 5*time.Second, []string{"/tool/compile -p other"}), cache,
		action(5, "cgo run", "native", 3*time.Second, 2*time.Second, []string{"/tool/cgo native.go"}),
		action(6, "build", "native", time.Second, 500*time.Millisecond, []string{"\"/tool path/asm\" -gensymabis native.s", "\"/tool path/asm\" -o native.o native.s"}, 5),
		action(7, "build", "cached-consumer", 90*time.Second, 0, nil, 2),
	}
	graph, err := Analyze(actions)
	if err != nil {
		t.Fatal(err)
	}
	if graph.ExecutedActions != 6 || graph.ExecutedBuildActions != 4 || graph.ExecutedCgoActions != 1 || graph.ExecutedAssemblyActions != 1 || graph.ExecutedLinkActions != 1 {
		t.Fatalf("cache check counted as execution or overlapping kinds lost: %+v", graph)
	}
	if graph.CompileCommands != 3 || graph.CgoCommands != 1 || graph.AssemblyCommands != 2 || graph.LinkCommands != 1 {
		t.Fatal("command counts do not reflect actual recorded tool invocations", graph.ExecutedActionDetails)
	}
	if graph.CompilationToolTimeNS != int64(14500*time.Millisecond) || graph.LinkToolTimeNS != int64(time.Second) || graph.ExecutedActionChainNS != int64(11*time.Second) || graph.LongestActionChainNS != int64(102*time.Second) {
		t.Fatalf("elapsed, tool work, cache and executed-only paths conflated: %+v", graph)
	}
	var path []string
	var bridge bool
	for _, step := range graph.ExecutedActionChain {
		if step.ID == 7 && step.ElapsedNS == 0 {
			bridge = true
		}
		if step.ElapsedNS > 0 {
			path = append(path, step.Package)
		}
	}
	if !bridge || !slices.Equal(path, []string{"leaf", "a", "app"}) {
		t.Fatal("critical path must follow dependencies, not sum slowest nodes", path)
	}
	for _, detail := range graph.ExecutedActionDetails {
		if detail.Package == "cached" {
			t.Fatal("cache check in executed evidence")
		}
		if detail.Package == "a" && (detail.ElapsedNS != int64(4*time.Second) || detail.ToolTimeNS != int64(3*time.Second)) {
			t.Fatal("lost time distinction")
		}
	}
}
