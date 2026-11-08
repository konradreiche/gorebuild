package history

import (
	"fmt"
	"slices"
	"sort"
)

type eventSelection struct {
	Index   int
	Reasons []string
}

type sampleCandidate struct {
	index  int
	reason string
}

// Sampling balances four understandable queues, not an impact score. Endpoints
// guarantee temporal coverage when the budget permits; duplicates use one build.
// Failed/unsupported samples are never replaced with successful observations.
func sampleEvents(report *Report, count int) []eventSelection {
	var eligible []int
	for index, event := range report.Events {
		if report.FocusPackage == "" || slices.Contains(event.PackageDirs, report.FocusPackage) {
			eligible = append(eligible, index)
		}
	}
	sort.SliceStable(eligible, func(i, j int) bool {
		return report.Events[eligible[i]].TimeUTC.After(report.Events[eligible[j]].TimeUTC)
	})
	count = min(count, len(eligible))
	if count <= 0 {
		return nil
	}
	if count <= 2 {
		result := []eventSelection{{Index: eligible[0], Reasons: []string{"time: newest eligible event"}}}
		if count == 2 {
			result = append(result, eventSelection{Index: eligible[len(eligible)-1], Reasons: []string{"time: oldest eligible event"}})
		}
		sort.Slice(result, func(i, j int) bool { return result[i].Index < result[j].Index })
		return result
	}
	selected := make(map[int][]string)
	add := func(candidate sampleCandidate) bool {
		reasons, exists := selected[candidate.index]
		if !slices.Contains(reasons, candidate.reason) {
			selected[candidate.index] = append(reasons, candidate.reason)
		}
		return !exists
	}
	add(sampleCandidate{eligible[0], "time: newest eligible event"})
	if count > 1 {
		add(sampleCandidate{eligible[len(eligible)-1], "time: oldest eligible event"})
	}
	frequent := slices.Clone(report.Packages)
	rankPackages(frequent)
	reach := slices.Clone(report.Packages)
	sort.Slice(reach, func(i, j int) bool {
		left := reach[i]
		right := reach[j]
		if len(left.ReverseDependencies) != len(right.ReverseDependencies) {
			return len(left.ReverseDependencies) > len(right.ReverseDependencies)
		}
		if left.BuildChanges != right.BuildChanges {
			return left.BuildChanges > right.BuildChanges
		}
		return left.Dir < right.Dir
	})
	var multi []sampleCandidate
	for _, index := range eligible {
		if size := len(report.Events[index].BuildPackageDirs); size > 1 {
			multi = append(multi, sampleCandidate{index, fmt.Sprintf("multi-package: %d build-package changes", size)})
		}
	}
	sort.SliceStable(multi, func(i, j int) bool {
		return len(report.Events[multi[i].index].BuildPackageDirs) > len(report.Events[multi[j].index].BuildPackageDirs)
	})
	var timeline []sampleCandidate
	anchors := min(len(eligible), max(3, (count+3)/4+2))
	for slot := range anchors {
		position := int(int64(slot) * int64(len(eligible)-1) / int64(anchors-1))
		timeline = append(timeline, sampleCandidate{eligible[position], "time: chronological quantile anchor"})
	}
	// Ensure every event remains selectable when other queues exhaust.
	for _, index := range eligible {
		timeline = append(timeline, sampleCandidate{index, "time: remaining chronological coverage"})
	}
	queues := [][]sampleCandidate{
		packageCandidates(frequent, report.Events, eligible, "frequency"),
		packageCandidates(reach, report.Events, eligible, "reach"), multi, timeline,
	}
	signatures := make(map[int]string, len(eligible))
	covered := make(map[string]bool)
	for _, index := range eligible {
		signatures[index] = eventCoverage(report.Events[index], report.Events[eligible[0]], report.Events[eligible[len(eligible)-1]])
		if _, exists := selected[index]; exists {
			covered[signatures[index]] = true
		}
	}
	deferred := make([][]sampleCandidate, len(queues))
	var allowRepeated bool
	for len(selected) < count {
		var progressed bool
		for index := range queues {
			for len(queues[index]) > 0 {
				candidate := queues[index][0]
				queues[index] = queues[index][1:]
				if _, exists := selected[candidate.index]; exists {
					add(candidate)
					continue
				}
				if !allowRepeated && covered[signatures[candidate.index]] {
					deferred[index] = append(deferred[index], candidate)
					continue
				}
				add(candidate)
				if allowRepeated {
					add(sampleCandidate{candidate.index, "coverage fallback: repeated package/input cohort after distinct candidates exhausted"})
				}
				covered[signatures[candidate.index]] = true
				progressed = true
				break
			}
			if len(selected) == count {
				break
			}
		}
		if !progressed {
			if allowRepeated {
				break
			}
			queues = deferred
			allowRepeated = true
		}
	}
	var result []eventSelection
	for index, reasons := range selected {
		result = append(result, eventSelection{Index: index, Reasons: reasons})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Index < result[j].Index })
	return result
}

// A coverage key is descriptive, not a cost prediction. Prefer different
// changed-package/input-class cohorts within four chronological bands.
func eventCoverage(event, newest, oldest Event) string {
	var band int64
	if span := newest.TimeUTC.Unix() - oldest.TimeUTC.Unix(); span > 0 {
		band = min(3, (event.TimeUTC.Unix()-oldest.TimeUTC.Unix())*4/span)
	}
	packages := slices.Clone(event.BuildPackageDirs)
	if len(packages) == 0 {
		packages = slices.Clone(event.PackageDirs)
	}
	sort.Strings(packages)
	classes := make(map[string]bool)
	for _, file := range event.Files {
		classes[file.Classification] = true
	}
	var kinds []string
	for kind := range classes {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	return fmt.Sprintf("%d:%q:%q", band, packages, kinds)
}

// Visit one occurrence per package per round so a single frequently changing
// package does not monopolize its queue. Ordering within each package is newest
// first; the separate timeline queue covers other parts of the period.
func packageCandidates(packages []PackageImpact, events []Event, eligible []int, criterion string) []sampleCandidate {
	occurrences := make(map[string][]int)
	for _, index := range eligible {
		for _, dir := range events[index].BuildPackageDirs {
			occurrences[dir] = append(occurrences[dir], index)
		}
	}
	var active []PackageImpact
	for _, pkg := range packages {
		if pkg.InTarget && pkg.BuildChanges > 0 && len(occurrences[pkg.Dir]) > 0 {
			active = append(active, pkg)
		}
	}
	var result []sampleCandidate
	for len(active) > 0 {
		remaining := active[:0]
		for _, pkg := range active {
			indices := occurrences[pkg.Dir]
			reason := fmt.Sprintf("frequency: %s (%d build events)", pkg.Dir, pkg.BuildChanges)
			if criterion == "reach" {
				reason = fmt.Sprintf("reach: %s (%d potential consumers)", pkg.Dir, len(pkg.ReverseDependencies))
			}
			result = append(result, sampleCandidate{indices[0], reason})
			occurrences[pkg.Dir] = indices[1:]
			if len(indices) > 1 {
				remaining = append(remaining, pkg)
			}
		}
		active = remaining
	}
	return result
}
