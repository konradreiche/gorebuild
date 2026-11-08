package explain

import (
	"context"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type sourceDeclaration struct {
	name         string
	kind         string
	exported     bool
	node         ast.Node
	object       types.Object
	dependencies []string
}

type checkedSource struct {
	pkg            *types.Package
	files          []*ast.File
	info           *types.Info
	declarations   map[string]*sourceDeclaration
	objects        map[types.Object]string
	positions      map[token.Pos]string
	errors         []string
	initialization bool
	blankImports   []string
	pragmas        bool
}

func inspectTypes(ctx context.Context, data *Dataset, dependency, consumerQuery, sourceRoot string, top int, options TypeOptions, result *Inspection) (string, error) {
	if top < 1 {
		top = 5
	}
	consumer, err := data.ResolvePackage(consumerQuery)
	if err != nil {
		return "", err
	}
	if dependency == consumer {
		return "", fmt.Errorf("consumer and dependency must differ")
	}
	result.Consumer = consumer
	for _, event := range data.Events {
		if event.Replay.Measured() {
			result.SuccessfulEvents++
		}
		if recompiled(event.Replay, consumer) {
			result.ConsumerRecompilations++
		}
		if recompiled(event.Replay, dependency) && recompiled(event.Replay, consumer) {
			result.JointRecompilations++
		}
	}
	if result.JointRecompilations == 0 {
		return "", fmt.Errorf("typed inspection requires a sample where both selected packages actually recompiled")
	}
	root, err := filepath.Abs(sourceRoot)
	if err != nil {
		return "", err
	}
	depDir, err := sourceDirectory(data, dependency, root)
	if err != nil {
		return "", err
	}
	consumerDir, err := sourceDirectory(data, consumer, root)
	if err != nil {
		return "", err
	}
	dep, use, fset, err := loadSourcePair(ctx, data, root, depDir, consumerDir, options, result)
	if err != nil {
		return "", err
	}
	var direct, dot, blank bool
	for _, file := range use.files {
		for _, spec := range file.Imports {
			name, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return "", err
			}
			if name == dependency {
				direct = true
				if spec.Name != nil {
					dot = spec.Name.Name == "."
					blank = spec.Name.Name == "_"
				}
			}
		}
	}
	if !direct {
		if result.Complete {
			return "", fmt.Errorf("current consumer source does not directly import %s; only targeted direct relationships are inspected", dependency)
		}
		result.Diagnostics = append(result.Diagnostics, "direct current-source import could not be verified with incomplete type/source information")
	}
	uses := make(map[string]int)
	for _, obj := range use.info.Uses {
		if method, ok := obj.(*types.Func); ok {
			obj = method.Origin()
		}
		if obj.Pkg() != dep.pkg || !obj.Exported() {
			continue
		}
		if name, known := declarationName(dep, obj); known {
			uses[name]++
		}
	}
	// Method/field selections are not always represented in Uses on older Go.
	for expression, selection := range use.info.Selections {
		if use.info.Uses[expression.Sel] != nil {
			continue
		}
		obj := selection.Obj()
		if method, ok := obj.(*types.Func); ok {
			obj = method.Origin()
		}
		if obj.Pkg() == dep.pkg && obj.Exported() {
			if name, known := declarationName(dep, obj); known {
				uses[name]++
			}
		}
	}
	var out strings.Builder
	fmt.Fprintf(&out, "\nTargeted source/type inspection: %s -> %s\n", data.label(consumer), data.label(dependency))
	fmt.Fprintf(&out, "Source root: %s\n", root)
	fmt.Fprintf(&out, "Measured: consumer recompiled in %d/%d successful events; provider and consumer co-recompiled in %d (not causal attribution).\n", result.ConsumerRecompilations, result.SuccessfulEvents, result.JointRecompilations)
	fmt.Fprintf(&out, "Resolved current import: %s -> %s.\n", data.label(consumer), data.label(dependency))
	var providerRoute []string
	var routeEvents int
	for _, event := range data.Events {
		if !recompiled(event.Replay, consumer) {
			continue
		}
		route := dependencyPath(event.Replay.Graph, dependency, consumer)
		if len(route) > 1 {
			routeEvents++
			if len(providerRoute) == 0 {
				providerRoute = route
			}
		}
	}
	for index := range providerRoute {
		providerRoute[index] = data.label(providerRoute[index])
	}
	if routeEvents > 0 {
		fmt.Fprintf(&out, "Historical DAG route: %s (%d events); possible propagation, not invalidation proof.\n", strings.Join(providerRoute, " -> "), routeEvents)
	}
	fmt.Fprintln(&out, "Current source checkout only—not historical source or cache-invalidation proof. No generators, package initialization or target links run.")
	if options.AllowExportBuilds {
		fmt.Fprintln(&out, "Export-data compilation explicitly permitted for dependencies; no historical replay runs.")
	} else {
		fmt.Fprintln(&out, "Cache-only package loading (-n): no compilers run; uncached imports may require source type checking. --allow-export-builds permits compiler export data when needed.")
	}
	revision, err := exec.Command("git", "-c", "core.fsmonitor=false", "-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		fmt.Fprintln(&out, "Source revision unavailable; alignment with capture HEAD is unverified.")
	} else {
		fmt.Fprintf(&out, "Source HEAD: %s; capture HEAD: %s\n", strings.TrimSpace(string(revision)), data.Metadata.Head)
	}
	status, err := exec.Command("git", "-c", "core.fsmonitor=false", "-C", root, "status", "--porcelain", "--untracked-files=no").Output()
	if err != nil {
		fmt.Fprintln(&out, "Tracked source cleanliness unavailable.")
	} else {
		fmt.Fprintf(&out, "Tracked checkout dirty: %t (untracked/generated files may still affect source selection).\n", len(status) > 0)
	}
	fmt.Fprintf(&out, "Source selection: %s/%s; flags %v; GOFLAGS=%q; GOWORK=%q.\n", result.Environment["GOOS"], result.Environment["GOARCH"], result.Flags, result.Environment["GOFLAGS"], result.Environment["GOWORK"])
	fmt.Fprintf(&out, "Toolchain: %s; captured: %s (current-source analysis).\n", result.Toolchain, data.Metadata.GoVersion)
	partial := !result.Complete
	fmt.Fprintf(&out, "Type coverage: go/packages, 0 fabricated opaque imports; %d diagnostics. Partial: %t.\n", len(result.Diagnostics), partial)
	fmt.Fprintln(&out, "Resolved: exported declaration uses in consumer (object bindings, not runtime call counts):")
	var names []string
	for name := range uses {
		names = append(names, name)
	}
	sort.Strings(names)
	for index, name := range names {
		declaration := dep.declarations[name]
		position := fset.Position(declaration.node.Pos())
		closure := declarationClosure(dep, name)
		result.Declarations = append(result.Declarations, DeclarationUse{Name: name, Kind: declaration.kind, References: uses[name], Position: position.String(), Dependencies: closure, DirectDependencies: declaration.dependencies})
		if index >= top {
			continue
		}
		fmt.Fprintf(&out, "  %s (%s): %d references; %s:%d\n", name, declaration.kind, uses[name], filepath.Base(position.Filename), position.Line)
		if len(closure) > 0 {
			fmt.Fprintf(&out, "    Declaration dependencies: %s", strings.Join(closure[:min(top, len(closure))], ", "))
			if len(closure) > top {
				fmt.Fprintf(&out, " ... (%d more; increase --top to inspect)", len(closure)-top)
			}
			fmt.Fprintln(&out)
		}
	}
	if len(names) == 0 {
		fmt.Fprintln(&out, "  (none resolved; this is NOT proof the dependency is unused/removable)")
	}
	if len(names) > top {
		fmt.Fprintf(&out, "  ... (%d more referenced exported declarations)\n", len(names)-top)
	}
	result.Initialization = dep.initialization
	result.BlankImports = dep.blankImports
	result.Pragmas = dep.pragmas
	pending := append([]string(nil), names...)
	reachable := make(map[string]bool)
	for len(pending) > 0 {
		name := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if reachable[name] {
			continue
		}
		reachable[name] = true
		declaration := dep.declarations[name]
		pending = append(pending, declaration.dependencies...)
		result.DeclarationGraph = append(result.DeclarationGraph, DeclarationDetail{Name: name, Kind: declaration.kind, Exported: declaration.exported, Dependencies: declaration.dependencies, Position: fset.Position(declaration.node.Pos()).String()})
	}
	sort.Slice(result.DeclarationGraph, func(i, j int) bool { return result.DeclarationGraph[i].Name < result.DeclarationGraph[j].Name })
	fmt.Fprintln(&out, "Constraints on removal/narrowing:")
	if dep.initialization || blank || len(dep.blankImports) > 0 {
		fmt.Fprintln(&out, "  Initialization/package variables/blank imports may carry side effects independently of direct exported references.")
	}
	if dot {
		fmt.Fprintln(&out, "  Dot import: unqualified references must be resolved; simple selector counts are insufficient.")
	}
	if dep.pragmas {
		fmt.Fprintln(&out, "  go:generate/go:linkname pragmas require separate build/link investigation; no generators were run.")
	}
	fmt.Fprintln(&out, "  Methods, receiver types, interfaces and unexported declaration closures constrain any boundary change; references are static bindings, not a complete runtime call graph.")
	for name, declaration := range dep.declarations {
		if declaration.kind == "var" {
			result.State = append(result.State, name)
		}
	}
	sort.Strings(result.State)
	if len(result.State) > 0 {
		fmt.Fprintf(&out, "  Provider package state: %s", strings.Join(result.State[:min(top, len(result.State))], ", "))
		if len(result.State) > top {
			fmt.Fprintf(&out, " ... (%d more in structured details)", len(result.State)-top)
		}
		fmt.Fprintln(&out)
	}
	fmt.Fprintln(&out, "  Interface satisfaction, reflection and dynamic dispatch can create implicit uses outside the identifier closure; complete type checking is not a removability proof.")
	if len(result.GeneratedFiles) > 0 {
		fmt.Fprintf(&out, "  %d selected generated source files; generation inputs/processes remain unverified.\n", len(result.GeneratedFiles))
	}
	fmt.Fprintln(&out, "  Adding a dependency -> consumer back-edge while consumer -> dependency remains would create an import cycle. No new boundary or split is proposed.")
	if len(names) > 0 {
		fmt.Fprintf(&out, "  %d directly referenced exported declarations identify a bounded relationship worth inspecting, not proof of a removable/narrowable dependency.\n", len(names))
	}
	if partial {
		fmt.Fprintln(&out, "  Incomplete type checking forbids a removal conclusion. Resolved object bindings are partial evidence; unresolved symbols/initialization may retain the dependency.")
	}
	fmt.Fprintln(&out, "Inferred: recorded dependency routes permit change propagation; resolved symbols do not establish cache invalidation causality.")
	routes := make(map[string]int)
	for _, event := range data.Events {
		if !recompiled(event.Replay, consumer) {
			continue
		}
		for _, changed := range data.changedImports(&event) {
			route := dependencyPath(event.Replay.Graph, changed, consumer)
			if len(route) < 2 {
				continue
			}
			for index := range route {
				route[index] = data.label(route[index])
			}
			routes[strings.Join(route, " -> ")]++
		}
	}
	for route, count := range routes {
		result.DependencyRoutes = append(result.DependencyRoutes, fmt.Sprintf("%s (%d events)", route, count))
	}
	sort.Strings(result.DependencyRoutes)
	for _, route := range result.DependencyRoutes[:min(top, len(result.DependencyRoutes))] {
		fmt.Fprintf(&out, "  %s\n", route)
	}
	fmt.Fprintln(&out, "Unverified: removability/narrowing and achievable compilation savings. No refactoring recommendation is made.")
	result.Constraints = []string{"Static bindings are not a complete runtime call graph.", "Initialization, package state, methods and interfaces constrain narrowing.", "Generated code and generation inputs require separate review.", "Adding a back-edge creates an import cycle while the direct edge remains.", "No removability or cache-causality conclusion follows from source references."}
	for _, diagnostic := range result.Diagnostics[:min(3, len(result.Diagnostics))] {
		fmt.Fprintf(&out, "    Diagnostic: %s\n", shortDiagnostic(diagnostic))
	}
	return out.String(), nil
}

