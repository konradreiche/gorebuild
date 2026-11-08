package explain

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/konradreiche/gorebuild/internal/profile"
)

func seconds(ns int64) string { return fmt.Sprintf("%.3fs", time.Duration(ns).Seconds()) }
func typical(values []int64) string {
	if value, available := median(values); available {
		return seconds(value)
	}
	if len(values) == 1 {
		return seconds(values[0]) + " (single observation)"
	}
	return "unavailable"
}

func describeEvent(data *Dataset, event *Event, top int) (string, error) {
	var out strings.Builder
	fmt.Fprintf(&out, "Historical rebuild: %s\nTarget: %s\n", event.Commit, data.Metadata.ImportPath)
	if len(event.Parents) > 0 {
		fmt.Fprintf(&out, "Parent: %s\n", event.Parents[0])
	}
	fmt.Fprintf(&out, "Changed packages/directories (%d): %s", len(event.PackageDirs), strings.Join(event.PackageDirs[:min(len(event.PackageDirs), max(top, 10))], ", "))
	if len(event.PackageDirs) > max(top, 10) {
		fmt.Fprintf(&out, " ... (%d more in artifact)", len(event.PackageDirs)-max(top, 10))
	}
	fmt.Fprintln(&out)
	if len(event.PackageDirs) == 0 {
		fmt.Fprintln(&out, "  (no attributed Go-package changes; non-Go effects may still exist)")
	}
	fmt.Fprintln(&out, "Changed files:")
	for _, file := range event.Files[:min(len(event.Files), max(top, 10))] {
		fmt.Fprintf(&out, "  %q [%s]\n", file.Path, file.Classification)
	}
	if len(event.Files) > max(top, 10) {
		fmt.Fprintln(&out, "  ... (all files remain in the history artifact)")
	}
	if event.Replay == nil {
		fmt.Fprintln(&out, "Not replayed: no measured cost or critical-path estimate is available.")
		return out.String(), nil
	}
	replay := event.Replay
	if replay.Problem != "" {
		return "", fmt.Errorf("%s: cannot explain corrupt/incomplete measured evidence: %s", event.Commit, replay.Problem)
	}
	if !replay.Measured() {
		fmt.Fprintf(&out, "Replay %s at %s: %s\nNo successful rebuild measurement; not a zero-cost event.\n", replay.Status, replay.Stage, replay.Reason)
		return out.String(), nil
	}
	graph := replay.Graph
	fmt.Fprintf(&out, "\nWall time: %s\n", seconds(replay.Rebuild.Run.WallTimeNS))
	if replay.Warm != nil {
		fmt.Fprintf(&out, "Warm no-op baseline: %s (context, not subtracted)\n", seconds(replay.Warm.Run.WallTimeNS))
	}
	fmt.Fprintf(&out, "Compilation/build tool work: %s; linking tool work: %s\n", seconds(graph.CompilationToolTimeNS), seconds(graph.LinkToolTimeNS))
	fmt.Fprintf(&out, "Executed nodes: %d; build %d, cgo-bearing %d, assembly-bearing %d, link %d (subsets overlap)\n", graph.ExecutedActions, graph.ExecutedBuildActions, graph.ExecutedCgoActions, graph.ExecutedAssemblyActions, graph.ExecutedLinkActions)
	fmt.Fprintln(&out, "Executed package build actions (ranked by recorded tool work):")
	actions := buildActions(replay, "")
	sort.SliceStable(actions, func(i, j int) bool { return actions[i].ToolTimeNS > actions[j].ToolTimeNS })
	for _, action := range actions[:min(top, len(actions))] {
		fmt.Fprintf(&out, "  %9s tool / %9s elapsed  %s (Go compiler commands: %d)\n", seconds(action.ToolTimeNS), seconds(action.ElapsedNS), actionName(data, action), action.CompileCommands)
	}
	if len(actions) == 0 {
		fmt.Fprintln(&out, "  (none)")
	}
	if len(actions) > top {
		fmt.Fprintf(&out, "  ... (%d more; full actions/commands in the artifact)\n", len(actions)-top)
	}
	fmt.Fprintf(&out, "\nCritical dependency path (executed action elapsed, estimated): %s\n", seconds(graph.ExecutedActionChainNS))
	var chainNames []string
	var pathActions []profile.ExecutedAction
	byID := make(map[int]profile.ExecutedAction)
	for _, action := range graph.ExecutedActionDetails {
		byID[action.ID] = action
	}
	for _, step := range graph.ExecutedActionChain {
		if step.ElapsedNS > 0 {
			chainNames = append(chainNames, actionName(data, byID[step.ID]))
			pathActions = append(pathActions, byID[step.ID])
		}
	}
	if len(chainNames) > 0 {
		fmt.Fprintf(&out, "  Dependency order: %s\n", strings.Join(chainNames, " -> "))
	} else {
		fmt.Fprintln(&out, "  (no executed elapsed duration)")
	}
	sort.SliceStable(pathActions, func(i, j int) bool { return pathActions[i].ElapsedNS > pathActions[j].ElapsedNS })
	fmt.Fprintln(&out, "Largest critical-path contributions (ranked by action elapsed):")
	for _, action := range pathActions[:min(top, len(pathActions))] {
		fmt.Fprintf(&out, "  %9s  %s\n", seconds(action.ElapsedNS), actionName(data, action))
	}
	if err := formatCounterfactuals(&out, data, graph, top); err != nil {
		return "", err
	}
	formatVCS(&out, data, event)
	formatRecurring(&out, data, event, top)
	formatLimits(&out, data)
	return out.String(), nil
}

