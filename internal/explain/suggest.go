package explain

import (
	"context"
	"fmt"
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"unicode"
)

const maximumGroupDeclarations = 40

type DependencyEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
}
type CandidateEvent struct {
	Commit               string `json:"commit"`
	GroupChanged         bool   `json:"group_changed"`
	Unknown              bool   `json:"unknown"`
	Reason               string `json:"reason,omitempty"`
	ConsumerRecompiled   bool   `json:"consumer_recompiled"`
	ConditionalAvoidance bool   `json:"conditional_avoidance"`
	ActionID             int    `json:"action_id,omitempty"`
	CriticalPathNS       int64  `json:"critical_path_ns,omitempty"`
	WithoutActionNS      int64  `json:"without_action_ns,omitempty"`
	MaximumSavingNS      int64  `json:"maximum_saving_ns,omitempty"`
}
type Candidate struct {
	Roots                       []string            `json:"roots"`
	Consumer                    string              `json:"consumer"`
	Destination                 string              `json:"destination"`
	Declarations                []string            `json:"declarations"`
	Edges                       []DeclarationDetail `json:"declaration_edges"`
	Imports                     []string            `json:"imports"`
	Status                      string              `json:"status"`
	Reasons                     []string            `json:"reasons"`
	Constraints                 []string            `json:"constraints"`
	RemovedEdges                []DependencyEdge    `json:"potentially_removed_edges"`
	AddedEdges                  []DependencyEdge    `json:"proposed_added_edges"`
	RemainingReferences         []string            `json:"remaining_provider_references"`
	UnexportedBoundary          []string            `json:"unexported_incoming_references"`
	HistoryAnalyzed             bool                `json:"history_analyzed"`
	ProviderChangeEvents        int                 `json:"provider_source_change_events"`
	GroupChangeEvents           int                 `json:"group_change_events"`
	IndependentlyChangingEvents int                 `json:"provider_changed_group_unchanged_events"`
	UnknownEvents               int                 `json:"unknown_history_events"`
	ConsumerRecompiled          int                 `json:"consumer_recompiled_events"`
	SuccessfulSamples           int                 `json:"successful_samples"`
	ConditionalEvents           int                 `json:"conditional_avoidance_events"`
	OnCriticalPath              int                 `json:"conditional_events_on_critical_path"`
	Events                      []CandidateEvent    `json:"historical_evidence"`
}
type SuggestConfiguration struct {
	Consumer    string            `json:"consumer"`
	Complete    bool              `json:"complete"`
	Toolchain   string            `json:"toolchain"`
	Flags       []string          `json:"build_flags"`
	Environment map[string]string `json:"build_environment"`
}

type Suggestions struct {
	SchemaVersion          int                    `json:"schema_version"`
	Provider               string                 `json:"provider"`
	Source                 string                 `json:"source"`
	SourceHead             string                 `json:"source_head"`
	CaptureHead            string                 `json:"capture_head"`
	SourceDirty            bool                   `json:"source_dirty"`
	ProviderRecompiled     int                    `json:"provider_recompiled_events"`
	SuccessfulSamples      int                    `json:"successful_samples"`
	TargetDeclarationGraph []DeclarationDetail    `json:"resolved_target_declaration_graph"`
	Incoming               []IncomingDependency   `json:"incoming_dependencies"`
	CompositionSites       []CompositionSite      `json:"resolved_composition_sites"`
	CacheEvidence          *CacheEvidence         `json:"cache_evidence,omitempty"`
	Complexity             *CompilationComplexity `json:"compilation_complexity"`
	Candidates             []Candidate            `json:"candidates"`
	Configurations         []SuggestConfiguration `json:"source_configurations"`
	Diagnostics            []string               `json:"diagnostics"`
	Limitations            []string               `json:"limitations"`
}

