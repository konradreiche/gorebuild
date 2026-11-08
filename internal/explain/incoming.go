package explain

import (
	"context"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/konradreiche/gorebuild/internal/profile"
)

type ImportRequirement struct {
	Declaration string   `json:"declaration"`
	Symbols     []string `json:"symbols"`
	Positions   []string `json:"positions"`
}

type IncomingEvent struct {
	Commit                  string   `json:"commit"`
	DependencySourceChanged bool     `json:"dependency_source_changed"`
	DependencyBuildRoute    []string `json:"inferred_dependency_build_route,omitempty"`
	HistoricalTargetRoute   []string `json:"historical_import_to_target_route,omitempty"`
	TargetRecompiled        bool     `json:"target_recompiled"`
	TargetSourceChanged     bool     `json:"target_source_changed"`
	TargetInputsUncertain   bool     `json:"target_inputs_uncertain"`
	TargetSourceFiles       []string `json:"target_changed_source_files,omitempty"`
	OnCriticalPath          bool     `json:"on_critical_path"`
	ObservedImportChanged   bool     `json:"observed_import_content_changed"`
	ObservedChangedImports  []string `json:"observed_changed_imports,omitempty"`
	ObservedChangedFiles    []string `json:"observed_changed_target_inputs,omitempty"`
	CacheEvidenceComplete   bool     `json:"cache_evidence_complete"`
}

type IncomingDependency struct {
	Import              string              `json:"import"`
	Requirements        []ImportRequirement `json:"resolved_requirements"`
	DirectChangeEvents  int                 `json:"dependency_source_change_events"`
	TargetRecompiled    int                 `json:"relevant_target_recompilations"`
	WithoutTargetSource int                 `json:"recompilations_without_target_source_changes"`
	OnCriticalPath      int                 `json:"without_source_on_critical_path"`
	ObservedInputEvents int                 `json:"observed_import_change_events"`
	Events              []IncomingEvent     `json:"historical_evidence"`
	Boundaries          []IncomingBoundary  `json:"boundaries"`
}

type IncomingBoundary struct {
	Strategy              string            `json:"strategy"`
	Status                string            `json:"status"`
	StructurallySupported bool              `json:"structurally_supported_current_configuration"`
	TypeAnalysisComplete  bool              `json:"type_analysis_complete"`
	HistoricalAlignment   bool              `json:"source_matches_capture"`
	Declarations          []string          `json:"declarations"`
	RemainingDeclarations []string          `json:"remaining_declarations,omitempty"`
	RemovedEdges          []DependencyEdge  `json:"potentially_removed_edges"`
	AddedEdges            []DependencyEdge  `json:"proposed_added_edges"`
	RequiredAPIChanges    []string          `json:"required_api_changes"`
	Reasons               []string          `json:"reasons"`
	Constraints           []string          `json:"constraints"`
	FactoryCalls          []FactoryBoundary `json:"resolved_factory_boundaries,omitempty"`
	AffectedActions       []BoundaryImpact  `json:"conditional_action_impacts"`
	ConditionalEvents     int               `json:"conditional_avoidance_events"`
	HistoryAnalyzed       bool              `json:"declaration_history_analyzed"`
	DeclarationChanges    int               `json:"relevant_group_change_events"`
	DeclarationUnchanged  int               `json:"relevant_group_unchanged_events"`
	HistoryUnknown        int               `json:"unknown_group_history_events"`
}

type BoundaryImpact struct {
	Commit          string `json:"commit"`
	ActionID        int    `json:"target_action_id,omitempty"`
	Conditional     bool   `json:"conditional_avoidance"`
	Reason          string `json:"reason"`
	CurrentNS       int64  `json:"current_critical_path_ns,omitempty"`
	WithoutNS       int64  `json:"without_target_action_ns,omitempty"`
	MaximumSavingNS int64  `json:"maximum_theoretical_saving_ns,omitempty"`
}

