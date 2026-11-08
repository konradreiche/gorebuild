package history

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

func FormatText(report Report, top int) string {
	if top < 1 {
		top = 10
	}
	var output strings.Builder
	fmt.Fprintf(&output, "Target: %s\n", report.Metadata.ImportPath)
	fmt.Fprintf(&output, "Go %s  %s/%s\n", report.Metadata.GoVersion, report.Metadata.GOOS, report.Metadata.GOARCH)
	var changed, inTarget int
	for _, pkg := range report.Packages {
		if pkg.Changes > 0 {
			changed++
			if pkg.InTarget {
				inTarget++
			}
		}
	}
	requested := report.Metadata.SinceRequested
	if requested == "" {
		requested = report.Metadata.SinceUTC.Format("2006-01-02")
	}
	fmt.Fprintf(&output, "History requested: %s (since %s UTC)\nEvents analyzed:   %d; %d changed package directories (%d in target)\n", requested, report.Metadata.SinceUTC.Format("2006-01-02"), len(report.Events), changed, inTarget)
	oldest := "(none)"
	if report.Metadata.OldestEventUTC != nil {
		oldest = report.Metadata.OldestEventUTC.Format("2006-01-02")
	}
	fmt.Fprintf(&output, "Oldest event:      %s\n", oldest)
	coverage := "Complete (available first-parent history)"
	if report.Metadata.Truncated {
		coverage = fmt.Sprintf("Incomplete (--max-commits=%d)", report.Metadata.MaxCommits)
	}
	if report.Metadata.Shallow {
		coverage = "Incomplete (shallow repository)"
		if report.Metadata.Truncated {
			coverage = fmt.Sprintf("Incomplete (--max-commits=%d; shallow repository)", report.Metadata.MaxCommits)
		}
	}
	fmt.Fprintf(&output, "Coverage:          %s\n", coverage)
	files := report.NonGoChanges.Files
	events := report.NonGoChanges.Events
	fmt.Fprintf(&output, "Non-Go changes: %d unlikely / %d potentially build-relevant / %d unknown file changes (%d/%d/%d events; categories may overlap)\n", files.Unlikely, files.PotentialBuild, files.Unknown, events.Unlikely, events.PotentialBuild, events.Unknown)
	if report.Replay == nil {
		fmt.Fprintln(&output, "Replayed: 0; build impact is estimated, not measured")
	} else {
		fmt.Fprintf(&output, "Replayed: %d measured / %d selected (%d requested); %d failed, %d excluded\n",
			report.Replay.Measured, report.Replay.Selected, report.Replay.Requested, report.Replay.Failed, report.Replay.Excluded)
	}
	if report.Metadata.Dirty {
		fmt.Fprintln(&output, "Warning: dirty working tree; current graph may differ from HEAD history.")
	}
	if report.Metadata.Shallow {
		fmt.Fprintln(&output, "Warning: shallow repository; history and boundary deltas are incomplete.")
	}

	if report.FocusPackage != "" {
		formatPackage(&output, report, top)
	} else if report.Replay != nil {
		formatMeasuredTable(&output, report, top)
	} else {
		fmt.Fprintln(&output, "\nHistorical build-event frequency (dependency reach is estimated):")
		fmt.Fprintf(&output, "  %-32s %12s %10s\n", "Package", "Build events", "Consumers")
		var shown int
		for _, pkg := range report.Packages {
			if !pkg.InTarget || pkg.BuildChanges == 0 {
				continue
			}
			if shown == top {
				fmt.Fprintln(&output, "  ... (full event and package data in JSON)")
				break
			}
			fmt.Fprintf(&output, "  %-32s %12d %10d\n", pkg.Dir, pkg.BuildChanges, len(pkg.ReverseDependencies))
			shown++
		}
		if shown == 0 {
			fmt.Fprintln(&output, "  (no non-test Go changes mapped to the current target)")
		}
		fmt.Fprintln(&output, "Build events include non-test Go changes and mapped potential build inputs, not proven invalidations.")
		fmt.Fprintln(&output, "Consumers exclude the package itself. Frequency/reach alone do not identify optimization opportunities.")
		if changed > inTarget {
			fmt.Fprintf(&output, "%d changed directories outside the current target graph retained in JSON.\n", changed-inTarget)
		}
	}

	if report.Replay != nil && report.FocusPackage == "" {
		formatActualWork(&output, report, top)
	}
	formatVCSComparison(&output, report)
	fmt.Fprintln(&output, "\nCo-change patterns (including tests and mapped inputs; small counts are not coupling evidence):")
	var shown int
	for _, pair := range report.CoChanges {
		if report.FocusPackage != "" && pair.Packages[0] != report.FocusPackage && pair.Packages[1] != report.FocusPackage {
			continue
		}
		fmt.Fprintf(&output, "  %s + %s  %d events (%.0f%% of %s [%d/%d]; %.0f%% of %s [%d/%d])\n", pair.Packages[0], pair.Packages[1], pair.Commits, 100*pair.Shares[0], pair.Packages[0], pair.Commits, pair.PackageChanges[0], 100*pair.Shares[1], pair.Packages[1], pair.Commits, pair.PackageChanges[1])
		shown++
		if shown == min(top, 5) {
			break
		}
	}
	if shown == 0 {
		fmt.Fprintln(&output, "  (none)")
	}
	if report.Replay != nil {
		formatReplayProblems(&output, report, top)
	}
	fmt.Fprintln(&output, "\nNotes:")
	fmt.Fprintln(&output, "  Static estimates use today's import graph; reverse dependencies may remain cached.")
	if report.Replay != nil {
		fmt.Fprintln(&output, "  Measured medians/P90 use successful event samples only; sparse coverage and single-run variability limit conclusions.")
		fmt.Fprintln(&output, "  Cohorts overlap: medians are not isolated package costs, causal attribution, or measured refactoring savings.")
		fmt.Fprintln(&output, "  Executed-only critical paths omit Go startup/VCS/cache/scheduling overhead. Tool work, path elapsed time and wall time are not additive; warm baseline is not subtracted.")
	}
	fmt.Fprintln(&output, "  One commit delta is one event. Package rows overlap; do not sum them as independent builds.")
	fmt.Fprintln(&output, "  First-parent history includes merge deltas. Directory moves and historical build tags are not reconstructed.")
	fmt.Fprintln(&output, "  Non-Go classification uses current inputs and heuristics; historical generation/embedding may differ. Even docs/tests can cause VCS relinking.")
	return output.String()
}