// Suggest never edits source or replays builds. Every proposed edge change is
// conditional on manual migration/behavior validation; the fixed-DAG model is
// not an estimate of achieved savings or of the new package's compilation cost.
func Suggest(ctx context.Context, data *Dataset, query, root, consumerFilter string, options TypeOptions, cacheFiles ...string) (*Suggestions, error) {
	provider, err := data.ResolvePackage(query)
	if err != nil {
		return nil, err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	depDir, err := sourceDirectory(data, provider, root)
	if err != nil {
		return nil, err
	}
	result := &Suggestions{SchemaVersion: 2, Provider: provider, Source: root, CaptureHead: data.Metadata.Head, Limitations: []string{
		"Current build configuration only; other platforms/tags and historical type graphs are not reconstructed.",
		"Git declaration fingerprints describe syntactic changes, not semantic independence or cache-hit causality.",
		"At most 40 declarations per bounded group; broad closures are rejected rather than sold as package splits.",
		"Conditional impact zeros one observed consumer action, retaining competing paths; new package cost and real cache behavior are unverified.",
		"Only direct repository consumers in the captured target closure are inspected, not unrelated packages or tests.",
		"No automatic edits, target builds, generators or historical replays; refactoring verification is future work.",
	}}
	if len(cacheFiles) > 1 {
		return nil, fmt.Errorf("select at most one cache diagnostic artifact")
	}
	if len(cacheFiles) == 1 && cacheFiles[0] != "" {
		result.CacheEvidence, err = readCacheEvidence(cacheFiles[0], data, provider)
		if err != nil {
			return nil, err
		}
	}
	for _, event := range data.Events {
		if event.Replay.Measured() {
			result.SuccessfulSamples++
		}
		if recompiled(event.Replay, provider) {
			result.ProviderRecompiled++
		}
	}
	head, err := sourceGit(ctx, root, "rev-parse", "HEAD")
	if err != nil {
		result.Diagnostics = append(result.Diagnostics, "source Git HEAD unavailable")
	} else {
		result.SourceHead = strings.TrimSpace(string(head))
	}
	status, err := sourceGit(ctx, root, "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		result.Diagnostics = append(result.Diagnostics, "source cleanliness unavailable")
		result.SourceDirty = true
	} else {
		result.SourceDirty = len(status) > 0
	}
	var consumers []string
	for _, node := range data.Graph {
		if slices.Contains(node.Imports, provider) {
			if _, err := sourceDirectory(data, node.ImportPath, root); err != nil {
				continue
			}
			consumers = append(consumers, node.ImportPath)
		}
	}
	if consumerFilter != "" {
		consumer, err := data.ResolvePackage(consumerFilter)
		if err != nil {
			return nil, err
		}
		if !slices.Contains(consumers, consumer) {
			return nil, fmt.Errorf("selected consumer does not directly import provider in captured target graph")
		}
		consumers = []string{consumer}
	}
	sort.Strings(consumers)
	if len(consumers) == 0 {
		result.Diagnostics = append(result.Diagnostics, "no direct repository consumers in captured target scope; no dependency-removing extraction established")
	}
	changes := make(map[string]map[string]string) // shared historical source parse cache
	for _, consumer := range consumers {
		consumerDir, err := sourceDirectory(data, consumer, root)
		if err != nil {
			return nil, err
		}
		inspection := &Inspection{Provider: provider, Consumer: consumer}
		dep, use, fset, err := loadSourcePair(ctx, data, root, depDir, consumerDir, options, inspection)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			result.Diagnostics = append(result.Diagnostics, consumer+": "+err.Error())
			continue
		}
		result.Configurations = append(result.Configurations, SuggestConfiguration{Consumer: consumer, Complete: inspection.Complete, Toolchain: inspection.Toolchain, Flags: inspection.Flags, Environment: inspection.Environment})
		result.Diagnostics = append(result.Diagnostics, inspection.Diagnostics...)
		result.CompositionSites = append(result.CompositionSites, compositionCalls(dep, use, fset, inspection.Complete)...)
		roots := referencedDeclarations(dep, use)
		atoms := declarationAtoms(dep)
		seeds := make([][]string, 0, len(roots)+1)
		for _, name := range roots {
			seeds = append(seeds, []string{name})
		}
		if len(roots) > 1 {
			seeds = append(seeds, roots)
		}
		if len(roots) == 0 {
			result.Diagnostics = append(result.Diagnostics, consumer+": no resolved provider exports; no extraction inferred")
		}
		seen := make(map[string]bool)
		for _, seed := range seeds {
			names := closedGroup(dep, atoms, seed)
			key := strings.Join(names, "\x00")
			if seen[key] {
				continue
			}
			seen[key] = true
			candidate := makeCandidate(data, dep, use, fset, inspection, seed, roots, names, atoms, root)
			candidate.SuccessfulSamples = result.SuccessfulSamples
			for _, event := range data.Events {
				if recompiled(event.Replay, consumer) {
					candidate.ConsumerRecompiled++
				}
			}
			if result.SourceDirty || result.SourceHead == "" || result.SourceHead != data.Metadata.Head {
				candidate.Constraints = append(candidate.Constraints, "source dirty/revision differs from capture; history alignment requires investigation")
				if candidate.Status != "rejected" {
					candidate.Status = "requires investigation"
				}
			}
			if candidate.Status != "rejected" {
				if err := candidateHistory(ctx, data, root, depDir, &candidate, fset, dep, changes); err != nil {
					return nil, err
				}
			}
			result.Candidates = append(result.Candidates, candidate)
		}
	}
	if err := analyzeIncoming(ctx, data, root, depDir, result, options, result.CacheEvidence); err != nil {
		return nil, err
	}
	slices.SortStableFunc(result.Candidates, func(left, right Candidate) int {
		rank := func(status string) int {
			if status == "requires validation" {
				return 0
			}
			if status == "requires investigation" {
				return 1
			}
			return 2
		}
		if order := rank(left.Status) - rank(right.Status); order != 0 {
			return order
		}
		if left.ConditionalEvents != right.ConditionalEvents {
			return right.ConditionalEvents - left.ConditionalEvents
		}
		if left.OnCriticalPath != right.OnCriticalPath {
			return right.OnCriticalPath - left.OnCriticalPath
		}
		if len(left.Declarations) != len(right.Declarations) {
			return len(left.Declarations) - len(right.Declarations)
		}
		if order := strings.Compare(left.Consumer, right.Consumer); order != 0 {
			return order
		}
		return strings.Compare(strings.Join(left.Roots, ","), strings.Join(right.Roots, ","))
	})
	return result, nil
}

