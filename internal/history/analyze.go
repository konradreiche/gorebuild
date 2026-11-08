package history

import (
	"path"
	"sort"
	"strings"
)

// Analyze maps historical file paths to package directories, then projects
// non-test Go changes onto today's graph. Directory moves deliberately remain
// separate identities; removed/out-of-target directories are retained too.
func Analyze(root string, nodes []PackageNode, events []Event) (Report, error) {
	impact, err := analyzeGraph(nodes)
	if err != nil {
		return Report{}, err
	}
	report := Report{SchemaVersion: 4, Analysis: "estimated-current-import-graph", Graph: nodes}
	packages := make(map[string]*PackageImpact)
	nodesByDir := make(map[string]PackageNode)
	knownDirs := make(map[string]bool)
	for _, event := range events {
		for _, file := range event.Files {
			if strings.HasSuffix(file.Path, ".go") {
				knownDirs[path.Dir(file.Path)] = true
			}
		}
	}
	for _, node := range nodes {
		if dir, inside := repositoryDir(root, node.Dir); inside {
			nodesByDir[dir] = node
			knownDirs[dir] = true
			packages[dir] = &PackageImpact{
				Dir: dir, ImportPath: node.ImportPath, InTarget: true,
				ReverseDependencies: impact.consumers[node.ImportPath], PotentialPath: impact.paths[node.ImportPath],
			}
		}
	}
	pairs := make(map[[2]string]int)
	months := make(map[string]map[string]int)
	for _, original := range events {
		// Keep the caller's raw Git events untouched.
		event := Event{Commit: original.Commit, Parents: original.Parents, TimeUTC: original.TimeUTC}
		changed := make(map[string]bool)
		built := make(map[string]bool)
		categories := make(map[string]bool)
		for _, file := range original.Files {
			change := classifyFile(file.Path, nodesByDir, knownDirs)
			if change.PackageDir != "" {
				changed[change.PackageDir] = true
				if !change.TestOnly {
					built[change.PackageDir] = true
				}
			} else {
				countClassification(&report.NonGoChanges.Files, change.Classification)
				categories[change.Classification] = true
				for _, dir := range change.AffectedPackageDirs {
					changed[dir] = true
					built[dir] = true
				}
				if change.Classification == "unknown" || (change.Classification == "potential_build" && len(change.AffectedPackageDirs) == 0) {
					event.UnmodeledFiles = append(event.UnmodeledFiles, file.Path)
				}
			}
			event.Files = append(event.Files, change)
		}
		for category := range categories {
			countClassification(&report.NonGoChanges.Events, category)
		}
		affected := make(map[string]bool)
		for dir := range changed {
			event.PackageDirs = append(event.PackageDirs, dir)
			pkg := packages[dir]
			if pkg == nil {
				pkg = &PackageImpact{Dir: dir}
				packages[dir] = pkg
			}
			pkg.Changes++
			pkg.Commits = append(pkg.Commits, event.Commit)
			if months[dir] == nil {
				months[dir] = make(map[string]int)
			}
			months[dir][event.TimeUTC.Format("2006-01")]++
			if built[dir] {
				pkg.BuildChanges++
				event.BuildPackageDirs = append(event.BuildPackageDirs, dir)
				if pkg.InTarget {
					affected[pkg.ImportPath] = true
					for _, consumer := range pkg.ReverseDependencies {
						affected[consumer] = true
					}
				}
			}
		}
		sort.Strings(event.PackageDirs)
		sort.Strings(event.BuildPackageDirs)
		for pkg := range affected {
			event.EstimatedAffectedPackages = append(event.EstimatedAffectedPackages, pkg)
		}
		sort.Strings(event.EstimatedAffectedPackages)
		// Each unordered pair contributes once per event, not once per file.
		for index, left := range event.PackageDirs {
			for _, right := range event.PackageDirs[index+1:] {
				pairs[[2]string{left, right}]++
			}
		}
		report.Events = append(report.Events, event)
	}
	for dir, pkg := range packages {
		for month, count := range months[dir] {
			pkg.Monthly = append(pkg.Monthly, MonthCount{Month: month, Changes: count})
		}
		sort.Slice(pkg.Monthly, func(i, j int) bool { return pkg.Monthly[i].Month < pkg.Monthly[j].Month })
		report.Packages = append(report.Packages, *pkg)
	}
	rankPackages(report.Packages)
	for pair, count := range pairs {
		counts := [2]int{packages[pair[0]].Changes, packages[pair[1]].Changes}
		report.CoChanges = append(report.CoChanges, CoChange{
			Packages: pair, Commits: count, PackageChanges: counts,
			Shares: [2]float64{float64(count) / float64(counts[0]), float64(count) / float64(counts[1])},
		})
	}
	sort.Slice(report.CoChanges, func(i, j int) bool {
		left := report.CoChanges[i]
		right := report.CoChanges[j]
		if left.Commits != right.Commits {
			return left.Commits > right.Commits
		}
		if left.Packages[0] != right.Packages[0] {
			return left.Packages[0] < right.Packages[0]
		}
		return left.Packages[1] < right.Packages[1]
	})
	return report, nil
}

// Frequency is evidence of historical activity, not a cost or opportunity
// score. Reach remains supporting data and does not determine the ranking.
func rankPackages(packages []PackageImpact) {
	sort.Slice(packages, func(i, j int) bool {
		left := packages[i]
		right := packages[j]
		if left.BuildChanges != right.BuildChanges {
			return left.BuildChanges > right.BuildChanges
		}
		return left.Dir < right.Dir
	})
}
