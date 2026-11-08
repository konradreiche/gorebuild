package explain

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

type FactoryBoundary struct {
	Owner        string   `json:"owner"`
	Factory      string   `json:"factory"`
	Position     string   `json:"position"`
	ResultType   string   `json:"neutral_result_type"`
	Dependencies []string `json:"construction_imports"`
}

type CompositionSite struct {
	Consumer string `json:"consumer"`
	Function string `json:"called_target_function"`
	Position string `json:"position"`
	Complete bool   `json:"complete"`
}

func compositionCalls(provider, consumer *checkedSource, fset *token.FileSet, complete bool) []CompositionSite {
	var result []CompositionSite
	for _, file := range consumer.files {
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			identifier := calledIdentifier(call.Fun)
			if identifier == nil {
				return true
			}
			object, ok := consumer.info.Uses[identifier].(*types.Func)
			if !ok || object.Pkg() != provider.pkg || object.Type().(*types.Signature).Recv() != nil {
				return true
			}
			result = append(result, CompositionSite{Consumer: consumer.pkg.Path(), Function: object.Name(), Position: fset.Position(call.Pos()).String(), Complete: complete})
			return true
		})
	}
	return result
}

func calledIdentifier(expression ast.Expr) *ast.Ident {
	switch expression := expression.(type) {
	case *ast.Ident:
		return expression
	case *ast.SelectorExpr:
		return expression.Sel
	case *ast.IndexExpr:
		return calledIdentifier(expression.X)
	case *ast.IndexListExpr:
		return calledIdentifier(expression.X)
	}
	return nil
}

func incomingBoundaries(data *Dataset, source *checkedSource, fset *token.FileSet, inspection *Inspection, dependency *IncomingDependency, graph map[string][]string) []IncomingBoundary {
	var roots []string
	for _, requirement := range dependency.Requirements {
		roots = append(roots, requirement.Declaration)
	}
	extraction := IncomingBoundary{Strategy: "extract import-dependent declarations", Status: "requires validation", TypeAnalysisComplete: inspection.Complete}
	extraction.Declarations = closedGroup(source, declarationAtoms(source), roots)
	members := make(map[string]bool)
	for _, name := range extraction.Declarations {
		members[name] = true
	}
	for name, declaration := range source.declarations {
		if members[name] {
			continue
		}
		for _, other := range declaration.dependencies {
			if members[other] {
				extraction.RemainingDeclarations = append(extraction.RemainingDeclarations, name)
				break
			}
		}
	}
	sort.Strings(extraction.RemainingDeclarations)
	if len(extraction.RemainingDeclarations) > 0 {
		extraction.Reasons = append(extraction.Reasons, "retained target declarations call/use extracted group; target -> adapter -> dependency retains exposure")
	}
	if len(roots) == 0 {
		extraction.Reasons = append(extraction.Reasons, "no resolved declaration use (possibly side-effect-only import); no extraction inferred")
	}
	if len(extraction.Declarations) == len(source.declarations) {
		extraction.Reasons = append(extraction.Reasons, "whole-package relocation does not establish a new useful boundary")
	}
	if len(extraction.Declarations) > maximumGroupDeclarations {
		extraction.Reasons = append(extraction.Reasons, "dependency closure includes broad type/method families, not a bounded extraction")
	}
	if !cohesiveGroup(source, declarationAtoms(source), extraction.Declarations) {
		extraction.Reasons = append(extraction.Reasons, "dependent declarations do not form one cohesive closed group")
	}
	if len(extraction.Reasons) == 0 {
		destination := inspection.ModulePath + "/internal/" + packageName(inspection.Provider) + "_" + packageName(dependency.Import) + "_adapter"
		if inspection.ModulePath == "" {
			extraction.Reasons = append(extraction.Reasons, "module-root internal destination visibility is unknown")
		} else {
			location := filepath.Join(filepath.Dir(inspection.Environment["GOMOD"]), "internal", packageName(inspection.Provider)+"_"+packageName(dependency.Import)+"_adapter")
			if _, err := os.Stat(location); err != nil {
				if !os.IsNotExist(err) {
					extraction.Reasons = append(extraction.Reasons, "adapter destination cannot be inspected")
				}
			} else {
				extraction.Reasons = append(extraction.Reasons, "adapter destination exists; merging dependency graphs is unsupported")
			}
		}
		imports := declarationImports(source, extraction.Declarations)
		for _, imported := range imports {
			extraction.AddedEdges = append(extraction.AddedEdges, DependencyEdge{From: destination, To: imported})
		}
		for _, node := range data.Graph {
			if slices.Contains(node.Imports, inspection.Provider) {
				extraction.AddedEdges = append(extraction.AddedEdges, DependencyEdge{From: node.ImportPath, To: destination})
			}
		}
		extraction.RequiredAPIChanges = append(extraction.RequiredAPIChanges, "move complete declaration/type/method families; update all callers to import adapter; no forwarding wrapper may remain in target")
	}
	extraction = validateIncomingGraph(extraction, source, inspection, dependency.Import, graph)
	inversion := factoryInversion(source, fset, inspection, dependency.Import)
	inversion = validateIncomingGraph(inversion, source, inspection, dependency.Import, graph)
	return []IncomingBoundary{extraction, inversion}
}

