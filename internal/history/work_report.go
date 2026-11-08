package history

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/konradreiche/gorebuild/internal/profile"
)

func formatActualWork(output *strings.Builder, report Report, top int) {
	work := report.Replay.Work
	if work == nil {
		return
	}
	fmt.Fprintf(output, "\nHistorical rebuild analysis (%d successful events):\n", work.Samples)
	fmt.Fprintf(output, "  Median event wall: %s; warmed no-op baseline: %s\n", measuredDuration(work.MedianWallTimeNS), measuredDuration(work.MedianWarmWallTimeNS))
	fmt.Fprintf(output, "  Median compilation work: %s; linking work: %s; executed critical path: %s\n", measuredDuration(work.MedianCompilationToolNS), measuredDuration(work.MedianLinkToolNS), measuredDuration(work.MedianCriticalPathNS))
	fmt.Fprintln(output, "Most frequently rebuilt packages (executed build nodes, not changed-package counts):")
	fmt.Fprintf(output, "  %-44s %7s %11s %18s\n", "Package", "Replays", "Go compiled", "Median action work")
	for _, pkg := range work.RebuiltPackages[:min(top, len(work.RebuiltPackages))] {
		fmt.Fprintf(output, "  %-44s %7d %11d %18s\n", pkg.Package, pkg.Replays, pkg.RecompiledReplays, measuredDuration(pkg.MedianToolTimeNS))
	}
	if len(work.RebuiltPackages) == 0 {
		fmt.Fprintln(output, "  (none)")
	}
	var events []Event
	for _, event := range report.Events {
		if successfulReplay(event.Replay) {
			events = append(events, event)
		}
	}
	sort.SliceStable(events, func(i, j int) bool {
		return events[i].Replay.Rebuild.Run.WallTimeNS > events[j].Replay.Rebuild.Run.WallTimeNS
	})
	fmt.Fprintln(output, "Most expensive replayed events (not independent package timings):")
	fmt.Fprintf(output, "  %-12s %9s %9s %12s %10s %11s\n", "Commit", "Wall", "Warm", "Compile work", "Link work", "Path (est.)")
	for _, event := range events[:min(top, 5, len(events))] {
		run := event.Replay.Rebuild.Run
		var warm *int64
		if event.Replay.Warm != nil {
			warm = &event.Replay.Warm.Run.WallTimeNS
		}
		fmt.Fprintf(output, "  %-12s %9s %9s %12s %10s %11s\n", shortCommit(event.Commit), measuredDuration(&run.WallTimeNS), measuredDuration(warm), measuredDuration(&run.Graph.CompilationToolTimeNS), measuredDuration(&run.Graph.LinkToolTimeNS), measuredDuration(&run.Graph.ExecutedActionChainNS))
	}
	if len(events) == 0 {
		fmt.Fprintln(output, "  (none)")
	}
	fmt.Fprintln(output, "Action work is recorded command time, not package build latency. Medians need 2+ replays; '-' is unavailable/sparse.")
}

func formatCriticalPath(output *strings.Builder, graph profile.GraphSummary, top int) {
	fmt.Fprintf(output, "    Critical path (estimated; executed action elapsed time): %.3fs\n", time.Duration(graph.ExecutedActionChainNS).Seconds())
	onPath := make(map[int]bool)
	var steps []profile.ChainStep
	for _, step := range graph.ExecutedActionChain {
		onPath[step.ID] = true
		if step.ElapsedNS > 0 {
			steps = append(steps, step)
		}
	}
	sort.SliceStable(steps, func(i, j int) bool { return steps[i].ElapsedNS > steps[j].ElapsedNS })
	fmt.Fprintln(output, "    Largest path contributions (ranked by elapsed time; full dependency order in JSON):")
	for _, step := range steps[:min(top, 5, len(steps))] {
		fmt.Fprintf(output, "      %9.3fs  %s\n", time.Duration(step.ElapsedNS).Seconds(), actionLabel(step.Mode, step.Package))
	}
	if len(steps) == 0 {
		fmt.Fprintln(output, "      (no executed action duration)")
	}
	var other []profile.ExecutedAction
	for _, action := range graph.ExecutedActionDetails {
		if !onPath[action.ID] {
			other = append(other, action)
		}
	}
	sort.SliceStable(other, func(i, j int) bool { return other[i].ToolTimeNS > other[j].ToolTimeNS })
	fmt.Fprintln(output, "    Other executed actions (recorded tool work):")
	for _, action := range other[:min(top, 5, len(other))] {
		fmt.Fprintf(output, "      %9.3fs  %s\n", time.Duration(action.ToolTimeNS).Seconds(), actionLabel(action.Mode, action.Package))
	}
	if len(other) == 0 {
		fmt.Fprintln(output, "      (none)")
	}
}

func actionLabel(mode, pkg string) string {
	if mode == "build" && pkg != "" {
		return pkg
	}
	return strings.TrimSpace(mode + " " + pkg)
}

func shortCommit(commit string) string { return commit[:min(12, len(commit))] }

func formatVCSComparison(output *strings.Builder, report Report) {
	comparison := report.VCSComparison
	if comparison == nil {
		return
	}
	fmt.Fprintf(output, "\nObserved VCS configuration comparison (%d matched successful events):\n", comparison.PairedEvents)
	fmt.Fprintf(output, "  No-VCS outcomes: %d measured, %d failed, %d excluded\n", comparison.NoVCSMeasured, comparison.NoVCSFailed, comparison.NoVCSExcluded)
	fmt.Fprintf(output, "  %-18s %10s %10s %12s %10s %8s %8s\n", "Configuration", "Median wall", "Warm", "Compile work", "Link work", "Builds*", "Links*")
	for _, entry := range []struct {
		name string
		work ReplayWorkSummary
	}{{"Normal defaults", comparison.Normal}, {"-buildvcs=false", comparison.NoVCS}} {
		work := entry.work
		fmt.Fprintf(output, "  %-18s %10s %10s %12s %10s %8d %8d\n", entry.name, measuredDuration(work.MedianWallTimeNS), measuredDuration(work.MedianWarmWallTimeNS), measuredDuration(work.MedianCompilationToolNS), measuredDuration(work.MedianLinkToolNS), work.TotalBuildActions, work.TotalLinkActions)
	}
	fmt.Fprintf(output, "  Median paired difference (no-VCS minus normal): wall %s; linking work %s\n", signedDuration(comparison.MedianWallDeltaNS), signedDuration(comparison.MedianLinkDeltaNS))
	fmt.Fprintln(output, "* Action totals across matched events. Independent caches/worktrees; order alternates. Differences are observational, not proven VCS causality.")
}

func signedDuration(value *int64) string {
	if value == nil {
		return "-"
	}
	return fmt.Sprintf("%+.3fs", time.Duration(*value).Seconds())
}
