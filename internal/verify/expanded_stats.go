package verify

import (
	"fmt"
	"slices"
	"strings"
)

func validPair(pair Pair) bool {
	return pair.Before != nil && pair.After != nil && pair.Before.Status == "measured" && pair.After.Status == "measured"
}

func noiseConclusion(pairs, controls []Pair) NoiseConclusion {
	var deltas, absolute []int64
	for _, pair := range pairs {
		if validPair(pair) {
			deltas = append(deltas, pair.After.Incremental.Run.WallTimeNS-pair.Before.Incremental.Run.WallTimeNS)
		}
	}
	for _, pair := range controls {
		if validPair(pair) {
			delta := pair.After.Incremental.Run.WallTimeNS - pair.Before.Incremental.Run.WallTimeNS
			if delta < 0 {
				delta = -delta
			}
			absolute = append(absolute, delta)
		}
	}
	treatment := distribution(deltas)
	noise := distribution(absolute)
	result := NoiseConclusion{Confidence: "Preliminary", Direction: "undetermined", ControlAbsoluteMedianNS: noise.MedianNS, ControlMaxAbsoluteNS: noise.MaxNS}
	if !treatment.MedianAvailable {
		result.Reason = "fewer than two valid treatment pairs"
		return result
	}
	if treatment.MedianNS < 0 {
		result.Direction = "after faster"
	} else {
		result.Direction = "after slower or equal"
	}
	if treatment.MinNS <= 0 && treatment.MaxNS >= 0 {
		result.Confidence = "Inconclusive"
		result.Reason = "paired differences span zero; structural cache isolation is independent of this timing conclusion"
		return result
	}
	if treatment.Count < 5 || noise.Count < 5 {
		result.Reason = "fewer than five treatment/control pairs; no significance claim"
		return result
	}
	magnitude := treatment.MedianNS
	if magnitude < 0 {
		magnitude = -magnitude
	}
	if magnitude <= noise.MaxNS {
		result.Confidence = "Inconclusive"
		result.Reason = "median treatment difference lies within maximum observed same-revision absolute difference"
		return result
	}
	result.Confidence = "Consistent (observed only)"
	result.Reason = "all paired differences have the same sign and median magnitude exceeds maximum observed same-revision noise; not a statistical significance test"
	return result
}

func structuralCounts(report *Report, pairs []Pair) []StructuralCount {
	names := []string{report.Before.ImportPath, report.Before.ChangedPackage}
	for _, edge := range append(append([]Edge(nil), report.DependencyChanges.Removed...), report.DependencyChanges.Added...) {
		if !slices.Contains(names, edge.From) {
			names = append(names, edge.From)
		}
	}
	slices.Sort(names)
	var result []StructuralCount
	for _, name := range names {
		value := StructuralCount{Package: name}
		for _, pair := range pairs {
			if !validPair(pair) {
				continue
			}
			value.Pairs++
			if slices.Contains(pair.Before.Recompiled, name) {
				value.BeforeCompiled++
			}
			if slices.Contains(pair.After.Recompiled, name) {
				value.AfterCompiled++
			}
		}
		value.Conclusion = structuralConclusion(value)
		result = append(result, value)
	}
	return result
}

func structuralConclusion(value StructuralCount) string {
	if value.Pairs == 0 {
		return "no accepted observations"
	}
	if value.BeforeCompiled == 0 && value.AfterCompiled == 0 {
		return "cached in both revisions; scenario did not propagate here, no avoided-action claim"
	}
	if value.BeforeCompiled == value.Pairs && value.AfterCompiled == 0 {
		return "compilation avoided in every observed pair"
	}
	return "observed compiler execution counts; no universal avoidance claim"
}

func expandedFormat(report *Report) string {
	var out strings.Builder
	fmt.Fprintf(&out, "Verify %s: %s -> %s\nStructural graph result: %d removed, %d added edges; compilation outcomes below are scenario-specific.\n", report.Target, short(report.Before.Commit), short(report.After.Commit), len(report.DependencyChanges.Removed), len(report.DependencyChanges.Added))
	for _, reach := range report.DependencyChanges.Reachability {
		if reach.Before && !reach.After {
			fmt.Fprintf(&out, "  Verified dependency reach removed: %s -> %s\n", reach.Package, report.Before.ChangedPackage)
		}
	}
	fmt.Fprintf(&out, "Correctness: before=%t after=%t; tests are not complete behavioral equivalence.\n", report.Before.Correct, report.After.Correct)
	fmt.Fprintf(&out, "Cold performance (%d independent pairs; separate from incremental repetitions):\n", report.ColdSummary.ValidPairs)
	showMetric(&out, "Cold wall", report.ColdSummary.ColdWall)
	fmt.Fprintln(&out, "  Cold conclusions are preliminary; ranges and possible regressions are retained, no import-removal speedup assumed.")
	for _, result := range report.Scenarios {
		fmt.Fprintf(&out, "\nScenario %s [%s]: %s", result.Scenario.Name, result.Scenario.Kind, result.Status)
		if result.Scenario.Direction != "" {
			fmt.Fprintf(&out, " (%s historical patch; isolated original event: %t)", result.Scenario.Direction, result.Scenario.IsolatedHistoricalEvent)
		}
		fmt.Fprintln(&out)
		for _, diagnostic := range result.Diagnostics[:min(2, len(result.Diagnostics))] {
			fmt.Fprintf(&out, "  Diagnostic: %s\n", diagnostic)
		}
		for _, value := range result.Structure {
			fmt.Fprintf(&out, "  Compiler %s: before %d/%d; after %d/%d — %s\n", value.Package, value.BeforeCompiled, value.Pairs, value.AfterCompiled, value.Pairs, value.Conclusion)
		}
		showMetric(&out, "Incremental wall", result.Summary.IncrementalWall)
		showMetric(&out, "Compilation work (not savings)", result.Summary.CompilationWork)
		showMetric(&out, "Link work", result.Summary.LinkWork)
		showMetric(&out, "Executed critical path estimate", result.Summary.CriticalPath)
		fmt.Fprintf(&out, "  Same-revision control: %d pairs; absolute paired median %s, maximum %s.\n", result.ControlSummary.ValidPairs, duration(result.Noise.ControlAbsoluteMedianNS), duration(result.Noise.ControlMaxAbsoluteNS))
		fmt.Fprintf(&out, "  Timing confidence: %s; %s; %s\n", result.Noise.Confidence, result.Noise.Direction, result.Noise.Reason)
	}
	for _, limitation := range report.Untested {
		fmt.Fprintf(&out, "Untested: %s\n", limitation)
	}
	for _, failure := range report.Errors {
		fmt.Fprintf(&out, "Failure: %s\n", failure)
	}
	fmt.Fprintln(&out, "Structural cache isolation is separate from latency. Full inputs, patches, caller audit, captures, controls and individual schedules are in --out.")
	return out.String()
}

func showMetric(out *strings.Builder, name string, metric MetricComparison) {
	if !metric.PairedDelta.MedianAvailable {
		fmt.Fprintf(out, "  %s: median unavailable (%d pairs).\n", name, metric.PairedDelta.Count)
		return
	}
	fmt.Fprintf(out, "  %s: before %s, after %s; paired after-minus-before median %s [%s, %s].\n", name, duration(metric.Before.MedianNS), duration(metric.After.MedianNS), duration(metric.PairedDelta.MedianNS), duration(metric.PairedDelta.MinNS), duration(metric.PairedDelta.MaxNS))
}