func shortDiagnostic(message string) string {
	message = strings.Join(strings.Fields(message), " ")
	if len(message) > 400 {
		return message[:400] + " ... (full diagnostic in --out)"
	}
	return message
}

func sourceDirectory(data *Dataset, pkg, root string) (string, error) {
	for _, node := range data.Graph {
		if node.ImportPath != pkg {
			continue
		}
		relative, err := filepath.Rel(data.Metadata.Repository, node.Dir)
		if err != nil {
			return "", err
		}
		if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("typed inspection only supports source packages inside the captured repository")
		}
		dir := filepath.Join(root, relative)
		resolved, err := filepath.EvalSymlinks(dir)
		if err != nil {
			return "", err
		}
		resolvedRoot, err := filepath.EvalSymlinks(root)
		if err != nil {
			return "", err
		}
		relative, err = filepath.Rel(resolvedRoot, resolved)
		if err != nil {
			return "", err
		}
		if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("source directory escapes supplied root")
		}
		return resolved, nil
	}
	return "", fmt.Errorf("no captured source directory for %s", pkg)
}

func (source *checkedSource) addDeclaration(name, kind string, node ast.Node, identifier *ast.Ident) {
	obj := source.info.Defs[identifier]
	if obj == nil {
		return
	}
	source.declarations[name] = &sourceDeclaration{name: name, kind: kind, node: node, object: obj, exported: obj.Exported()}
	source.objects[obj] = name
	if source.positions == nil {
		source.positions = make(map[token.Pos]string)
	}
	source.positions[obj.Pos()] = name
}