func formatPackage(output *strings.Builder, report Report, top int) {
	var selected PackageImpact
	for _, pkg := range report.Packages {
		if pkg.Dir == report.FocusPackage {
			selected = pkg
			break
		}
	}
	fmt.Fprintf(output, "\nPackage: %s", selected.Dir)
	if selected.ImportPath != "" {
		fmt.Fprintf(output, " (%s)", selected.ImportPath)
	}
	fmt.Fprintf(output, "\nChange events: %d; potential build events: %d\n", selected.Changes, selected.BuildChanges)
	if selected.Measured == nil {
		fmt.Fprintln(output, "Sampled rebuild median / P90: unavailable (no replay)")
	} else {
		measured := selected.Measured
		fmt.Fprintf(output, "Measured build events: %d / %d total; %d selected, %d failed, %d excluded\n", measured.Samples, selected.BuildChanges, measured.Selected, measured.Failed, measured.Excluded)
		fmt.Fprintf(output, "Sampled event rebuild median: %s\n", measuredDuration(measured.MedianWallTimeNS))
		if measured.MinWallTimeNS != nil {
			fmt.Fprintf(output, "Sampled rebuild range: %s - %s\n", measuredDuration(measured.MinWallTimeNS), measuredDuration(measured.MaxWallTimeNS))
		}
		if measured.P90WallTimeNS != nil {
			fmt.Fprintf(output, "Sampled rebuild P90: %s\n", measuredDuration(measured.P90WallTimeNS))
		} else if measured.Samples > 0 {
			fmt.Fprintln(output, "P90 not reported (needs at least 10 successful samples)")
		}
		if measured.Samples == 1 {
			fmt.Fprintln(output, "Single successful observation: inspect event evidence below; no package-cohort median is reported.")
		}
		if measured.Samples >= 2 {
			fmt.Fprintf(output, "Median recorded tool work: package itself %s; other compilation %s; linking %s\n", measuredDuration(measured.MedianOwnBuildToolNS), measuredDuration(measured.MedianOtherBuildToolNS), measuredDuration(measured.MedianLinkToolNS))
			fmt.Fprintln(output, "These are overlapping tool-work medians, not a sequential wall-time breakdown; other compilation includes co-changed packages.")
		}
	}
	if selected.InTarget {
		fmt.Fprintf(output, "Potential consumers: %d\n", len(selected.ReverseDependencies))
		for _, consumer := range selected.ReverseDependencies[:min(top, len(selected.ReverseDependencies))] {
			fmt.Fprintf(output, "  %s\n", consumer)
		}
		fmt.Fprintf(output, "Longest potential dependency path (unweighted, not time):\n  %s\n", strings.Join(selected.PotentialPath, " -> "))
	} else {
		fmt.Fprintln(output, "Outside the current target graph; static reach is unknown. Historical event measurements, if any, are shown above.")
	}
	fmt.Fprintln(output, "Historical activity (events by UTC month):")
	for _, month := range selected.Monthly {
		fmt.Fprintf(output, "  %s  %d\n", month.Month, month.Changes)
	}
	if report.Replay != nil {
		fmt.Fprintln(output, "Sampled historical changes (most recent first; all events in JSON):")
	} else {
		fmt.Fprintln(output, "Representative change events (most recent first):")
	}
	var shown int
	for _, event := range report.Events {
		if report.Replay != nil && event.Replay == nil {
			continue
		}
		if !slices.Contains(event.PackageDirs, selected.Dir) {
			continue
		}
		if shown == top {
			fmt.Fprintln(output, "  ... (all events in JSON)")
			break
		}
		fmt.Fprintf(output, "  %s  %s  %d potentially affected packages (event union)\n", event.Commit[:min(12, len(event.Commit))], event.TimeUTC.Format("2006-01-02"), len(event.EstimatedAffectedPackages))
		formatEventReplay(output, event, top)
		for _, file := range event.Files {
			fmt.Fprintf(output, "    %q [%s]\n", file.Path, file.Classification)
		}
		if len(event.UnmodeledFiles) > 0 {
			fmt.Fprintln(output, "    Includes unmodeled non-Go changes; estimate may be incomplete.")
		}
		shown++
	}
	if shown == 0 {
		if report.Replay != nil {
			fmt.Fprintln(output, "  (no sampled events involving this package)")
		} else {
			fmt.Fprintln(output, "  (none in selected history)")
		}
	}
}