func importRequirements(source *checkedSource, fset *token.FileSet) map[string][]ImportRequirement {
	result := make(map[string][]ImportRequirement)
	for name, declaration := range source.declarations {
		symbols := make(map[string]map[string]bool)
		positions := make(map[string][]string)
		ast.Inspect(declaration.node, func(node ast.Node) bool {
			identifier, ok := node.(*ast.Ident)
			if !ok {
				return true
			}
			obj := source.info.Uses[identifier]
			if obj == nil || obj.Pkg() == nil || obj.Pkg() == source.pkg {
				return true
			}
			dependency := obj.Pkg().Path()
			if symbols[dependency] == nil {
				symbols[dependency] = make(map[string]bool)
			}
			symbols[dependency][obj.Name()] = true
			positions[dependency] = append(positions[dependency], fset.Position(identifier.Pos()).String())
			return true
		})
		for dependency, found := range symbols {
			var names []string
			for symbol := range found {
				names = append(names, symbol)
			}
			sort.Strings(names)
			result[dependency] = append(result[dependency], ImportRequirement{Declaration: name, Symbols: names, Positions: positions[dependency]})
		}
	}
	for dependency := range result {
		slices.SortFunc(result[dependency], func(left, right ImportRequirement) int { return strings.Compare(left.Declaration, right.Declaration) })
	}
	return result
}

func analyzeIncoming(ctx context.Context, data *Dataset, root, depDir string, report *Suggestions, options TypeOptions, cache *CacheEvidence) error {
	inspection := &Inspection{Provider: report.Provider, Consumer: report.Provider}
	source, _, fset, err := loadSourcePair(ctx, data, root, depDir, depDir, options, inspection)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		report.Diagnostics = append(report.Diagnostics, "incoming source: "+err.Error())
		return nil
	}
	report.Configurations = append(report.Configurations, SuggestConfiguration{Consumer: report.Provider, Complete: inspection.Complete, Toolchain: inspection.Toolchain, Flags: inspection.Flags, Environment: inspection.Environment})
	report.Diagnostics = append(report.Diagnostics, inspection.Diagnostics...)
	if source.pkg == nil {
		report.Diagnostics = append(report.Diagnostics, "incoming package has no resolved type scope")
		return nil
	}
	requirements := importRequirements(source, fset)
	for name, declaration := range source.declarations {
		report.TargetDeclarationGraph = append(report.TargetDeclarationGraph, DeclarationDetail{Name: name, Kind: declaration.kind, Exported: declaration.exported, Dependencies: declaration.dependencies, Position: fset.Position(declaration.node.Pos()).String()})
	}
	slices.SortFunc(report.TargetDeclarationGraph, func(left, right DeclarationDetail) int { return strings.Compare(left.Name, right.Name) })
	relative, err := filepath.Rel(root, depDir)
	if err != nil {
		return err
	}
	targetDir := filepath.ToSlash(relative)
	graph := currentImportGraph(data)
	historyCache := make(map[string]map[string]string)
	for _, imported := range source.pkg.Imports() {
		dependency := IncomingDependency{Import: imported.Path(), Requirements: requirements[imported.Path()]}
		incomingHistory(data, report.Provider, targetDir, &dependency, cache, graph)
		dependency.Boundaries = incomingBoundaries(data, source, fset, inspection, &dependency, graph)
		for index := range dependency.Boundaries {
			boundary := &dependency.Boundaries[index]
			finishFactoryBoundary(boundary, report.CompositionSites, report.Provider, dependency.Import, graph)
			if boundary.Status == "rejected" {
				continue
			}
			boundary.HistoricalAlignment = !report.SourceDirty && report.SourceHead != "" && report.SourceHead == report.CaptureHead
			if report.SourceDirty || report.SourceHead == "" || report.SourceHead != report.CaptureHead {
				boundary.Status = "requires investigation"
				boundary.Constraints = append(boundary.Constraints, "current source is dirty or differs from capture HEAD")
			}
			if err := incomingGroupHistory(ctx, data, root, targetDir, source, fset, &dependency, boundary, historyCache); err != nil {
				return err
			}
			modelIncomingBoundary(data, report.Provider, &dependency, boundary, cache, graph)
		}
		report.Incoming = append(report.Incoming, dependency)
	}
	slices.SortStableFunc(report.Incoming, func(left, right IncomingDependency) int {
		if incomingSupported(left) != incomingSupported(right) {
			if incomingSupported(left) {
				return -1
			}
			return 1
		}
		if left.WithoutTargetSource != right.WithoutTargetSource {
			return right.WithoutTargetSource - left.WithoutTargetSource
		}
		if left.OnCriticalPath != right.OnCriticalPath {
			return right.OnCriticalPath - left.OnCriticalPath
		}
		if left.DirectChangeEvents != right.DirectChangeEvents {
			return right.DirectChangeEvents - left.DirectChangeEvents
		}
		return strings.Compare(left.Import, right.Import)
	})
	report.Complexity = compilationComplexity(data, report.Provider, source, fset)
	return nil
}

