package explain

import (
	"fmt"
	"strings"

	"github.com/konradreiche/gorebuild/internal/presentation"
)

func (report *Suggestions) Render(data *Dataset, options presentation.Options) string {
	var out strings.Builder
	fmt.Fprintf(&out, "gorebuild suggest: %s\n", data.label(report.Provider))
	var supported, rejectedIncoming, rejectedOutgoing, shown int
	for _, dependency := range report.Incoming {
		for _, boundary := range dependency.Boundaries {
			if boundary.Status == "rejected" {
				rejectedIncoming++
				continue
			}
			if !boundary.StructurallySupported {
				continue
			}
			supported++
			if shown >= options.Limit() {
				continue
			}
			shown++
			presentation.Section(&out, "Structurally supported candidate")
			proposal := boundary.Strategy + " (" + data.label(dependency.Import) + ")"
			if boundary.Strategy == "inject neutral factory results at composition root" && len(boundary.AddedEdges) > 0 {
				proposal = fmt.Sprintf("Move %s construction into %s", data.label(dependency.Import), data.label(boundary.AddedEdges[0].From))
			}
			fmt.Fprintf(&out, "  Proposed: %s\n", proposal)
			renderEdges(&out, data, "Removes", boundary.RemovedEdges)
			renderEdges(&out, data, "Adds", boundary.AddedEdges)
			fmt.Fprintf(&out, "  Observed: %d relevant target recompilations; %d without target source edits.\n", dependency.TargetRecompiled, dependency.WithoutTargetSource)
			fmt.Fprintf(&out, "  Historical: dependency changed in %d events; no sole-cause attribution.\n", dependency.DirectChangeEvents)
			fmt.Fprintln(&out, "  Feasibility: supported in the resolved current configuration.")
			fmt.Fprintf(&out, "  Status: %s; requires implementation and verification.\n", boundary.Status)
			for _, constraint := range boundary.Constraints[:min(2, len(boundary.Constraints))] {
				fmt.Fprintf(&out, "  Constraint: %s\n", constraint)
			}
			if len(boundary.RequiredAPIChanges) > 0 {
				fmt.Fprintf(&out, "  Required: %s\n", boundary.RequiredAPIChanges[0])
			}
			fmt.Fprintln(&out, "  Benefit: conditional cache isolation; latency benefit unverified.")
		}
	}
	for _, candidate := range report.Candidates {
		if candidate.Status == "rejected" {
			rejectedOutgoing++
			continue
		}
		if shown >= options.Limit() {
			continue
		}
		shown++
		presentation.Section(&out, "Candidate requiring validation")
		fmt.Fprintf(&out, "  Proposed: extract %s to %s\n", strings.Join(candidate.Roots, ", "), candidate.Destination)
		renderEdges(&out, data, "Removes", candidate.RemovedEdges)
		renderEdges(&out, data, "Adds", candidate.AddedEdges)
		fmt.Fprintf(&out, "  Observed: consumer recompiled in %d/%d successful samples.\n", candidate.ConsumerRecompiled, candidate.SuccessfulSamples)
		fmt.Fprintf(&out, "  Historical: %d provider changes with group unchanged; %d unknown.\n", candidate.IndependentlyChangingEvents, candidate.UnknownEvents)
		fmt.Fprintf(&out, "  Feasibility: %s; implementation and verification required.\n", candidate.Status)
		for _, constraint := range candidate.Constraints[:min(2, len(candidate.Constraints))] {
			fmt.Fprintf(&out, "  Constraint: %s\n", constraint)
		}
		fmt.Fprintln(&out, "  Benefit: conditional cache isolation; no verified wall-time savings.")
	}
	if shown == 0 {
		presentation.Section(&out, "Finding")
		fmt.Fprintln(&out, "  No structurally supported candidate established in this scope.")
	}
	if supported > options.Limit() {
		fmt.Fprintf(&out, "\n  %d supported candidates; terminal view limited by --top.\n", supported)
	}
	presentation.Section(&out, "Rejected candidates")
	fmt.Fprintf(&out, "  Outgoing groups: %d; incoming boundary proposals: %d.\n", rejectedOutgoing, rejectedIncoming)
	if !options.Details {
		fmt.Fprintln(&out, "  Use --details for individual rejection reasons.")
	}
	presentation.Section(&out, "Limitations")
	fmt.Fprintln(&out, "  Resolved source supports feasibility, not behavioral equivalence.")
	fmt.Fprintln(&out, "  Historical co-occurrence is not cache-invalidation causality.")
	fmt.Fprintln(&out, "  Compiler-complexity inventory requires profiling, not speculative savings.")
	for _, diagnostic := range report.Diagnostics[:min(2, len(report.Diagnostics))] {
		fmt.Fprintf(&out, "  Diagnostic: %s\n", shortDiagnostic(diagnostic))
	}
	if options.Details {
		presentation.Section(&out, "Supporting evidence and limitations")
		out.WriteString(report.Describe(data, options.Limit()))
		// Include every rejection, not only the first --top candidate graphs.
		for _, candidate := range report.Candidates {
			if candidate.Status == "rejected" {
				fmt.Fprintf(&out, "\nRejected outgoing group %s:\n", strings.Join(candidate.Roots, ", "))
				for _, reason := range candidate.Reasons {
					fmt.Fprintf(&out, "  %s\n", reason)
				}
			}
		}
		for _, dependency := range report.Incoming {
			for _, boundary := range dependency.Boundaries {
				if boundary.Status == "rejected" {
					fmt.Fprintf(&out, "\nRejected incoming %s (%s):\n", data.label(dependency.Import), boundary.Strategy)
					for _, reason := range boundary.Reasons {
						fmt.Fprintf(&out, "  %s\n", reason)
					}
				}
			}
		}
	} else {
		presentation.Hint(&out)
	}
	return presentation.Fit(out.String(), options.Columns())
}
func renderEdges(out *strings.Builder, data *Dataset, heading string, edges []DependencyEdge) {
	for _, edge := range edges {
		fmt.Fprintf(out, "  %s: %s -> %s\n", heading, data.label(edge.From), data.label(edge.To))
	}
}