func makeCandidate(data *Dataset, dep, use *checkedSource, fset *token.FileSet, inspection *Inspection, seed, roots, names []string, atoms map[string][]string, root string) Candidate {
	candidate := Candidate{Roots: seed, Consumer: inspection.Consumer, Declarations: names, Imports: declarationImports(dep, names), Status: "requires validation"}
	members := make(map[string]bool)
	for _, name := range names {
		members[name] = true
		declaration := dep.declarations[name]
		if function, ok := declaration.node.(*ast.FuncDecl); ok && function.Body == nil {
			candidate.Constraints = append(candidate.Constraints, "assembly/external function bodies require moving associated non-Go inputs; unsupported automatically")
		}
		candidate.Edges = append(candidate.Edges, DeclarationDetail{Name: name, Kind: declaration.kind, Exported: declaration.exported, Dependencies: declaration.dependencies, Position: fset.Position(declaration.node.Pos()).String()})
	}
	slug := strings.Map(func(char rune) rune {
		if unicode.IsLetter(char) || unicode.IsDigit(char) {
			return unicode.ToLower(char)
		}
		return '_'
	}, packageName(inspection.Provider)+"_"+seed[0])
	candidate.Destination = inspection.ModulePath + "/internal/" + slug
	if inspection.ModulePath == "" {
		candidate.Constraints = append(candidate.Constraints, "module root/destination visibility is unknown")
	}
	for _, name := range roots {
		if !members[name] {
			candidate.RemainingReferences = append(candidate.RemainingReferences, name)
		}
	}
	if len(candidate.RemainingReferences) > 0 {
		candidate.Reasons = append(candidate.Reasons, "consumer still references provider declarations; no consumer -> provider edge disappears")
	}
	if !cohesiveGroup(dep, atoms, names) {
		candidate.Reasons = append(candidate.Reasons, "required declarations are disconnected; no cohesive extraction established")
	}
	if len(names) > maximumGroupDeclarations {
		candidate.Reasons = append(candidate.Reasons, "broad closure exceeds bounded extraction scope; moving most of a package is not a small architectural candidate")
	}
	var retained []string
	for name, declaration := range dep.declarations {
		if members[name] {
			continue
		}
		retained = append(retained, name)
		for _, other := range declaration.dependencies {
			if members[other] && !dep.declarations[other].exported {
				candidate.UnexportedBoundary = append(candidate.UnexportedBoundary, name+" -> "+other)
			}
		}
	}
	sort.Strings(candidate.UnexportedBoundary)
	if len(candidate.UnexportedBoundary) > 0 {
		candidate.Constraints = append(candidate.Constraints, "unexported incoming references need visibility/API changes")
	}
	for _, name := range names {
		if dep.declarations[name].kind == "var" || strings.HasPrefix(name, "init@") {
			candidate.Constraints = append(candidate.Constraints, "package state/initialization moves require order and shared-identity review")
			break
		}
	}
	if dep.initialization || len(dep.blankImports) > 0 {
		candidate.Constraints = append(candidate.Constraints, "provider initialization/blank imports may require retaining the original import for side effects")
	}
	if dep.pragmas || len(inspection.GeneratedFiles) > 0 {
		candidate.Constraints = append(candidate.Constraints, "generated/linkname/generator declarations need manual provenance review")
	}
	for _, file := range dep.files {
		for _, group := range file.Comments {
			for _, comment := range group.List {
				if strings.HasPrefix(comment.Text, "//go:build") || strings.HasPrefix(comment.Text, "// +build") {
					candidate.Constraints = append(candidate.Constraints, "build-tag/variant-specific declarations require matching file selection")
					break
				}
			}
		}
	}
	if !inspection.Complete {
		candidate.Constraints = append(candidate.Constraints, "incomplete type checking; unresolved references forbid a feasibility conclusion")
	}
	for _, file := range use.files {
		for _, spec := range file.Imports {
			if spec.Path.Value == fmt.Sprintf("%q", inspection.Provider) && spec.Name != nil && spec.Name.Name == "_" {
				candidate.Constraints = append(candidate.Constraints, "consumer blank import requires preserving initialization")
			}
		}
	}
	// An internal destination is only proposed inside the selected module.
	if inspection.ModulePath != "" {
		relative, err := filepath.Rel(data.Metadata.Repository, filepath.Dir(data.Metadata.TargetModuleFile))
		if err != nil || data.Metadata.TargetModuleFile == "" {
			relative = "."
		}
		destination := filepath.Join(root, relative, "internal", slug)
		if _, err := os.Stat(destination); err != nil {
			if !os.IsNotExist(err) {
				candidate.Constraints = append(candidate.Constraints, "destination accessibility unverified")
			}
		} else {
			candidate.Reasons = append(candidate.Reasons, "proposed destination exists; merging with its dependencies is unsupported")
		}
	}
	candidate.AddedEdges = append(candidate.AddedEdges, DependencyEdge{From: inspection.Consumer, To: candidate.Destination})
	for _, imported := range candidate.Imports {
		candidate.AddedEdges = append(candidate.AddedEdges, DependencyEdge{From: candidate.Destination, To: imported})
	}
	var incoming bool
	for _, name := range retained {
		for _, other := range dep.declarations[name].dependencies {
			if members[other] {
				incoming = true
			}
		}
	}
	if incoming {
		candidate.AddedEdges = append(candidate.AddedEdges, DependencyEdge{From: inspection.Provider, To: candidate.Destination})
	}
	graph := make(map[string][]string)
	for _, node := range data.Graph {
		graph[node.ImportPath] = append([]string(nil), node.Imports...)
	}
	if len(candidate.RemainingReferences) == 0 {
		graph[inspection.Consumer] = slices.DeleteFunc(graph[inspection.Consumer], func(name string) bool { return name == inspection.Provider })
		candidate.RemovedEdges = append(candidate.RemovedEdges, DependencyEdge{From: inspection.Consumer, To: inspection.Provider})
	}
	for _, edge := range candidate.AddedEdges {
		graph[edge.From] = append(graph[edge.From], edge.To)
	}
	for _, edge := range candidate.AddedEdges {
		if importReachable(graph, edge.To, edge.From) {
			candidate.Reasons = append(candidate.Reasons, "proposed import graph introduces a cycle")
			break
		}
	}
	if len(candidate.RemainingReferences) == 0 && importReachable(graph, inspection.Consumer, inspection.Provider) {
		candidate.Reasons = append(candidate.Reasons, "provider remains transitively reachable; extraction does not isolate consumer from provider changes")
	}
	if len(candidate.Reasons) > 0 {
		candidate.Status = "rejected"
	} else if len(candidate.Constraints) > 0 {
		candidate.Status = "requires investigation"
	}
	candidate.Constraints = append(candidate.Constraints, "other build variants, imported initialization, reflection and dynamic use require review; actual savings unverified")
	return candidate
}