func formatCounterfactuals(out *strings.Builder, data *Dataset, graph profile.GraphSummary, top int) error {
	actions := slices.Clone(graph.ExecutedActionDetails)
	sort.SliceStable(actions, func(i, j int) bool { return actions[i].ElapsedNS > actions[j].ElapsedNS })
	fmt.Fprintf(out, "\nZero-duration counterfactuals (current estimated path %s):\n", seconds(graph.ExecutedActionChainNS))
	fmt.Fprintf(out, "  %-45s %10s %12s %12s\n", "Executed action", "Observed", "Without it", "Max saving")
	for _, action := range actions[:min(top, len(actions))] {
		change, err := profile.EstimateCriticalPathSaving(graph.ActionGraph, action.ID)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "  %-45s %10s %12s %12s\n", actionName(data, action), seconds(change.ObservedNS), seconds(change.WithoutNS), seconds(change.MaximumSavingNS))
	}
	fmt.Fprintln(out, "Optimistic theoretical critical-path upper bounds, not predicted wall-clock savings. Only one node's duration is zeroed; dependencies and competing paths remain.")
	return nil
}

func formatVCS(out *strings.Builder, data *Dataset, event *Event) {
	fmt.Fprintln(out, "\nVCS stamping:")
	flags := append(strings.Fields(data.Metadata.GOFLAGS), event.Replay.BuildFlags...)
	mode := "auto"
	for _, flag := range flags {
		name, value, _ := strings.Cut(strings.TrimLeft(flag, "-"), "=")
		if name == "buildvcs" {
			mode = value
		}
	}
	fmt.Fprintf(out, "  Captured configuration: -buildvcs=%s.\n", mode)
	if event.NoVCSReplay != nil {
		if event.NoVCSReplay.Measured() {
			variant := event.NoVCSReplay
			fmt.Fprintf(out, "  Matched no-VCS observation: wall %s, compilation %s, linking %s; %d build / %d link nodes.\n", seconds(variant.Rebuild.Run.WallTimeNS), seconds(variant.Graph.CompilationToolTimeNS), seconds(variant.Graph.LinkToolTimeNS), variant.Graph.ExecutedBuildActions, variant.Graph.ExecutedLinkActions)
			fmt.Fprintf(out, "  Observed no-VCS minus normal wall: %+.3fs. A configuration contrast, not proof of VCS-only cost.\n", time.Duration(variant.Rebuild.Run.WallTimeNS-event.Replay.Rebuild.Run.WallTimeNS).Seconds())
		} else {
			fmt.Fprintf(out, "  No matched successful no-VCS observation: %s %s %s\n", event.NoVCSReplay.Status, event.NoVCSReplay.Reason, event.NoVCSReplay.Problem)
		}
	} else if mode == "false" {
		fmt.Fprintln(out, "  Stamping was disabled; source/configuration changes may still require compilation and linking.")
	} else if event.Replay.Graph.ExecutedLinkActions > 0 {
		fmt.Fprintln(out, "  Revision metadata may have contributed to main compilation/linking. No matched no-VCS replay exists in this artifact; VCS-only time cannot be inferred.")
	} else {
		fmt.Fprintln(out, "  No executed linking was recorded; there is no measured linking cost to attribute to stamping.")
	}
}