func factoryInversion(source *checkedSource, fset *token.FileSet, inspection *Inspection, dependency string) IncomingBoundary {
	boundary := IncomingBoundary{Strategy: "inject neutral factory results at composition root", Status: "requires investigation", TypeAnalysisComplete: inspection.Complete}
	covered := make(map[token.Pos]bool)
	owners := make(map[string]bool)
	for name, declaration := range source.declarations {
		function, ok := declaration.node.(*ast.FuncDecl)
		if !ok || function.Recv != nil || function.Body == nil || (function.Type.TypeParams != nil && len(function.Type.TypeParams.List) > 0) {
			continue
		}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			identifier := calledIdentifier(call.Fun)
			if identifier == nil {
				return true
			}
			factory, ok := source.info.Uses[identifier].(*types.Func)
			if !ok || !objectDependency(factory, dependency) {
				return true
			}
			signature, ok := factory.Type().(*types.Signature)
			if !ok || signature.Recv() != nil || signature.Results().Len() != 1 {
				return true
			}
			result := signature.Results().At(0).Type()
			if _, ok := result.Underlying().(*types.Interface); !ok {
				return true
			}
			if typeDependsOn(result, dependency, make(map[types.Type]bool)) {
				return true
			}
			if !movableConstruction(source, function, call) {
				return true
			}
			ast.Inspect(call, func(node ast.Node) bool {
				if id, ok := node.(*ast.Ident); ok && objectDependency(source.info.Uses[id], dependency) {
					covered[id.Pos()] = true
				}
				return true
			})
			boundary.FactoryCalls = append(boundary.FactoryCalls, FactoryBoundary{Owner: name, Factory: factory.Name(), Position: fset.Position(call.Pos()).String(), ResultType: types.TypeString(result, func(pkg *types.Package) string { return pkg.Path() }), Dependencies: callImports(source, call)})
			owners[name] = true
			return true
		})
	}
	for identifier, object := range source.info.Uses {
		if objectDependency(object, dependency) && !covered[identifier.Pos()] {
			boundary.Reasons = append(boundary.Reasons, "dependency appears outside supported neutral-result construction calls (types, state, methods or other expressions)")
			break
		}
	}
	for owner := range owners {
		boundary.Declarations = append(boundary.Declarations, owner)
	}
	sort.Strings(boundary.Declarations)
	slices.SortFunc(boundary.FactoryCalls, func(left, right FactoryBoundary) int { return strings.Compare(left.Position, right.Position) })
	if len(boundary.FactoryCalls) == 0 {
		boundary.Reasons = append(boundary.Reasons, "no wholly isolated factory calls returning dependency-neutral interfaces")
	}
	boundary.Constraints = append(boundary.Constraints, "construction/registration/initialization order, feature gates and error paths must be preserved; tests and out-of-scope callers must be migrated")
	return boundary
}