func incomingSupported(dependency IncomingDependency) bool {
	for _, boundary := range dependency.Boundaries {
		if boundary.Status != "rejected" && boundary.StructurallySupported && len(boundary.RemovedEdges) > 0 {
			return true
		}
	}
	return false
}

func currentImportGraph(data *Dataset) map[string][]string {
	graph := make(map[string][]string)
	for _, node := range data.Graph {
		graph[node.ImportPath] = append([]string(nil), node.Imports...)
	}
	return graph
}

func directSourceChanges(event *Event, dir string) ([]string, bool) {
	var files []string
	var uncertain bool
	for _, file := range event.Files {
		if path.Dir(file.Path) == dir && strings.HasSuffix(file.Path, ".go") && !strings.HasSuffix(file.Path, "_test.go") {
			files = append(files, file.Path)
			continue
		}
		if slices.Contains(file.AffectedPackageDirs, dir) || file.PackageDir == dir {
			if file.Classification != "go_source" && file.Classification != "test_only" && file.Classification != "unlikely" && !file.TestOnly {
				uncertain = true
			}
		}
		if file.Classification == "unknown" || (file.Classification == "potential_build" && len(file.AffectedPackageDirs) == 0) {
			uncertain = true
		}
	}
	sort.Strings(files)
	return files, uncertain
}

func incomingHistory(data *Dataset, target, targetDir string, dependency *IncomingDependency, cache *CacheEvidence, graph map[string][]string) {
	for _, event := range data.Events {
		changed := data.changedImports(&event)
		direct := slices.Contains(changed, dependency.Import)
		if direct {
			dependency.DirectChangeEvents++
		}
		var route []string
		if event.Replay.Measured() {
			for _, source := range changed {
				if source == dependency.Import {
					route = []string{source}
					break
				}
				if candidate := dependencyPath(event.Replay.Graph, source, dependency.Import); len(candidate) > 0 {
					route = candidate
					break
				}
			}
		}
		observed, imports, files := cache.inputChanges(event.Commit, dependency.Import)
		if !direct && len(route) == 0 && !observed {
			continue
		}
		evidence := IncomingEvent{Commit: event.Commit, DependencySourceChanged: direct, DependencyBuildRoute: route, TargetRecompiled: recompiled(event.Replay, target), ObservedImportChanged: observed, ObservedChangedImports: imports, ObservedChangedFiles: files, CacheEvidenceComplete: cache != nil && cache.Complete && cache.Commit == event.Commit}
		evidence.TargetSourceFiles, evidence.TargetInputsUncertain = directSourceChanges(&event, targetDir)
		evidence.TargetSourceChanged = len(evidence.TargetSourceFiles) > 0
		if event.Replay.Measured() {
			evidence.HistoricalTargetRoute = dependencyPath(event.Replay.Graph, dependency.Import, target)
		}
		if evidence.TargetRecompiled {
			dependency.TargetRecompiled++
			for _, action := range buildActions(event.Replay, target) {
				if onPath(event.Replay.Graph, action.ID) {
					evidence.OnCriticalPath = true
				}
			}
			if !evidence.TargetSourceChanged {
				dependency.WithoutTargetSource++
				if evidence.OnCriticalPath {
					dependency.OnCriticalPath++
				}
			}
		}
		if observed {
			dependency.ObservedInputEvents++
		}
		dependency.Events = append(dependency.Events, evidence)
	}
}

