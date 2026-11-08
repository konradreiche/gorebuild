package verify

import (
	"fmt"
	"slices"
	"strings"

	"github.com/konradreiche/gorebuild/internal/presentation"
)

// Render summarizes existing evidence only; Format retains the detailed view.
func Render(report *Report, options presentation.Options) string {
	var out strings.Builder
	module := strings.TrimSuffix(report.Before.ChangedPackage, "/"+report.Before.PackageDirectory)
	label := func(name string) string { return presentation.Package(name, module) }
	target := report.Before.PackageDirectory
	if target == "" {
		target = report.Target
	}
	fmt.Fprintf(&out, "gorebuild verify: %s\n", presentation.Package(target, ""))
	presentation.Section(&out, "Structural result")
	var removed bool
	var affected []string
	for _, reach := range report.DependencyChanges.Reachability {
		if reach.Before && !reach.After {
			removed = true
			affected = append(affected, reach.Package)
		}
	}
	status := "Not established"
	if removed {
		status = "Verified"
	}
	rows := [][]string{{"Dependency removed", status}}
	var avoided int
	for _, pkg := range affected {
		var count, pairs int
		var mixed bool
		for _, scenario := range report.Scenarios {
			for _, value := range scenario.Structure {
				if value.Package == pkg && (value.BeforeCompiled > 0 || value.AfterCompiled > 0) {
					if value.BeforeCompiled != value.Pairs || value.AfterCompiled != 0 && value.AfterCompiled != value.Pairs {
						mixed = true
					}
					pairs += value.Pairs
					if value.BeforeCompiled == value.Pairs && value.AfterCompiled == 0 {
						count += value.Pairs
					}
				}
			}
		}
		avoided += count
		value := "No applicable observations"
		if pairs > 0 {
			value = fmt.Sprintf("%d/%d applicable runs", count, pairs)
		}
		if mixed {
			value = "Mixed observations; see --details"
		}
		rows = append(rows, []string{label(pkg) + " compilation avoided", value})
	}
	presentation.Table(&out, options.Columns(), []string{"Check", "Result"}, rows)
	if len(report.Scenarios) > 0 {
		fmt.Fprintln(&out, "  Cached in both revisions is not recompilation avoided.")
	}
	presentation.Section(&out, "Incremental performance")
	var performance [][]string
	for _, scenario := range report.Scenarios {
		delta := scenario.Summary.IncrementalWall.PairedDelta
		conclusion := scenario.Noise.Confidence
		if conclusion == "" {
			conclusion = "Inconclusive"
		}
		if scenario.Status != "measured" {
			conclusion = scenario.Status
		}
		if !report.PerformanceValid {
			conclusion = "Withheld"
		}
		performance = append(performance, []string{scenarioLabel(scenario.Scenario), pairedDelta(delta), conclusion})
	}
	if len(performance) == 0 {
		performance = append(performance, []string{"Exported declaration", pairedDelta(report.Summary.IncrementalWall.PairedDelta), "Inconclusive"})
	}
	presentation.Table(&out, options.Columns(), []string{"Scenario", "Wall delta", "Conclusion"}, performance)
	fmt.Fprintln(&out, "  Paired after-minus-before median; negative means after was faster.")
	presentation.Section(&out, "Cold performance")
	cold := report.ColdSummary.ColdWall.PairedDelta
	if len(report.Scenarios) == 0 {
		cold = report.Summary.ColdWall.PairedDelta
	}
	conclusion := "Inconclusive"
	if !report.PerformanceValid {
		conclusion = "Withheld"
	}
	presentation.Table(&out, options.Columns(), []string{"Measurement", "Result"}, [][]string{{"Median paired wall difference", pairedDelta(cold)}, {"Conclusion", conclusion}})
	presentation.Section(&out, "Correctness")
	builds := "Passed"
	tests := "Passed"
	runtime := "Passed"
	race := true
	for _, revision := range []Revision{report.Before, report.After} {
		if revision.Build == nil {
			if builds != "Failed" {
				builds = "Not checked"
			}
		} else if revision.Build.Failure != "" {
			builds = "Failed"
		}
		if revision.Tests == nil {
			if tests != "Failed" {
				tests = "Not checked"
			}
			race = false
		} else {
			if revision.Tests.Failure != "" {
				tests = "Failed"
			}
			race = race && slices.Contains(revision.Tests.Arguments, "-race")
		}
		for _, check := range revision.ExtraCallerTests {
			if check.Failure != "" {
				tests = "Failed"
			}
			race = race && slices.Contains(check.Arguments, "-race")
		}
		if revision.Runtime == nil {
			if runtime != "Failed" {
				runtime = "Not checked"
			}
		} else if revision.Runtime.Failure != "" {
			runtime = "Failed"
		}
	}
	for _, scenario := range report.Scenarios {
		for _, check := range scenario.Correctness {
			if check.Failure != "" || check.Tests != nil && check.Tests.Failure != "" {
				tests = "Failed"
			}
			if check.Tests != nil {
				race = race && slices.Contains(check.Tests.Arguments, "-race")
			}
		}
	}
	testLabel := "Tests"
	if race {
		testLabel = "Tests and race checks"
	}
	presentation.Table(&out, options.Columns(), []string{"Check", "Result"}, [][]string{{"Builds", builds}, {testLabel, tests}, {"Runtime smoke checks", runtime}})
	presentation.Section(&out, "Assessment")
	if avoided > 0 && report.PerformanceValid {
		fmt.Fprintln(&out, "  Cache isolation verified for the applicable measured edits.")
	}
	var consistent bool
	// Report the existing noise assessment, not a new significance test.
	for _, scenario := range report.Scenarios {
		if strings.HasPrefix(scenario.Noise.Confidence, "Consistent") && report.PerformanceValid {
			consistent = true
			fmt.Fprintf(&out, "  %s: %s (%s); observed only, not statistical significance.\n", scenarioLabel(scenario.Scenario), scenario.Noise.Confidence, scenario.Noise.Direction)
		}
	}
	if !consistent {
		fmt.Fprintln(&out, "  End-to-end performance improvement not established.")
	}
	fmt.Fprintln(&out, "  Do not adopt solely for build speed.")
	if !report.PerformanceValid {
		fmt.Fprintln(&out, "  Performance comparison invalid; see failures below.")
	}
	for _, err := range report.Errors {
		fmt.Fprintf(&out, "  Failure: %s\n", err)
	}
	fmt.Fprintln(&out, "  Tests do not prove equivalence; other configurations remain untested.")
	if options.Details {
		presentation.Section(&out, "Supporting evidence and limitations")
		out.WriteString(Format(report))
	} else {
		presentation.Hint(&out)
	}
	return presentation.Fit(out.String(), options.Columns())
}
func scenarioLabel(scenario Scenario) string {
	switch scenario.Kind {
	case "implementation":
		return "Implementation-only"
	case "exported":
		return "Exported declaration"
	case "historical":
		if scenario.Direction == "reverse" {
			return "Inverse historical edit"
		}
		return "Historical edit"
	}
	return scenario.Name
}
func pairedDelta(value Distribution) string {
	if !value.MedianAvailable {
		return fmt.Sprintf("Unavailable (%d)", value.Count)
	}
	return presentation.Delta(value.MedianNS)
}
