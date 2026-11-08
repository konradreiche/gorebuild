package explain

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/konradreiche/gorebuild/internal/profile"
)

type packageObservation struct {
	event     *Event
	actions   []profile.ExecutedAction
	toolNS    int64
	elapsedNS int64
	pathNS    int64
}

func describePackage(data *Dataset, pkg string, top int) string {
	var out strings.Builder
	fmt.Fprintf(&out, "Package: %s (%s)\nTarget: %s\n", data.label(pkg), pkg, data.Metadata.ImportPath)
	for _, entry := range data.Packages {
		if entry.ImportPath == pkg {
			fmt.Fprintf(&out, "Historical activity: %d attributed changes, %d potential build events (not developer build invocations)\n", entry.Changes, entry.BuildChanges)
		}
	}
	var selected, successful, failed, excluded, unusable, direct, pathEvents, buildEvents int
	var observations []packageObservation
	changedCounts := make(map[string]int)
	routes := make(map[string]int)
	var tool, elapsed, pathTimes []int64
	for index := range data.Events {
		event := &data.Events[index]
		if event.Replay == nil {
			continue
		}
		selected++
		replay := event.Replay
		if replay.Status == "failed" {
			failed++
			continue
		}
		if replay.Status == "excluded" {
			excluded++
			continue
		}
		if !replay.Measured() {
			unusable++
			continue
		}
		successful++
		if len(buildActions(replay, pkg)) > 0 {
			buildEvents++
		}
		if !recompiled(replay, pkg) {
			continue
		}
		observation := packageObservation{event: event}
		for _, action := range buildActions(replay, pkg) {
			if action.CompileCommands == 0 {
				continue
			}
			observation.actions = append(observation.actions, action)
			observation.toolNS += action.ToolTimeNS
			observation.elapsedNS += action.ElapsedNS
			if onPath(replay.Graph, action.ID) {
				observation.pathNS += action.ElapsedNS
			}
		}
		tool = append(tool, observation.toolNS)
		elapsed = append(elapsed, observation.elapsedNS)
		pathTimes = append(pathTimes, observation.pathNS)
		if observation.pathNS > 0 {
			pathEvents++
		}
		observations = append(observations, observation)
		changed := data.changedImports(event)
		if slices.Contains(changed, pkg) {
			direct++
		}
		for _, dir := range event.BuildPackageDirs {
			changedCounts[dir]++
		}
		seen := make(map[string]bool)
		for _, source := range changed {
			if source == pkg {
				continue
			}
			route := dependencyPath(replay.Graph, source, pkg)
			if len(route) < 2 {
				continue
			}
			for index := range route {
				route[index] = data.label(route[index])
			}
			key := strings.Join(route, " -> ")
			if !seen[key] {
				routes[key]++
				seen[key] = true
			}
		}
	}
	fmt.Fprintf(&out, "\nSample coverage: %d selected; %d usable successful rebuilds; %d failed, %d excluded, %d unusable.\n", selected, successful, failed, excluded, unusable)
	fmt.Fprintf(&out, "Measured Go recompilation: %d/%d successful events; executed package build nodes: %d/%d.\n", len(observations), successful, buildEvents, successful)
	fmt.Fprintf(&out, "Of its recompilation events, %d changed this package directly; the rest are downstream/co-changing/configuration observations, not proof of a particular cause.\n", direct)
	fmt.Fprintf(&out, "Typical recorded package build-node work containing Go compilation: %s\nTypical action elapsed sum per event: %s\n", typical(tool), typical(elapsed))
	fmt.Fprintf(&out, "On the selected estimated critical path: %d/%d recompilation events; typical elapsed contribution %s (off-path events contribute zero).\n", pathEvents, len(observations), typical(pathTimes))
	fmt.Fprintln(&out, "These are compilation-node measurements, not whole-build latency or potential savings; ties may select another equally long path.")
	fmt.Fprintln(&out, "\nChanged packages co-occurring with recompilation (overlapping event counts, not causal attribution):")
	printCounts(&out, changedCounts, top)
	fmt.Fprintln(&out, "\nCommon upstream build-dependency routes (inferred from historical action DAGs, not symbol-use proof):")
	printCounts(&out, routes, top)
	sort.SliceStable(observations, func(i, j int) bool { return observations[i].elapsedNS > observations[j].elapsedNS })
	fmt.Fprintln(&out, "\nRepresentative expensive compilation observations / zero-duration path models:")
	fmt.Fprintf(&out, "  %-12s %9s %9s %9s %10s %10s\n", "Commit", "Pkg tool", "Pkg elapsed", "Path", "Without", "Max saving")
	for _, observation := range observations[:min(top, len(observations))] {
		actions := slices.Clone(observation.actions)
		sort.SliceStable(actions, func(i, j int) bool { return actions[i].ElapsedNS > actions[j].ElapsedNS })
		change, err := profile.EstimateCriticalPathSaving(observation.event.Replay.Graph.ActionGraph, actions[0].ID)
		if err != nil {
			fmt.Fprintf(&out, "  %s: model unavailable: %s\n", shortCommit(observation.event.Commit), err)
			continue
		}
		fmt.Fprintf(&out, "  %-12s %9s %9s %9s %10s %10s\n", shortCommit(observation.event.Commit), seconds(observation.toolNS), seconds(observation.elapsedNS), seconds(change.CurrentNS), seconds(change.WithoutNS), seconds(change.MaximumSavingNS))
		if len(actions) > 1 {
			fmt.Fprintf(&out, "    Model zeroes only action #%d, not all package actions.\n", actions[0].ID)
		}
	}
	if len(observations) == 0 {
		fmt.Fprintln(&out, "  (no recognized Go compiler commands for this package; no compilation-cost conclusion is available)")
	}
	fmt.Fprintln(&out, "Max saving is an optimistic theoretical bound on the weighted DAG only, including competing paths; not predicted achievable wall-clock savings.")
	formatLimits(&out, data)
	return out.String()
}

func printCounts(out *strings.Builder, counts map[string]int, top int) {
	var keys []string
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if counts[keys[i]] != counts[keys[j]] {
			return counts[keys[i]] > counts[keys[j]]
		}
		return keys[i] < keys[j]
	})
	for _, key := range keys[:min(top, len(keys))] {
		fmt.Fprintf(out, "  %3d events  %s\n", counts[key], key)
	}
	if len(keys) == 0 {
		fmt.Fprintln(out, "  (none established)")
	}
}
