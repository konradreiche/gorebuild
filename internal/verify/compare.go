package verify

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/konradreiche/gorebuild/internal/history"
)

func compareGraphs(before, after []history.PackageNode, dependency string) GraphComparison {
	var result GraphComparison
	edges := func(nodes []history.PackageNode) map[Edge]bool {
		found := make(map[Edge]bool)
		for _, node := range nodes {
			for _, imported := range node.Imports {
				found[Edge{From: node.ImportPath, To: imported}] = true
			}
		}
		return found
	}
	left := edges(before)
	right := edges(after)
	for edge := range left {
		if !right[edge] {
			result.Removed = append(result.Removed, edge)
		}
	}
	for edge := range right {
		if !left[edge] {
			result.Added = append(result.Added, edge)
		}
	}
	order := func(a, b Edge) int {
		if value := strings.Compare(a.From, b.From); value != 0 {
			return value
		}
		return strings.Compare(a.To, b.To)
	}
	slices.SortFunc(result.Removed, order)
	slices.SortFunc(result.Added, order)
	names := make(map[string]bool)
	for _, nodes := range [][]history.PackageNode{before, after} {
		for _, node := range nodes {
			if !strings.HasPrefix(node.Dir, "/") && node.Dir != "" {
				names[node.ImportPath] = true
			}
		}
	}
	for name := range names {
		result.Reachability = append(result.Reachability, Reach{Package: name, Before: reaches(before, name, dependency), After: reaches(after, name, dependency)})
	}
	slices.SortFunc(result.Reachability, func(left, right Reach) int { return strings.Compare(left.Package, right.Package) })
	return result
}