func formatRecurring(out *strings.Builder, data *Dataset, event *Event, top int) {
	type recurrence struct {
		action profile.ExecutedAction
		others int
		route  []string
	}
	var rows []recurrence
	changed := data.changedImports(event)
	seen := make(map[string]bool)
	for _, action := range buildActions(event.Replay, "") {
		if action.CompileCommands == 0 || seen[action.Package] {
			continue
		}
		seen[action.Package] = true
		row := recurrence{action: action}
		for _, other := range data.Events {
			if other.Commit != event.Commit && recompiled(other.Replay, action.Package) {
				row.others++
			}
		}
		for _, source := range changed {
			if route := dependencyPath(event.Replay.Graph, source, action.Package); len(route) > 1 {
				row.route = route
				break
			}
		}
		if row.others > 0 {
			rows = append(rows, row)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].others != rows[j].others {
			return rows[i].others > rows[j].others
		}
		return rows[i].action.ToolTimeNS > rows[j].action.ToolTimeNS
	})
	fmt.Fprintln(out, "\nRecurring downstream compilation worth investigating (not automatic optimization candidates):")
	for _, row := range rows[:min(top, len(rows))] {
		fmt.Fprintf(out, "  %s: recompiled in %d other successful samples; this action %s recorded tool work", data.label(row.action.Package), row.others, seconds(row.action.ToolTimeNS))
		if slices.Contains(changed, row.action.Package) {
			fmt.Fprint(out, " (also directly changed here)")
		}
		fmt.Fprintln(out)
		if len(row.route) > 0 {
			for index := range row.route {
				row.route[index] = data.label(row.route[index])
			}
			fmt.Fprintf(out, "    Inferred build route: %s\n", strings.Join(row.route, " -> "))
		} else {
			fmt.Fprintln(out, "    No route from an attributed changed package was established; recurrence is observational.")
		}
	}
	if len(rows) == 0 {
		fmt.Fprintln(out, "  (none established in the other measured events)")
	}
}

func formatLimits(out *strings.Builder, data *Dataset) {
	fmt.Fprintln(out, "\nEvidence limits: elapsed action time, tool work and whole-event wall time are distinct and not additive. Cache checks are not executed compiler work.")
	fmt.Fprintln(out, "DAG routes/co-changing packages suggest relationships, not proof of invalidation causality. Models hold other durations and cache/scheduling behavior fixed; no refactoring or savings claim is made.")
	if data.Metadata.Truncated || data.Metadata.Shallow {
		fmt.Fprintln(out, "History coverage is incomplete (limited/shallow capture).")
	}
	if len(data.Warnings) > 0 {
		fmt.Fprintf(out, "%d incomplete/corrupt measured graph(s) were excluded from usable evidence.\n", len(data.Warnings))
	}
}