func formatMeasuredTable(output *strings.Builder, report Report, top int) {
	fmt.Fprintf(output, "\nHistorical rebuild impact (%d sampled events; whole-event cohorts):\n", report.Replay.Selected)
	fmt.Fprintf(output, "  %-32s %12s %15s %12s\n", "Changed package", "Build events", "Median event wall", "Replays/all")
	var shown int
	for _, pkg := range report.Packages {
		if pkg.BuildChanges == 0 {
			continue
		}
		if shown == top {
			fmt.Fprintln(output, "  ... (all package statistics in JSON)")
			break
		}
		measured := pkg.Measured
		if measured == nil {
			measured = &PackageMeasurements{}
		}
		fmt.Fprintf(output, "  %-32s %12d %15s %12s\n", pkg.Dir, pkg.BuildChanges, measuredDuration(measured.MedianWallTimeNS), fmt.Sprintf("%d/%d", measured.Samples, pkg.BuildChanges))
		shown++
	}
	if shown == 0 {
		fmt.Fprintln(output, "  (no changed Go directories)")
	}
	fmt.Fprintln(output, "Event wall time belongs to the whole change, not to any single changed package. '-' means unmeasured or fewer than two successful replays.")
	fmt.Fprintln(output, "Ordered by build-event frequency; measured median breaks ties. Counts are events, not developer build invocations.")
}

