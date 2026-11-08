package history

import (
	"fmt"
	"slices"
	"strings"

	"github.com/konradreiche/gorebuild/internal/presentation"
)

func Render(report Report, options presentation.Options) string {
	var out strings.Builder
	fmt.Fprintf(&out, "gorebuild history: %s\n", report.Metadata.ImportPath)
	presentation.Section(&out, "Finding and coverage")
	coverage := "Partial"
	if report.Metadata.CoverageComplete && !report.Metadata.Shallow && !report.Metadata.Truncated {
		coverage = "Complete (requested available first-parent period)"
	}
	fmt.Fprintf(&out, "  History period: %s to %s.\n  Coverage: %s.\n", report.Metadata.SinceUTC.Format("2006-01-02"), report.Metadata.CapturedUTC.Format("2006-01-02"), coverage)
	if report.Metadata.OldestEventUTC != nil {
		fmt.Fprintf(&out, "  Oldest analyzed event: %s.\n", report.Metadata.OldestEventUTC.Format("2006-01-02"))
	}
	if report.Metadata.Shallow {
		fmt.Fprintln(&out, "  Incomplete: shallow repository.")
	}
	if report.Metadata.Truncated {
		fmt.Fprintf(&out, "  Incomplete: --max-commits=%d truncated the requested period.\n", report.Metadata.MaxCommits)
	}
	var selected, measured, failed, excluded int
	if report.Replay != nil {
		selected = report.Replay.Selected
		measured = report.Replay.Measured
		failed = report.Replay.Failed
		excluded = report.Replay.Excluded
	}
	fmt.Fprintf(&out, "  Events: %d analyzed; %d selected; %d successfully replayed.\n", len(report.Events), selected, measured)
	fmt.Fprintf(&out, "  Replay exclusions: %d; failures: %d. Missing samples are not zero cost.\n", excluded, failed)
	if measured == 0 {
		fmt.Fprintln(&out, "  No measured rebuild costs; frequency/reach alone does not prove rebuilds.")
	}
	label := func(name string) string {
		for _, pkg := range report.Packages {
			if pkg.ImportPath == name {
				return pkg.Dir
			}
		}
		return name
	}
	presentation.Section(&out, "Most frequently changed packages")
	packages := slices.Clone(report.Packages)
	slices.SortStableFunc(packages, func(a, b PackageImpact) int {
		if a.Changes != b.Changes {
			return b.Changes - a.Changes
		}
		return strings.Compare(a.Dir, b.Dir)
	})
	var changed [][]string
	for _, pkg := range packages {
		if pkg.Changes == 0 || report.FocusPackage != "" && pkg.Dir != report.FocusPackage {
			continue
		}
		changed = append(changed, []string{pkg.Dir, fmt.Sprint(pkg.Changes), fmt.Sprint(pkg.BuildChanges)})
		if len(changed) == options.Limit() {
			break
		}
	}
	presentation.Table(&out, options.Columns(), []string{"Package", "Change events", "Build-input events"}, changed)
	if len(changed) == 0 {
		fmt.Fprintln(&out, "  None attributed in this scope.")
	}
	fmt.Fprintln(&out, "  Change events include tests; build-input events are not proven misses.")
	presentation.Section(&out, "Most frequently recompiled packages (observed)")
	var work *ReplayWorkSummary
	if report.Replay != nil {
		work = report.Replay.Work
	}
	if work == nil || work.Samples == 0 {
		fmt.Fprintln(&out, "  No executed compiler measurements available.")
	} else {
		rebuilt := slices.Clone(work.RebuiltPackages)
		slices.SortStableFunc(rebuilt, func(a, b RebuiltPackage) int {
			if a.RecompiledReplays != b.RecompiledReplays {
				return b.RecompiledReplays - a.RecompiledReplays
			}
			return strings.Compare(a.Package, b.Package)
		})
		var rows [][]string
		for _, pkg := range rebuilt {
			if pkg.RecompiledReplays == 0 || report.FocusPackage != "" && label(pkg.Package) != report.FocusPackage {
				continue
			}
			rows = append(rows, []string{label(pkg.Package), fmt.Sprintf("%d/%d", pkg.RecompiledReplays, work.Samples), presentation.Optional(pkg.MedianToolTimeNS)})
			if len(rows) == options.Limit() {
				break
			}
		}
		presentation.Table(&out, options.Columns(), []string{"Package", "Replay events", "Package tool work*"}, rows)
		fmt.Fprintln(&out, "  *Recorded build-node median; may include cgo/assembly, not event wall time.")
	}
	presentation.Section(&out, "Measured whole-event rebuild durations")
	if work != nil {
		fmt.Fprintf(&out, "  Median wall time: %s (%d successful event samples).\n", presentation.Optional(work.MedianWallTimeNS), work.Samples)
	} else {
		fmt.Fprintln(&out, "  Unavailable; replay must be explicitly requested.")
	}
	fmt.Fprintln(&out, "  One commit delta is one event. Package cohorts overlap; never sum them.")
	presentation.Section(&out, "Limitations and next investigation")
	fmt.Fprintln(&out, "  Sparse/non-random coverage and host variability limit cost conclusions.")
	fmt.Fprintln(&out, "  Static reach uses the current graph, not historical cache causality.")
	var shown int
	for _, event := range report.Events {
		if event.Replay != nil && (event.Replay.Status == "excluded" || event.Replay.Status == "failed") {
			fmt.Fprintf(&out, "  %s: %s\n", event.Replay.Status, event.Replay.Reason)
			shown++
			if shown == 2 {
				break
			}
		}
	}
	for _, warning := range report.Warnings[:min(2, len(report.Warnings))] {
		fmt.Fprintf(&out, "  Warning: %s\n", warning)
	}
	if report.Metadata.Dirty {
		fmt.Fprintln(&out, "  Warning: dirty current graph may differ from captured HEAD.")
	}
	if options.Details {
		presentation.Section(&out, "Supporting evidence and diagnostics")
		out.WriteString(FormatText(report, options.Limit()))
	} else {
		presentation.Hint(&out)
	}
	return presentation.Fit(out.String(), options.Columns())
}