func (source *checkedSource) indexGeneralDeclaration(declaration *ast.GenDecl) {
	for _, spec := range declaration.Specs {
		switch spec := spec.(type) {
		case *ast.ValueSpec:
			if declaration.Tok == token.VAR && len(spec.Values) > 0 {
				source.initialization = true
			}
			for _, name := range spec.Names {
				source.addDeclaration(name.Name, declaration.Tok.String(), spec, name)
			}
		case *ast.TypeSpec:
			source.addDeclaration(spec.Name.Name, "type", spec, spec.Name)
			var fields *ast.FieldList
			kind := "field"
			switch node := spec.Type.(type) {
			case *ast.StructType:
				fields = node.Fields
			case *ast.InterfaceType:
				fields = node.Methods
				kind = "interface method"
			}
			if fields == nil {
				continue
			}
			for _, field := range fields.List {
				if len(field.Names) == 0 {
					source.indexEmbeddedField(spec.Name.Name, kind, field)
				}
				for _, name := range field.Names {
					source.addDeclaration(spec.Name.Name+"."+name.Name, kind, field, name)
				}
			}
		}
	}
}

func (source *checkedSource) indexEmbeddedField(owner, kind string, field *ast.Field) {
	identifier := embeddedIdentifier(field.Type)
	if identifier == nil {
		return
	}
	if _, ok := source.info.Defs[identifier].(*types.Var); ok {
		source.addDeclaration(owner+"."+identifier.Name, kind, field, identifier)
	}
}