func formatEventReplay(output *strings.Builder, event Event, top int) {
	if event.Replay == nil {
		fmt.Fprintln(output, "    Rebuild: not sampled")
		return
	}
	formatReplayEvidence(output, event.Replay, top)
	if event.NoVCSReplay != nil {
		fmt.Fprintln(output, "    Independent -buildvcs=false replay:")
		formatReplayEvidence(output, event.NoVCSReplay, top)
	}
	if event.VCSComparison != nil {
		delta := event.VCSComparison
		fmt.Fprintf(output, "    Observed no-VCS minus normal: wall %+.3fs; compilation work %+.3fs; link work %+.3fs; build/link actions %+d/%+d\n", time.Duration(delta.WallTimeNS).Seconds(), time.Duration(delta.CompilationToolNS).Seconds(), time.Duration(delta.LinkToolNS).Seconds(), delta.BuildActions, delta.LinkActions)
	}
}

func formatReplayEvidence(output *strings.Builder, result *ReplayResult, top int) {
	if len(result.SelectionReasons) > 0 {
		fmt.Fprintf(output, "    Selected for: %s (all selection reasons in JSON)\n", result.SelectionReasons[0])
	}
	if result.Status != "measured" {
		fmt.Fprintf(output, "    Rebuild: %s at %s: %s\n", result.Status, result.Stage, firstLine(result.Reason))
		return
	}
	build := result.Rebuild.Run
	fmt.Fprintf(output, "    Rebuild: %.3fs wall; %.3fs recorded tool work\n", time.Duration(build.WallTimeNS).Seconds(), time.Duration(build.Graph.ToolTimeNS).Seconds())
	if result.Warm != nil {
		fmt.Fprintf(output, "    Warm no-op baseline: %.3fs wall\n", time.Duration(result.Warm.Run.WallTimeNS).Seconds())
	}
	fmt.Fprintf(output, "    Executed: %d total; %d build / %d cgo-bearing / %d assembly-bearing / %d link nodes (subsets overlap)\n", build.Graph.ExecutedActions, build.Graph.ExecutedBuildActions, build.Graph.ExecutedCgoActions, build.Graph.ExecutedAssemblyActions, build.Graph.ExecutedLinkActions)
	fmt.Fprintf(output, "    Recorded tool work: compilation %.3fs; linking %.3fs. Recognized compiler/cgo/asm/link commands: %d/%d/%d/%d\n", time.Duration(build.Graph.CompilationToolTimeNS).Seconds(), time.Duration(build.Graph.LinkToolTimeNS).Seconds(), build.Graph.CompileCommands, build.Graph.CgoCommands, build.Graph.AssemblyCommands, build.Graph.LinkCommands)
	formatCriticalPath(output, build.Graph, top)
	fmt.Fprintln(output, "    Rebuilt packages: (executed build nodes; recognized Go recompilation is separate in JSON)")
	for _, pkg := range result.RebuiltPackages[:min(top, len(result.RebuiltPackages))] {
		fmt.Fprintf(output, "      %s\n", pkg)
	}
	if len(result.RebuiltPackages) == 0 {
		fmt.Fprintln(output, "      (none)")
	} else if len(result.RebuiltPackages) > top {
		fmt.Fprintln(output, "      ... (all executed actions in JSON)")
	}
	for _, pkg := range result.LinkedPackages {
		fmt.Fprintf(output, "    Linked: %s\n", pkg)
	}
}

func formatReplayProblems(output *strings.Builder, report Report, top int) {
	var shown int
	for _, event := range report.Events {
		for _, entry := range []struct {
			name   string
			result *ReplayResult
		}{{"normal", event.Replay}, {"no-vcs", event.NoVCSReplay}} {
			if entry.result == nil || entry.result.Status == "measured" {
				continue
			}
			if shown == 0 {
				fmt.Fprintln(output, "\nFailed or excluded samples (full diagnostics in JSON):")
			}
			fmt.Fprintf(output, "  %s  %s %s at %s: %s\n", shortCommit(event.Commit), entry.name, entry.result.Status, entry.result.Stage, firstLine(entry.result.Reason))
			shown++
			if shown == min(top, 5) {
				return
			}
		}
	}
}

func firstLine(value string) string {
	line, _, _ := strings.Cut(value, "\n")
	return line
}

func measuredDuration(value *int64) string {
	if value == nil {
		return "-"
	}
	return fmt.Sprintf("%.3fs", time.Duration(*value).Seconds())
}
