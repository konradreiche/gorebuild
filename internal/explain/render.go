package explain

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/konradreiche/gorebuild/internal/presentation"
)

// Render reads already analyzed capture/source records. Full Describe output is
// retained separately for artifacts and the progressive detailed view.
func Render(data *Dataset, selection Options, inspection *Inspection, cache *CacheDiagnostic, options presentation.Options) (string, error) {
	detailed, err := Describe(data, selection)
	if err != nil {
		return "", err
	}
	var out strings.Builder
	target := selection.Commit
	var pkg string
	if selection.Package != "" {
		pkg, err = data.ResolvePackage(selection.Package)
		if err != nil {
			return "", err
		}
		target = data.label(pkg)
	}
	fmt.Fprintf(&out, "gorebuild explain: %s\n", target)
	var event *Event
	var selected, usable, compiled, direct int
	var highest int64
	for index := range data.Events {
		candidate := &data.Events[index]
		if selection.Commit != "" {
			if strings.HasPrefix(candidate.Commit, selection.Commit) {
				event = candidate
			}
			continue
		}
		if candidate.Replay == nil {
			continue
		}
		selected++
		if !candidate.Replay.Measured() {
			continue
		}
		usable++
		if !recompiled(candidate.Replay, pkg) {
			continue
		}
		compiled++
		if slices.Contains(data.changedImports(candidate), pkg) {
			direct++
		}
		var tool int64
		for _, action := range buildActions(candidate.Replay, pkg) {
			tool += action.ToolTimeNS
		}
		if event == nil || tool > highest {
			event = candidate
			highest = tool
		}
	}
	presentation.Section(&out, "Finding")
	if pkg != "" {
		fmt.Fprintf(&out, "  Observed: Go compilation in %d/%d usable replayed events.\n", compiled, usable)
		fmt.Fprintf(&out, "  %d had direct package edits; other events do not establish a sole cause.\n", direct)
		fmt.Fprintf(&out, "  Coverage: %d selected, %d usable; not all historical events replayed.\n", selected, usable)
	}
	if event == nil || !event.Replay.Measured() {
		fmt.Fprintln(&out, "  No usable executed-action measurement for this selection.")
		if event != nil && event.Replay != nil {
			fmt.Fprintf(&out, "  Diagnostic: %s %s %s\n", event.Replay.Status, event.Replay.Reason, event.Replay.Problem)
		}
	} else {
		graph := event.Replay.Graph
		fmt.Fprintf(&out, "  Observed event %s: wall time %s; %d executed actions.\n", shortCommit(event.Commit), presentation.Duration(event.Replay.Rebuild.Run.WallTimeNS), graph.ExecutedActions)
		presentation.Section(&out, "Dependency path (inferred from recorded DAG)")
		var route []string
		if pkg != "" {
			for _, changed := range data.changedImports(event) {
				if changed != pkg {
					route = dependencyPath(graph, changed, pkg)
					if len(route) > 1 {
						break
					}
				}
			}
		}
		if len(route) > 1 {
			for index := range route {
				route[index] = data.label(route[index])
			}
			fmt.Fprintf(&out, "  %s\n", strings.Join(route, " -> "))
		} else {
			var chain []string
			for _, step := range graph.ExecutedActionChain {
				if step.ElapsedNS > 0 {
					name := data.label(step.Package)
					if step.Mode != "build" {
						name += " [" + step.Mode + "]"
					}
					chain = append(chain, name)
				}
			}
			if len(chain) > 0 {
				fmt.Fprintf(&out, "  Estimated critical path: %s\n", strings.Join(chain, " -> "))
			} else {
				fmt.Fprintln(&out, "  No upstream route established for this selection.")
			}
		}
		fmt.Fprintln(&out, "  Reachability is not proof of cache invalidation causality.")
		presentation.Section(&out, "Observed expensive actions")
		actions := slices.Clone(graph.ExecutedActionDetails)
		sort.SliceStable(actions, func(i, j int) bool { return actions[i].ToolTimeNS > actions[j].ToolTimeNS })
		var rows [][]string
		for _, action := range actions[:min(options.Limit(), len(actions))] {
			rows = append(rows, []string{data.label(action.Package), action.Mode, presentation.Duration(action.ToolTimeNS)})
		}
		presentation.Table(&out, options.Columns(), []string{"Package", "Action", "Tool work"}, rows)
		presentation.Section(&out, "Estimated critical path")
		fmt.Fprintf(&out, "  %s; weighted executed-action elapsed time, not wall-time savings.\n", presentation.Duration(graph.ExecutedActionChainNS))
	}
	presentation.Section(&out, "Observed cache inputs")
	if cache == nil {
		fmt.Fprintln(&out, "  Not captured here; executed actions alone do not identify changed keys.")
	} else {
		fmt.Fprintf(&out, "  Diagnostic: %s; complete input comparison: %t.\n", cache.Status, cache.Complete)
		changes := slices.Clone(cache.Changes)
		slices.SortStableFunc(changes, func(leftChange, rightChange CacheInputChange) int {
			left := leftChange.Block == "build "+cache.Package
			right := rightChange.Block == "build "+cache.Package
			if left == right {
				return 0
			}
			if left {
				return -1
			}
			return 1
		})
		for _, change := range changes[:min(options.Limit(), len(changes))] {
			kind, name, _ := strings.Cut(change.Block, " ")
			fmt.Fprintf(&out, "  %s %s: %s (%d removed / %d added entries).\n", kind, data.label(name), change.Category, len(change.Removed), len(change.Added))
			if options.Details {
				for _, input := range change.Removed[:min(options.Limit(), len(change.Removed))] {
					fmt.Fprintf(&out, "    Removed input: %s\n", input)
				}
				for _, input := range change.Added[:min(options.Limit(), len(change.Added))] {
					fmt.Fprintf(&out, "    Added input: %s\n", input)
				}
			}
		}
		fmt.Fprintln(&out, "  Hash differences show changed inputs, not a sole sufficient cause.")
	}
	presentation.Section(&out, "Resolved typed-source dependencies")
	if inspection == nil {
		fmt.Fprintln(&out, "  Not requested; no declaration-level dependency claim.")
	} else {
		fmt.Fprintf(&out, "  %s -> %s: %d referenced declarations; complete: %t.\n", data.label(inspection.Consumer), data.label(inspection.Provider), len(inspection.Declarations), inspection.Complete)
		for _, decl := range inspection.Declarations[:min(3, len(inspection.Declarations))] {
			fmt.Fprintf(&out, "  %s (%d references)\n", decl.Name, decl.References)
		}
		if !inspection.Complete {
			fmt.Fprintln(&out, "  Incomplete typed evidence cannot establish removability.")
		}
	}
	presentation.Section(&out, "Limitations and next investigation")
	fmt.Fprintln(&out, "  Counterfactual bounds are models, not measured wall-time savings.")
	fmt.Fprintln(&out, "  Architectural opportunities remain unverified; inspect typed consumers.")
	for _, warning := range data.Warnings[:min(2, len(data.Warnings))] {
		fmt.Fprintf(&out, "  Diagnostic: %s\n", warning)
	}
	if options.Details {
		presentation.Section(&out, "Supporting evidence and estimated counterfactual bounds")
		out.WriteString(detailed)
		if inspection != nil {
			out.WriteString(inspection.Text)
		}
		if cache != nil {
			out.WriteString(cache.Describe(options.Limit()))
		}
	} else {
		presentation.Hint(&out)
	}
	return presentation.Fit(out.String(), options.Columns()), nil
}