func importReachable(graph map[string][]string, from, to string) bool {
	pending := []string{from}
	seen := make(map[string]bool)
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

func (report *Suggestions) Describe(data *Dataset, top int) string {
	var out strings.Builder
	fmt.Fprintf(&out, "Suggestions: %s (recommendations only; current-source, no edits/build replays)\n", data.label(report.Provider))
	fmt.Fprint(&out, incomingSummary(report, data, top))
	fmt.Fprintln(&out, "\nB. Reduce outgoing dependencies (existing consumer isolation checks)")
	var plausible, rejected int
	for _, candidate := range report.Candidates {
		if candidate.Status == "rejected" {
			rejected++
		} else {
			plausible++
		}
	}
	fmt.Fprintf(&out, "Measured: provider recompiled in %d/%d successful samples.\n", report.ProviderRecompiled, report.SuccessfulSamples)
	fmt.Fprintf(&out, "%d bounded candidates requiring validation/investigation; %d rejected groups. No verified refactoring savings.\n", plausible, rejected)
	if plausible == 0 {
		fmt.Fprintln(&out, "No small, structurally supported extraction removes the relevant consumer dependency. Rejected groups have no benefit/stability estimate.")
	}
	for _, candidate := range report.Candidates[:min(top, len(report.Candidates))] {
		fmt.Fprintf(&out, "\nCandidate: %s (%d declarations)\nDestination: %s\nConsumer: %s\nStatus: %s\n", strings.Join(candidate.Roots, ", "), len(candidate.Declarations), candidate.Destination, data.label(candidate.Consumer), candidate.Status)
		for _, reason := range candidate.Reasons[:min(2, len(candidate.Reasons))] {
			fmt.Fprintf(&out, "  Rejected: %s\n", reason)
		}
		if candidate.Status == "rejected" {
			if len(candidate.RemainingReferences) > 0 {
				fmt.Fprintf(&out, "  Still used: %s\n", strings.Join(candidate.RemainingReferences[:min(5, len(candidate.RemainingReferences))], ", "))
			}
			continue
		}
		fmt.Fprintf(&out, "  Measured: consumer recompiled %d/%d successful samples.\n", candidate.ConsumerRecompiled, candidate.SuccessfulSamples)
		fmt.Fprintf(&out, "  Git: %d provider source-change events; group changed %d, unchanged %d, unknown %d.\n", candidate.ProviderChangeEvents, candidate.GroupChangeEvents, candidate.IndependentlyChangingEvents, candidate.UnknownEvents)
		for _, edge := range candidate.RemovedEdges {
			fmt.Fprintf(&out, "  Potentially removes: %s -> %s\n", data.label(edge.From), data.label(edge.To))
		}
		fmt.Fprintf(&out, "  Conditional modeled events: %d; on selected critical path: %d.\n", candidate.ConditionalEvents, candidate.OnCriticalPath)
		var bounds []int64
		for _, event := range candidate.Events {
			if event.ConditionalAvoidance {
				bounds = append(bounds, event.MaximumSavingNS)
			}
		}
		if value, ok := median(bounds); ok {
			fmt.Fprintf(&out, "  Theoretical median fixed-DAG upper bound: %s (new package cost excluded).\n", seconds(value))
		} else if len(bounds) == 1 {
			fmt.Fprintf(&out, "  Single-event theoretical bound: %s; no median.\n", seconds(bounds[0]))
		} else {
			fmt.Fprintln(&out, "  No supported impact estimate.")
		}
		for _, constraint := range candidate.Constraints[:min(3, len(candidate.Constraints))] {
			fmt.Fprintf(&out, "  Constraint: %s\n", constraint)
		}
	}
	for _, diagnostic := range report.Diagnostics[:min(3, len(report.Diagnostics))] {
		fmt.Fprintf(&out, "Diagnostic: %s\n", shortDiagnostic(diagnostic))
	}
	fmt.Fprint(&out, complexitySummary(report.Complexity))
	fmt.Fprintln(&out, "Measured actions, observed inputs, resolved symbols, modeled graph changes and unverified savings are distinct. Full evidence: --out.")
	return out.String()
}