func indexDeclarations(source *checkedSource) {
	for _, file := range source.files {
		for _, node := range file.Decls {
			switch declaration := node.(type) {
			case *ast.FuncDecl:
				name := declaration.Name.Name
				if name == "init" {
					source.initialization = true
					name = fmt.Sprintf("init@%d", declaration.Pos())
				}
				if declaration.Recv != nil {
					name = receiverName(declaration.Recv.List[0].Type) + "." + name
				}
				source.addDeclaration(name, "function/method", declaration, declaration.Name)
			case *ast.GenDecl:
				source.indexGeneralDeclaration(declaration)
			}
		}
	}
	for name, declaration := range source.declarations {
		seen := make(map[string]bool)
		ast.Inspect(declaration.node, func(node ast.Node) bool {
			if identifier, ok := node.(*ast.Ident); ok {
				obj := source.info.Uses[identifier]
				if method, ok := obj.(*types.Func); ok {
					obj = method.Origin()
				}
				if dependency, known := declarationName(source, obj); known && dependency != name {
					seen[dependency] = true
				}
			}
			return true
		})
		for dependency := range seen {
			declaration.dependencies = append(declaration.dependencies, dependency)
		}
		sort.Strings(declaration.dependencies)
	}
}

func declarationName(source *checkedSource, obj types.Object) (string, bool) {
	if obj == nil {
		return "", false
	}
	if name, ok := source.objects[obj]; ok {
		return name, true
	}
	// Instantiated generic fields/methods retain their declaration position.
	if obj.Pkg() != source.pkg || obj.Pos() == token.NoPos {
		return "", false
	}
	name, ok := source.positions[obj.Pos()]
	return name, ok
}

func embeddedIdentifier(expression ast.Expr) *ast.Ident {
	switch expression := expression.(type) {
	case *ast.Ident:
		return expression
	case *ast.SelectorExpr:
		return expression.Sel
	case *ast.StarExpr:
		return embeddedIdentifier(expression.X)
	case *ast.IndexExpr:
		return embeddedIdentifier(expression.X)
	case *ast.IndexListExpr:
		return embeddedIdentifier(expression.X)
	}
	return nil
}

func receiverName(expression ast.Expr) string {
	switch expression := expression.(type) {
	case *ast.Ident:
		return expression.Name
	case *ast.StarExpr:
		return receiverName(expression.X)
	case *ast.IndexExpr:
		return receiverName(expression.X)
	case *ast.IndexListExpr:
		return receiverName(expression.X)
	}
	return "unknown-receiver"
}

func declarationClosure(source *checkedSource, name string) []string {
	seen := map[string]bool{name: true}
	var result []string
	var visit func(string)
	visit = func(name string) {
		declaration := source.declarations[name]
		if declaration == nil {
			return
		}
		for _, dependency := range declaration.dependencies {
			if seen[dependency] {
				continue
			}
			seen[dependency] = true
			child := source.declarations[dependency]
			label := dependency
			if !child.exported {
				label += " [unexported]"
			}
			result = append(result, label)
			visit(dependency)
		}
	}
	visit(name)
	sort.Strings(result)
	return result
}