func movableConstruction(source *checkedSource, function *ast.FuncDecl, call *ast.CallExpr) bool {
	parameters := make(map[types.Object]bool)
	if function.Type.Params != nil {
		for _, field := range function.Type.Params.List {
			for _, name := range field.Names {
				parameters[source.info.Defs[name]] = true
			}
		}
	}
	movable := true
	ast.Inspect(call, func(node ast.Node) bool {
		identifier, ok := node.(*ast.Ident)
		if !ok {
			return true
		}
		object := source.info.Uses[identifier]
		if object == nil || object.Pkg() == nil || object.Pkg() != source.pkg {
			return true
		}
		if _, ok := object.(*types.PkgName); ok {
			return true
		}
		if _, ok := object.(*types.Var); ok && !parameters[object] {
			movable = false
		}
		if object.Parent() == source.pkg.Scope() && !object.Exported() {
			movable = false
		}
		return true
	})
	return movable
}

func callImports(source *checkedSource, call *ast.CallExpr) []string {
	found := make(map[string]bool)
	ast.Inspect(call, func(node ast.Node) bool {
		identifier, ok := node.(*ast.Ident)
		if !ok {
			return true
		}
		object := source.info.Uses[identifier]
		if object != nil && object.Pkg() != nil && object.Pkg() != source.pkg {
			found[object.Pkg().Path()] = true
		}
		return true
	})
	var names []string
	for name := range found {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Named types are not neutral just because their underlying interface is: their
// method signatures/type arguments may expose the very dependency being removed.
func typeDependsOn(value types.Type, dependency string, seen map[types.Type]bool) bool {
	if value == nil || seen[value] {
		return false
	}
	seen[value] = true
	switch value := value.(type) {
	case *types.Named:
		if value.Obj().Pkg() != nil && value.Obj().Pkg().Path() == dependency {
			return true
		}
		if value.TypeArgs() != nil {
			for index := range value.TypeArgs().Len() {
				if typeDependsOn(value.TypeArgs().At(index), dependency, seen) {
					return true
				}
			}
		}
		return typeDependsOn(value.Underlying(), dependency, seen)
	case *types.Alias:
		if value.Obj().Pkg() != nil && value.Obj().Pkg().Path() == dependency {
			return true
		}
		return typeDependsOn(types.Unalias(value), dependency, seen)
	case *types.Pointer:
		return typeDependsOn(value.Elem(), dependency, seen)
	case *types.Slice:
		return typeDependsOn(value.Elem(), dependency, seen)
	case *types.Array:
		return typeDependsOn(value.Elem(), dependency, seen)
	case *types.Map:
		return typeDependsOn(value.Key(), dependency, seen) || typeDependsOn(value.Elem(), dependency, seen)
	case *types.Chan:
		return typeDependsOn(value.Elem(), dependency, seen)
	case *types.Signature:
		return typeDependsOn(value.Params(), dependency, seen) || typeDependsOn(value.Results(), dependency, seen)
	case *types.Tuple:
		for index := range value.Len() {
			if typeDependsOn(value.At(index).Type(), dependency, seen) {
				return true
			}
		}
	case *types.Interface:
		value.Complete()
		for index := range value.NumMethods() {
			if typeDependsOn(value.Method(index).Type(), dependency, seen) {
				return true
			}
		}
		for index := range value.NumEmbeddeds() {
			if typeDependsOn(value.EmbeddedType(index), dependency, seen) {
				return true
			}
		}
	case *types.Struct:
		for index := range value.NumFields() {
			if typeDependsOn(value.Field(index).Type(), dependency, seen) {
				return true
			}
		}
	case *types.TypeParam:
		return typeDependsOn(value.Constraint(), dependency, seen)
	case *types.Union:
		for index := range value.Len() {
			if typeDependsOn(value.Term(index).Type(), dependency, seen) {
				return true
			}
		}
	}
	return false
}

func validateIncomingGraph(boundary IncomingBoundary, source *checkedSource, inspection *Inspection, dependency string, graph map[string][]string) IncomingBoundary {
	if !inspection.Complete {
		boundary.Constraints = append(boundary.Constraints, "incomplete type analysis forbids structural support")
	}
	if source.initialization || len(source.blankImports) > 0 {
		boundary.Constraints = append(boundary.Constraints, "package initialization/state semantics require review even when the compile-time edge can disappear")
	}
	if source.pragmas || len(inspection.GeneratedFiles) > 0 {
		boundary.Constraints = append(boundary.Constraints, "generated/linkname/generator inputs need provenance review")
	}
	for _, name := range boundary.Declarations {
		declaration := source.declarations[name]
		if declaration != nil && declaration.kind == "var" {
			boundary.Constraints = append(boundary.Constraints, "shared package state requires ownership/API review")
			break
		}
	}
	retained := append([]string(nil), graph[inspection.Provider]...)
	retained = slices.DeleteFunc(retained, func(name string) bool { return name == dependency })
	for _, imported := range retained {
		if importReachable(graph, imported, dependency) {
			boundary.Reasons = append(boundary.Reasons, "other target imports still reach dependency; removing only the direct edge does not isolate target")
			break
		}
	}
	if len(boundary.Reasons) > 0 {
		boundary.Status = "rejected"
		return boundary
	}
	// Inversion edges are filled only after a typed caller/composition site has
	// been established. No unsupported callback insertion counts as isolation.
	if boundary.Strategy == "inject neutral factory results at composition root" {
		return boundary
	}
	proposed := cloneImportGraph(graph)
	proposed[inspection.Provider] = retained
	for _, edge := range boundary.AddedEdges {
		proposed[edge.From] = append(proposed[edge.From], edge.To)
	}
	if incomingCycle(boundary.AddedEdges, proposed) || importReachable(proposed, inspection.Provider, dependency) {
		boundary.Status = "rejected"
		boundary.Reasons = append(boundary.Reasons, "proposed boundary introduces a cycle or retains target exposure")
		return boundary
	}
	boundary.StructurallySupported = inspection.Complete
	boundary.RemovedEdges = []DependencyEdge{{From: inspection.Provider, To: dependency}}
	if !inspection.Complete || len(boundary.Constraints) > 0 {
		boundary.Status = "requires investigation"
	}
	boundary.Constraints = append(boundary.Constraints, "other platforms/build tags and consumer visibility must be checked; no source edits or timing verification performed")
	return boundary
}

func cloneImportGraph(graph map[string][]string) map[string][]string {
	result := make(map[string][]string)
	for name, imports := range graph {
		result[name] = append([]string(nil), imports...)
	}
	return result
}

func incomingCycle(edges []DependencyEdge, graph map[string][]string) bool {
	for _, edge := range edges {
		if importReachable(graph, edge.To, edge.From) {
			return true
		}
	}
	return false
}

func finishFactoryBoundary(boundary *IncomingBoundary, sites []CompositionSite, target, dependency string, graph map[string][]string) {
	if boundary.Status == "rejected" || boundary.Strategy != "inject neutral factory results at composition root" {
		return
	}
	owners := make(map[string]bool)
	for _, name := range boundary.Declarations {
		owners[name] = false
	}
	for _, site := range sites {
		if _, known := owners[site.Function]; !known || !site.Complete {
			continue
		}
		owners[site.Function] = true
		edge := DependencyEdge{From: site.Consumer, To: dependency}
		if !slices.Contains(boundary.AddedEdges, edge) {
			boundary.AddedEdges = append(boundary.AddedEdges, edge)
		}
	}
	for _, known := range owners {
		if !known {
			boundary.Status = "rejected"
			boundary.Reasons = append(boundary.Reasons, "no complete typed composition-root call site for each affected constructor")
			return
		}
	}
	proposed := cloneImportGraph(graph)
	proposed[target] = slices.DeleteFunc(proposed[target], func(name string) bool { return name == dependency })
	for _, edge := range boundary.AddedEdges {
		proposed[edge.From] = append(proposed[edge.From], edge.To)
	}
	if incomingCycle(boundary.AddedEdges, proposed) || importReachable(proposed, target, dependency) {
		boundary.Status = "rejected"
		boundary.Reasons = append(boundary.Reasons, "inversion introduces import cycle or retains transitive exposure")
		return
	}
	boundary.StructurallySupported = boundary.TypeAnalysisComplete
	boundary.RemovedEdges = []DependencyEdge{{From: target, To: dependency}}
	for _, factory := range boundary.FactoryCalls {
		boundary.RequiredAPIChanges = append(boundary.RequiredAPIChanges, fmt.Sprintf("%s: inject %s instead of constructing %s here; move construction/options to existing caller", factory.Owner, factory.ResultType, factory.Factory))
	}
	boundary.RequiredAPIChanges = append(boundary.RequiredAPIChanges, "remove all in-target default factory calls/import; a forwarding or compatibility constructor retaining the dependency defeats isolation")
}