func reaches(nodes []history.PackageNode, from, to string) bool {
	graph := make(map[string][]string)
	for _, node := range nodes {
		graph[node.ImportPath] = node.Imports
	}
	seen := make(map[string]bool)
	pending := []string{from}
	for len(pending) > 0 {
		name := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if name == to {
			return true
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		pending = append(pending, graph[name]...)
	}
	return false
}

func distribution(values []int64) Distribution {
	result := Distribution{Count: len(values)}
	if len(values) == 0 {
		return result
	}
	values = append([]int64(nil), values...)
	slices.Sort(values)
	result.MinNS = values[0]
	result.MaxNS = values[len(values)-1]
	if len(values) < 2 {
		return result
	}
	result.MedianAvailable = true
	result.MedianNS = values[len(values)/2]
	if len(values)%2 == 0 {
		left := values[len(values)/2-1]
		right := values[len(values)/2]
		result.MedianNS = left + (right-left)/2
	}
	return result
}

func summarize(pairs []Pair) Summary {
	var result Summary
	compare := func(cold bool, metric func(*Measurement) int64) MetricComparison {
		var before, after, delta []int64
		for _, pair := range pairs {
			if pair.Before == nil || pair.After == nil || pair.Before.Status != "measured" || pair.After.Status != "measured" {
				continue
			}
			if cold {
				if pair.Before.Cold == nil || pair.After.Cold == nil {
					continue
				}
			} else {
				if pair.Before.Incremental == nil || pair.After.Incremental == nil {
					continue
				}
			}
			left := metric(pair.Before)
			right := metric(pair.After)
			before = append(before, left)
			after = append(after, right)
			delta = append(delta, right-left)
		}
		return MetricComparison{Before: distribution(before), After: distribution(after), PairedDelta: distribution(delta)}
	}
	result.ColdWall = compare(true, func(value *Measurement) int64 { return value.Cold.Run.WallTimeNS })
	result.IncrementalWall = compare(false, func(value *Measurement) int64 { return value.Incremental.Run.WallTimeNS })
	result.CompilationWork = compare(false, func(value *Measurement) int64 { return value.Incremental.Run.Graph.CompilationToolTimeNS })
	result.LinkWork = compare(false, func(value *Measurement) int64 { return value.Incremental.Run.Graph.LinkToolTimeNS })
	result.CriticalPath = compare(false, func(value *Measurement) int64 { return value.Incremental.Run.Graph.ExecutedActionChainNS })
	result.ColdCompilationWork = compare(true, func(value *Measurement) int64 { return value.Cold.Run.Graph.CompilationToolTimeNS })
	result.ColdLinkWork = compare(true, func(value *Measurement) int64 { return value.Cold.Run.Graph.LinkToolTimeNS })
	result.ColdCriticalPath = compare(true, func(value *Measurement) int64 { return value.Cold.Run.Graph.ExecutedActionChainNS })
	result.ValidPairs = max(result.IncrementalWall.PairedDelta.Count, result.ColdWall.PairedDelta.Count)
	result.Interpretation = interpret(result)
	return result
}

func interpret(result Summary) string {
	if result.ValidPairs < 2 {
		return "insufficient paired observations for a median or improvement claim"
	}
	deltas := result.IncrementalWall.PairedDelta
	if deltas.Count == 0 {
		deltas = result.ColdWall.PairedDelta
	}
	if deltas.MinNS <= 0 && deltas.MaxNS >= 0 {
		return "paired wall deltas span zero; direction is inconsistent within observed variation; no meaningful improvement established"
	}
	if deltas.MaxNS < 0 {
		return "all valid pairs observed lower wall time after; small observational sample, not statistical significance or behavioral-equivalence proof"
	}
	return "all valid pairs observed higher wall time after; possible regression, subject to load/order variability and small sample"
}

func Format(report *Report) string {
	if len(report.Scenarios) > 0 {
		return expandedFormat(report)
	}
	var out strings.Builder
	fmt.Fprintf(&out, "Verify %s: %s -> %s\nCorrectness checks: before=%t after=%t; performance valid=%t\n", report.Target, short(report.Before.Commit), short(report.After.Commit), report.Before.Correct, report.After.Correct, report.PerformanceValid)
	fmt.Fprintf(&out, "Graph: %d removed / %d added import edges (all differences in JSON).\n", len(report.DependencyChanges.Removed), len(report.DependencyChanges.Added))
	for _, edge := range report.DependencyChanges.Removed[:min(5, len(report.DependencyChanges.Removed))] {
		fmt.Fprintf(&out, "  Removed: %s -> %s\n", edge.From, edge.To)
	}
	for _, edge := range report.DependencyChanges.Added[:min(5, len(report.DependencyChanges.Added))] {
		fmt.Fprintf(&out, "  Added: %s -> %s\n", edge.From, edge.To)
	}
	fmt.Fprintf(&out, "Controlled package: %s; flags %v\n", report.Before.ChangedPackage, report.BuildFlags)
	if report.PerformanceValid {
		fmt.Fprintf(&out, "%d valid paired runs (after minus before; negative means faster):\n", report.Summary.ValidPairs)
		for _, row := range []struct {
			name  string
			value MetricComparison
		}{{"Cold wall", report.Summary.ColdWall}, {"Incremental wall", report.Summary.IncrementalWall}, {"Incremental compilation work", report.Summary.CompilationWork}, {"Incremental linking work", report.Summary.LinkWork}, {"Incremental estimated critical path", report.Summary.CriticalPath}} {
			if !row.value.PairedDelta.MedianAvailable {
				fmt.Fprintf(&out, "  %s: single observation; median withheld\n", row.name)
				continue
			}
			fmt.Fprintf(&out, "  %s: before %s / after %s; paired median delta %s [%s, %s]\n", row.name, duration(row.value.Before.MedianNS), duration(row.value.After.MedianNS), duration(row.value.PairedDelta.MedianNS), duration(row.value.PairedDelta.MinNS), duration(row.value.PairedDelta.MaxNS))
		}
		watched := map[string]bool{report.Before.ImportPath: true, report.Before.ChangedPackage: true}
		for _, edge := range report.DependencyChanges.Removed {
			watched[edge.From] = true
		}
		for _, edge := range report.DependencyChanges.Added {
			watched[edge.From] = true
		}
		var names []string
		for name := range watched {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			var before, after int
			for _, pair := range report.Pairs {
				if pair.Before != nil && slices.Contains(pair.Before.Recompiled, name) {
					before++
				}
				if pair.After != nil && slices.Contains(pair.After.Recompiled, name) {
					after++
				}
			}
			fmt.Fprintf(&out, "  Compiler executed %s: before %d/%d; after %d/%d\n", name, before, report.Summary.ValidPairs, after, report.Summary.ValidPairs)
		}
		fmt.Fprintln(&out, report.Summary.Interpretation)
	} else {
		fmt.Fprintln(&out, "Performance comparison withheld: correctness, build/cache/probe validation or cleanup failed.")
	}
	for _, failure := range report.Errors {
		fmt.Fprintf(&out, "Failure: %s\n", failure)
	}
	fmt.Fprintln(&out, "Tests are not behavioral-equivalence proof. Tool work and critical-path estimates are not wall savings; full captures, individual pairs and configuration are in --out.")
	return out.String()
}

func short(value string) string   { return value[:min(12, len(value))] }
func duration(value int64) string { return fmt.Sprintf("%.3fs", float64(value)/float64(time.Second)) }
