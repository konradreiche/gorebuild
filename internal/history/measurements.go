package history

import (
	"slices"
	"sort"
)

func summarizeMeasurements(report *Report) {
	report.Replay.Measured = 0
	report.Replay.Failed = 0
	report.Replay.Excluded = 0
	report.Replay.TotalMeasuredWallTimeNS = 0
	for _, event := range report.Events {
		if event.Replay == nil {
			continue
		}
		switch event.Replay.Status {
		case "measured":
			report.Replay.Measured++
			report.Replay.TotalMeasuredWallTimeNS += event.Replay.Rebuild.Run.WallTimeNS
		case "failed":
			report.Replay.Failed++
		case "excluded":
			report.Replay.Excluded++
		}
	}
	for index := range report.Packages {
		pkg := &report.Packages[index]
		measured := &PackageMeasurements{}
		var wall, own, other, link []int64
		for _, event := range report.Events {
			if event.Replay == nil || !slices.Contains(event.BuildPackageDirs, pkg.Dir) {
				continue
			}
			measured.Selected++
			switch event.Replay.Status {
			case "failed":
				measured.Failed++
			case "excluded":
				measured.Excluded++
			case "measured":
				measured.Samples++
				build := event.Replay.Rebuild.Run
				wall = append(wall, build.WallTimeNS)
				var ownNS int64
				ownImport, known := event.Replay.PackageImports[pkg.Dir]
				for _, action := range build.Graph.ExecutedActionDetails {
					if known && action.Package == ownImport && action.Mode != "link" && (action.Mode == "build" || action.CompileCommands+action.CgoCommands+action.AssemblyCommands > 0) {
						ownNS += action.ToolTimeNS
					}
				}
				own = append(own, ownNS)
				other = append(other, build.Graph.CompilationToolTimeNS-ownNS)
				link = append(link, build.Graph.LinkToolTimeNS)
			}
		}
		measured.MedianWallTimeNS = supportedMedian(wall)
		if len(wall) >= 10 {
			measured.P90WallTimeNS = p90(wall)
		}
		if len(wall) >= 2 {
			low := slices.Min(wall)
			high := slices.Max(wall)
			measured.MinWallTimeNS = &low
			measured.MaxWallTimeNS = &high
		}
		measured.MedianOwnBuildToolNS = supportedMedian(own)
		measured.MedianOtherBuildToolNS = supportedMedian(other)
		measured.MedianLinkToolNS = supportedMedian(link)
		pkg.Measured = measured
	}
	rankMeasuredPackages(report.Packages)
	summarizeReplayWork(report)
}

func median(values []int64) *int64 {
	if len(values) == 0 {
		return nil
	}
	ordered := slices.Clone(values)
	slices.Sort(ordered)
	middle := len(ordered) / 2
	result := ordered[middle]
	if len(ordered)%2 == 0 {
		result = ordered[middle-1] + (ordered[middle]-ordered[middle-1])/2
	}
	return &result
}

// Nearest-rank P90. Small sample counts are exposed, not hidden behind a
// precision/confidence claim; for fewer than ten observations this is the max.
func p90(values []int64) *int64 {
	if len(values) == 0 {
		return nil
	}
	ordered := slices.Clone(values)
	slices.Sort(ordered)
	result := ordered[(9*len(ordered)+9)/10-1]
	return &result
}

// Keep build-event frequency as the primary ordering. Measured latency can
// break ties, but is never multiplied by frequency or summed across packages.
func rankMeasuredPackages(packages []PackageImpact) {
	sort.SliceStable(packages, func(i, j int) bool {
		left := packages[i]
		right := packages[j]
		if left.BuildChanges != right.BuildChanges {
			return left.BuildChanges > right.BuildChanges
		}
		leftKnown := left.Measured != nil && left.Measured.MedianWallTimeNS != nil
		rightKnown := right.Measured != nil && right.Measured.MedianWallTimeNS != nil
		if leftKnown != rightKnown {
			return leftKnown
		}
		if !leftKnown {
			return left.Dir < right.Dir
		}
		if *left.Measured.MedianWallTimeNS != *right.Measured.MedianWallTimeNS {
			return *left.Measured.MedianWallTimeNS > *right.Measured.MedianWallTimeNS
		}
		return left.Dir < right.Dir
	})
}