func modelIncomingBoundary(data *Dataset, target string, dependency *IncomingDependency, boundary *IncomingBoundary, cache *CacheEvidence, graph map[string][]string) {
	for _, evidence := range dependency.Events {
		if !evidence.TargetRecompiled {
			continue
		}
		impact := BoundaryImpact{Commit: evidence.Commit}
		for index := range data.Events {
			event := &data.Events[index]
			if event.Commit != evidence.Commit {
				continue
			}
			impact.Reason = incomingImpactExclusion(data, event, target, dependency.Import, evidence, boundary, cache, graph)
			if impact.Reason != "" {
				break
			}
			actions := buildActions(event.Replay, target)
			if len(actions) != 1 {
				impact.Reason = "single target compiler action unavailable"
				break
			}
			bound, err := profile.EstimateCriticalPathSaving(event.Replay.Graph.ActionGraph, actions[0].ID)
			if err != nil {
				impact.Reason = "critical-path counterfactual unavailable"
				break
			}
			impact.Conditional = true
			impact.ActionID = actions[0].ID
			impact.CurrentNS = bound.CurrentNS
			impact.WithoutNS = bound.WithoutNS
			impact.MaximumSavingNS = bound.MaximumSavingNS
			impact.Reason = "conditional zero-target-action bound; new adapter cost and actual cache behavior unverified"
			boundary.ConditionalEvents++
			break
		}
		boundary.AffectedActions = append(boundary.AffectedActions, impact)
	}
	if boundary.ConditionalEvents == 0 {
		boundary.Constraints = append(boundary.Constraints, "no sampled event isolates an avoidable target compilation; measured savings unavailable")
	}
}

func incomingImpactExclusion(data *Dataset, event *Event, target, dependency string, evidence IncomingEvent, boundary *IncomingBoundary, cache *CacheEvidence, graph map[string][]string) string {
	if evidence.TargetSourceChanged {
		return "target source changed directly; removing an import does not avoid this compilation"
	}
	if !boundary.StructurallySupported || !boundary.HistoricalAlignment {
		return "structural analysis/source alignment is incomplete"
	}
	if len(evidence.HistoricalTargetRoute) < 2 && !evidence.ObservedImportChanged {
		return "historical import-to-target route/cache input not established"
	}
	if evidence.TargetInputsUncertain || len(evidence.ObservedChangedFiles) > 0 {
		return "target non-Go/observed file inputs changed or are uncertain"
	}
	if cache != nil && cache.Commit == event.Commit && !cache.Complete {
		return "selected cache evidence is incomplete or incompatible"
	}
	changed := data.changedImports(event)
	if len(changed) == 0 || len(changed) != len(event.BuildPackageDirs) {
		return "changed build inputs are unmapped"
	}
	retained := append([]string(nil), graph[target]...)
	retained = slices.DeleteFunc(retained, func(name string) bool { return name == dependency })
	for _, source := range changed {
		if source == target {
			return "target source/build inputs changed"
		}
		for _, imported := range retained {
			if importReachable(graph, imported, source) {
				return "other target imports remain exposed to changed inputs"
			}
		}
	}
	for _, imported := range evidence.ObservedChangedImports {
		if imported != dependency {
			return "other target imported content hashes changed"
		}
	}
	if cache != nil && cache.Commit == event.Commit {
		for _, change := range cache.Changes {
			if change.Category == "toolchain/build flags/environment or other Go hash inputs" || strings.HasPrefix(change.Category, "module/") {
				return "target configuration/module cache inputs also changed"
			}
		}
	}
	if target == data.Metadata.ImportPath && event.Replay.Graph.ExecutedLinkActions > 0 && !strings.Contains(strings.Join(event.Replay.BuildFlags, " "), "-buildvcs=false") {
		return "main stamping/linking inputs remain unisolated"
	}
	return ""
}

