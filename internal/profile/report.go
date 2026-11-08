package profile

import (
	"fmt"
	"sort"
	"strings"

	"github.com/konradreiche/gorebuild/internal/presentation"
)

// FormatText shows the most expensive critical-path actions by default.
// dependencyOrder preserves leaf-to-root order instead. Neither view truncates
// the underlying JSON data.
func FormatText(report Report, top int, dependencyOrder ...bool) string {
	if top < 1 {
		top = 10
	}
	var output strings.Builder
	fmt.Fprintf(&output, "gorebuild profile: %s\n", report.Metadata.ImportPath)
	fmt.Fprintf(&output, "Go %s  %s/%s\n", report.Metadata.GoVersion, report.Metadata.GOOS, report.Metadata.GOARCH)
	fmt.Fprintf(&output, "Cache: fresh isolated GOCACHE, then reused; shared module downloads\n\n")
	fmt.Fprintf(&output, "%-15s %10s %12s %9s\n", "Scenario", "Wall", "Tool work", "Executed")
	for _, scenario := range []struct {
		name string
		run  BuildRun
	}{{"Cold", report.Cold}, {"Warm (no-op)", report.WarmNoop}} {
		graph := scenario.run.Graph
		fmt.Fprintf(&output, "%-15s %10s %12s %9d\n",
			scenario.name, dur(scenario.run.WallTimeNS), dur(graph.ToolTimeNS), graph.ExecutedActions)
	}

	graph := report.Cold.Graph
	packageLabels := shortPackageLabels(graph)
	fmt.Fprintf(&output, "\nCold critical path (estimated): %s\n", dur(graph.LongestActionChainNS))
	onPath := make(map[int]bool, len(graph.LongestActionChain))
	var steps []ChainStep
	for _, step := range graph.LongestActionChain {
		onPath[step.ID] = true
		if step.ElapsedNS > 0 {
			steps = append(steps, step)
		}
	}
	inOrder := len(dependencyOrder) > 0 && dependencyOrder[0]
	if !inOrder {
		sort.SliceStable(steps, func(i, j int) bool { return steps[i].ElapsedNS > steps[j].ElapsedNS })
		fmt.Fprintln(&output, "Actions ranked by duration:")
	} else {
		fmt.Fprintln(&output, "Actions in dependency order (leaf to root):")
	}
	printSteps := func(steps []ChainStep, limit int, shares bool) {
		if shares {
			fmt.Fprintln(&output, "  Duration   Share  Package / Action")
		} else {
			fmt.Fprintln(&output, "  Duration  Package / Action")
		}
		for _, step := range steps[:min(len(steps), limit)] {
			label := packageLabels[step.Package]
			if step.Mode != "build" || label == "" {
				label = strings.TrimSpace(step.Mode + " " + label)
			}
			if shares {
				var share float64
				if graph.LongestActionChainNS > 0 {
					share = 100 * float64(step.ElapsedNS) / float64(graph.LongestActionChainNS)
				}
				fmt.Fprintf(&output, "  %9s  %5.1f%%  %s\n", dur(step.ElapsedNS), share, label)
			} else {
				fmt.Fprintf(&output, "  %9s  %s\n", dur(step.ElapsedNS), label)
			}
		}
		if len(steps) == 0 {
			fmt.Fprintln(&output, "  (none)")
		} else if len(steps) > limit {
			if shares {
				fmt.Fprintf(&output, "  ... (%d more actions; full data in JSON)\n", len(steps)-limit)
			} else {
				fmt.Fprintln(&output, "  ...")
			}
		}
	}
	printSteps(steps, top, true)
	fmt.Fprintln(&output, "\nCritical path explains the longest dependency chain,")
	fmt.Fprintln(&output, "not necessarily the actual wall-clock bottleneck or potential optimization savings.")

	var other []ChainStep
	for _, action := range graph.ActionGraph {
		if !onPath[action.ID] && action.Executed() && (action.Mode == "build" || action.Mode == "link") {
			other = append(other, ChainStep{ID: action.ID, Mode: action.Mode, Package: action.Package, ElapsedNS: int64(action.ToolDuration())})
		}
	}
	sort.SliceStable(other, func(i, j int) bool { return other[i].ElapsedNS > other[j].ElapsedNS })
	fmt.Fprintln(&output, "\nOther expensive build actions (not on critical path):")
	printSteps(other, min(top, 5), false)
	fmt.Fprintln(&output, "\nNotes:")
	fmt.Fprintln(&output, "  Shares use the full estimated critical path, including actions not shown.")
	fmt.Fprintln(&output, "  Critical-path durations are action elapsed times, not an exact breakdown of wall time.")
	fmt.Fprintln(&output, "  Off-path durations are recorded tool times for executed build/link actions only.")
	fmt.Fprintln(&output, "  Tool work sums recorded command wall times and can exceed build wall time due to parallelism.")
	fmt.Fprintln(&output, "  Build work may include cgo/assembly and is not exclusively Go compiler time.")
	fmt.Fprintln(&output, "  Warm (no-op) does not estimate the cost of rebuilding after a source edit.")
	fmt.Fprintln(&output, "  A single run is not a stable benchmark; load, source revision, and build configuration affect timings.")
	return output.String()
}

// Shorten common hosted repository paths, but retain enough context to identify
// the repository (for example aws-sdk-go-v2/service/ec2). Check the entire graph,
// not just the displayed rows, before choosing an unambiguous terminal label.
// Other import paths, including standard-library paths, remain unchanged.
func shortPackageLabels(graph GraphSummary) map[string]string {
	labels := make(map[string]string)
	add := func(pkg string) {
		label := pkg
		parts := strings.Split(pkg, "/")
		if len(parts) >= 3 {
			switch parts[0] {
			case "github.com", "gitlab.com", "bitbucket.org":
				label = strings.Join(parts[2:], "/")
			}
		}
		labels[pkg] = label
	}
	for _, action := range graph.ActionGraph {
		add(action.Package)
	}
	for _, step := range graph.LongestActionChain {
		add(step.Package)
	}
	counts := make(map[string]int)
	for _, label := range labels {
		counts[label]++
	}
	for pkg, label := range labels {
		if counts[label] > 1 {
			labels[pkg] = pkg
		}
	}
	return labels
}

func dur(ns int64) string { return presentation.Duration(ns) }
