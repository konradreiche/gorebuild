package history

import (
	"sort"
)

func successfulReplay(result *ReplayResult) bool {
	return result != nil && result.Status == "measured" && result.Rebuild != nil && result.Rebuild.Failure == ""
}

func compareReplay(normal, noVCS *ReplayResult) *ReplayDifference {
	if !successfulReplay(normal) || !successfulReplay(noVCS) {
		return nil
	}
	base := normal.Rebuild.Run
	variant := noVCS.Rebuild.Run
	return &ReplayDifference{
		WallTimeNS:        variant.WallTimeNS - base.WallTimeNS,
		CompilationToolNS: variant.Graph.CompilationToolTimeNS - base.Graph.CompilationToolTimeNS,
		LinkToolNS:        variant.Graph.LinkToolTimeNS - base.Graph.LinkToolTimeNS,
		ExecutedActions:   variant.Graph.ExecutedActions - base.Graph.ExecutedActions,
		BuildActions:      variant.Graph.ExecutedBuildActions - base.Graph.ExecutedBuildActions,
		LinkActions:       variant.Graph.ExecutedLinkActions - base.Graph.ExecutedLinkActions,
		CriticalPathNS:    variant.Graph.ExecutedActionChainNS - base.Graph.ExecutedActionChainNS,
		CgoActions:        variant.Graph.ExecutedCgoActions - base.Graph.ExecutedCgoActions,
		AssemblyActions:   variant.Graph.ExecutedAssemblyActions - base.Graph.ExecutedAssemblyActions,
		CompileCommands:   variant.Graph.CompileCommands - base.Graph.CompileCommands,
	}
}

// A lone observation remains in event evidence but is not reported as a stable
// package median. P90 separately requires ten observations.
func supportedMedian(values []int64) *int64 {
	if len(values) < 2 {
		return nil
	}
	return median(values)
}

func summarizeWork(results []*ReplayResult) ReplayWorkSummary {
	var summary ReplayWorkSummary
	var wall, warm, compilation, link, chain []int64
	byPackage := make(map[string]*RebuiltPackage)
	toolTimes := make(map[string][]int64)
	elapsedTimes := make(map[string][]int64)
	for _, result := range results {
		if !successfulReplay(result) {
			continue
		}
		summary.Samples++
		run := result.Rebuild.Run
		wall = append(wall, run.WallTimeNS)
		if result.Warm != nil && result.Warm.Failure == "" {
			warm = append(warm, result.Warm.Run.WallTimeNS)
		}
		compilation = append(compilation, run.Graph.CompilationToolTimeNS)
		link = append(link, run.Graph.LinkToolTimeNS)
		chain = append(chain, run.Graph.ExecutedActionChainNS)
		summary.TotalExecutedActions += run.Graph.ExecutedActions
		summary.TotalBuildActions += run.Graph.ExecutedBuildActions
		summary.TotalCompileCommands += run.Graph.CompileCommands
		summary.TotalCgoActions += run.Graph.ExecutedCgoActions
		summary.TotalAssemblyActions += run.Graph.ExecutedAssemblyActions
		summary.TotalLinkActions += run.Graph.ExecutedLinkActions
		seen := make(map[string]bool)
		recompiled := make(map[string]bool)
		for _, action := range run.Graph.ExecutedActionDetails {
			if action.Mode != "build" || action.Package == "" {
				continue
			}
			pkg := byPackage[action.Package]
			if pkg == nil {
				pkg = &RebuiltPackage{Package: action.Package}
				byPackage[action.Package] = pkg
			}
			pkg.ExecutedActions++
			if !seen[action.Package] {
				pkg.Replays++
				seen[action.Package] = true
			}
			if action.CompileCommands > 0 && !recompiled[action.Package] {
				pkg.RecompiledReplays++
				recompiled[action.Package] = true
			}
			toolTimes[action.Package] = append(toolTimes[action.Package], action.ToolTimeNS)
			elapsedTimes[action.Package] = append(elapsedTimes[action.Package], action.ElapsedNS)
		}
	}
	summary.MedianWallTimeNS = supportedMedian(wall)
	summary.MedianWarmWallTimeNS = supportedMedian(warm)
	summary.MedianCompilationToolNS = supportedMedian(compilation)
	summary.MedianLinkToolNS = supportedMedian(link)
	summary.MedianCriticalPathNS = supportedMedian(chain)
	for name, pkg := range byPackage {
		if pkg.Replays >= 2 {
			pkg.MedianToolTimeNS = median(toolTimes[name])
			pkg.MedianActionElapsedNS = median(elapsedTimes[name])
		}
		summary.RebuiltPackages = append(summary.RebuiltPackages, *pkg)
	}
	sort.Slice(summary.RebuiltPackages, func(i, j int) bool {
		left := summary.RebuiltPackages[i]
		right := summary.RebuiltPackages[j]
		if left.Replays != right.Replays {
			return left.Replays > right.Replays
		}
		return left.Package < right.Package
	})
	return summary
}

func summarizeReplayWork(report *Report) {
	var normal, pairedNormal, pairedNoVCS []*ReplayResult
	var wallDelta, linkDelta []int64
	if report.VCSComparison != nil {
		report.VCSComparison = &VCSComparisonSummary{}
	}
	for _, event := range report.Events {
		normal = append(normal, event.Replay)
		if report.VCSComparison == nil || event.NoVCSReplay == nil {
			continue
		}
		switch event.NoVCSReplay.Status {
		case "measured":
			report.VCSComparison.NoVCSMeasured++
		case "failed":
			report.VCSComparison.NoVCSFailed++
		case "excluded":
			report.VCSComparison.NoVCSExcluded++
		}
		if event.VCSComparison != nil {
			pairedNormal = append(pairedNormal, event.Replay)
			pairedNoVCS = append(pairedNoVCS, event.NoVCSReplay)
			wallDelta = append(wallDelta, event.VCSComparison.WallTimeNS)
			linkDelta = append(linkDelta, event.VCSComparison.LinkToolNS)
		}
	}
	work := summarizeWork(normal)
	report.Replay.Work = &work
	if report.VCSComparison != nil {
		report.VCSComparison.PairedEvents = len(pairedNormal)
		report.VCSComparison.Normal = summarizeWork(pairedNormal)
		report.VCSComparison.NoVCS = summarizeWork(pairedNoVCS)
		report.VCSComparison.MedianWallDeltaNS = supportedMedian(wallDelta)
		report.VCSComparison.MedianLinkDeltaNS = supportedMedian(linkDelta)
	}
}
