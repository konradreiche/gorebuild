package profile

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/konradreiche/gorebuild/internal/presentation"
)

func Render(report Report, options presentation.Options, dependencyOrder bool) string {
	var out strings.Builder
	fmt.Fprintf(&out, "gorebuild profile: %s\n", report.Metadata.ImportPath)
	graph := report.Cold.Graph
	labels := shortPackageLabels(graph)
	steps := slices.Clone(graph.LongestActionChain)
	steps = slices.DeleteFunc(steps, func(step ChainStep) bool { return step.ElapsedNS <= 0 })
	ranked := slices.Clone(steps)
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].ElapsedNS > ranked[j].ElapsedNS })
	presentation.Section(&out, "Finding")
	if len(ranked) > 0 {
		fmt.Fprintf(&out, "  Largest estimated critical-path action: %s (%s).\n", labels[ranked[0].Package], presentation.Duration(ranked[0].ElapsedNS))
	} else {
		fmt.Fprintln(&out, "  No timed critical-path actions available.")
	}
	presentation.Table(&out, options.Columns(), []string{"Observed build", "Wall time", "Tool work", "Executed actions"}, [][]string{{"Cold", presentation.Duration(report.Cold.WallTimeNS), presentation.Duration(graph.ToolTimeNS), fmt.Sprint(graph.ExecutedActions)}, {"Warm (unchanged)", presentation.Duration(report.WarmNoop.WallTimeNS), presentation.Duration(report.WarmNoop.Graph.ToolTimeNS), fmt.Sprint(report.WarmNoop.Graph.ExecutedActions)}})
	presentation.Section(&out, "Estimated cold critical path")
	fmt.Fprintf(&out, "  Total: %s. Actions ranked by duration.\n", presentation.Duration(graph.LongestActionChainNS))
	if dependencyOrder {
		fmt.Fprintln(&out, "  Showing dependency order (leaf to root), as requested.")
	} else {
		steps = ranked
	}
	var rows [][]string
	for _, step := range steps[:min(options.Limit(), len(steps))] {
		var share float64
		if graph.LongestActionChainNS > 0 {
			share = 100 * float64(step.ElapsedNS) / float64(graph.LongestActionChainNS)
		}
		rows = append(rows, []string{labels[step.Package], step.Mode, presentation.Duration(step.ElapsedNS), fmt.Sprintf("%.1f%%", share)})
	}
	presentation.Table(&out, options.Columns(), []string{"Package", "Action", "Duration", "Path share"}, rows)
	fmt.Fprintln(&out, "  Shares describe the full estimated path, not achievable wall improvement.")
	onPath := make(map[int]bool)
	for _, step := range graph.LongestActionChain {
		onPath[step.ID] = true
	}
	var off []ChainStep
	for _, action := range graph.ActionGraph {
		if !onPath[action.ID] && action.Executed() && (action.Mode == "build" || action.Mode == "link") {
			off = append(off, ChainStep{ID: action.ID, Mode: action.Mode, Package: action.Package, ElapsedNS: int64(action.ToolDuration())})
		}
	}
	sort.SliceStable(off, func(i, j int) bool { return off[i].ElapsedNS > off[j].ElapsedNS })
	presentation.Section(&out, "Expensive competing actions (off path)")
	rows = nil
	for _, step := range off[:min(3, len(off))] {
		rows = append(rows, []string{labels[step.Package], step.Mode, presentation.Duration(step.ElapsedNS)})
	}
	presentation.Table(&out, options.Columns(), []string{"Package", "Action", "Observed tool work"}, rows)
	if len(off) == 0 {
		fmt.Fprintln(&out, "  None recorded.")
	}
	presentation.Section(&out, "Limitations and next investigation")
	fmt.Fprintln(&out, "  Parallel tool work can exceed wall time; path estimates omit overhead.")
	fmt.Fprintln(&out, "  Competing paths limit modeled savings. Repeat before benchmarking edits.")
	fmt.Fprintln(&out, "  Warm unchanged builds do not measure incremental source-edit cost.")
	if options.Details {
		presentation.Section(&out, "Supporting evidence and diagnostics")
		out.WriteString(FormatText(report, options.Limit(), dependencyOrder))
	} else {
		presentation.Hint(&out)
	}
	return presentation.Fit(out.String(), options.Columns())
}