// Keep the actual Go object for factory type/interface validation, not textual
// selector matching. Qualified names are for evidence presentation only.
func objectDependency(obj types.Object, dependency string) bool {
	return obj != nil && obj.Pkg() != nil && obj.Pkg().Path() == dependency
}

func incomingSummary(report *Suggestions, data *Dataset, top int) string {
	var out strings.Builder
	fmt.Fprintf(&out, "\nA. Reduce incoming dependencies (%d resolved direct imports)\n", len(report.Incoming))
	for _, dependency := range report.Incoming[:min(top, len(report.Incoming))] {
		fmt.Fprintf(&out, "  %s: %d relevant recompilations; %d without target source edits, %d on critical path; %d observed import-hash changes (%d direct dependency-change events).\n", data.label(dependency.Import), dependency.TargetRecompiled, dependency.WithoutTargetSource, dependency.OnCriticalPath, dependency.ObservedInputEvents, dependency.DirectChangeEvents)
		var names []string
		for _, requirement := range dependency.Requirements {
			names = append(names, requirement.Declaration)
		}
		fmt.Fprintf(&out, "    Resolved declarations: %s", strings.Join(names[:min(4, len(names))], ", "))
		if len(names) > 4 {
			fmt.Fprintf(&out, " ... (%d more in --out)", len(names)-4)
		}
		fmt.Fprintln(&out)
		for _, boundary := range dependency.Boundaries {
			if boundary.Status == "rejected" {
				continue
			}
			if !boundary.StructurallySupported {
				fmt.Fprintf(&out, "    %s: requires investigation; incomplete structural support, no edge-removal claim.\n", boundary.Strategy)
				continue
			}
			fmt.Fprintf(&out, "    %s: %s; potentially removes target -> %s (current-config structural support: %t)\n", boundary.Strategy, boundary.Status, data.label(dependency.Import), boundary.StructurallySupported)
			if boundary.HistoryAnalyzed {
				fmt.Fprintf(&out, "      Git declarations in dependency-change events: unchanged %d, changed %d, unknown %d.\n", boundary.DeclarationUnchanged, boundary.DeclarationChanges, boundary.HistoryUnknown)
			}
			for _, change := range boundary.RequiredAPIChanges[:min(2, len(boundary.RequiredAPIChanges))] {
				fmt.Fprintf(&out, "      %s\n", change)
			}
			if len(boundary.RequiredAPIChanges) > 2 {
				fmt.Fprintf(&out, "      ... (%d more API changes in --out)\n", len(boundary.RequiredAPIChanges)-2)
			}
			fmt.Fprintf(&out, "      Conditional modeled events: %d; actual savings unverified.\n", boundary.ConditionalEvents)
			var bounds []int64
			for _, impact := range boundary.AffectedActions {
				if impact.Conditional {
					bounds = append(bounds, impact.MaximumSavingNS)
				}
			}
			if value, ok := median(bounds); ok {
				fmt.Fprintf(&out, "      Median fixed-DAG upper bound: %s (new adapter costs excluded).\n", seconds(value))
			} else if len(bounds) == 1 {
				fmt.Fprintf(&out, "      Single-event fixed-DAG upper bound: %s, no median.\n", seconds(bounds[0]))
			} else {
				fmt.Fprintln(&out, "      No isolated sampled benefit: timing bound withheld, not a claim of zero future benefit.")
			}
		}
	}
	if report.CacheEvidence != nil {
		fmt.Fprintf(&out, "  Observed inputs: %s; commit %s; compatible complete evidence: %t (separate diagnostic replay).\n", report.CacheEvidence.Path, shortCommit(report.CacheEvidence.Commit), report.CacheEvidence.Complete)
		for _, warning := range report.CacheEvidence.Warnings {
			fmt.Fprintf(&out, "    %s\n", warning)
		}
	}
	fmt.Fprintln(&out, "  Paths suggest exposure, not invalidation causality. Direct target edits never become avoided builds.")
	return out.String()
}
