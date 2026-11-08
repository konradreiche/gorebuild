package explain

import (
	"go/ast"
	"go/token"
	"go/types"
	"sort"
	"strings"
)

// Groups are dependency closures, not arbitrary file partitions. Atomic type /
// method families and const/value specs prevent proposing illegal partial moves.
func declarationAtoms(source *checkedSource) map[string][]string {
	atoms := make(map[string][]string)
	families := make(map[string][]string)
	nodes := make(map[ast.Node][]string)
	for name, declaration := range source.declarations {
		nodes[declaration.node] = append(nodes[declaration.node], name)
		owner, _, member := strings.Cut(name, ".")
		if member {
			families[owner] = append(families[owner], name)
		}
	}
	for owner, names := range families {
		if source.declarations[owner] != nil {
			names = append(names, owner)
		}
		for _, name := range names {
			atoms[name] = append(atoms[name], names...)
		}
	}
	for _, names := range nodes {
		for _, name := range names {
			atoms[name] = append(atoms[name], names...)
		}
	}
	for _, file := range source.files {
		for _, node := range file.Decls {
			declaration, ok := node.(*ast.GenDecl)
			if !ok || declaration.Tok != token.CONST {
				continue
			}
			var names []string
			for _, spec := range declaration.Specs {
				value, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for _, name := range value.Names {
					if source.declarations[name.Name] != nil {
						names = append(names, name.Name)
					}
				}
			}
			for _, name := range names {
				atoms[name] = append(atoms[name], names...)
			}
		}
	}
	return atoms
}

func closedGroup(source *checkedSource, atoms map[string][]string, roots []string) []string {
	pending := append([]string(nil), roots...)
	seen := make(map[string]bool)
	var names []string
	for len(pending) > 0 {
		name := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		declaration := source.declarations[name]
		if seen[name] || declaration == nil {
			continue
		}
		seen[name] = true
		names = append(names, name)
		pending = append(pending, declaration.dependencies...)
		pending = append(pending, atoms[name]...)
	}
	sort.Strings(names)
	return names
}

func referencedDeclarations(provider, consumer *checkedSource) []string {
	found := make(map[string]bool)
	add := func(obj types.Object) {
		if method, ok := obj.(*types.Func); ok {
			obj = method.Origin()
		}
		if obj == nil || obj.Pkg() != provider.pkg || !obj.Exported() {
			return
		}
		if name, ok := declarationName(provider, obj); ok {
			found[name] = true
		}
	}
	for _, obj := range consumer.info.Uses {
		add(obj)
	}
	for _, selection := range consumer.info.Selections {
		add(selection.Obj())
	}
	var names []string
	for name := range found {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func declarationImports(source *checkedSource, names []string) []string {
	imports := make(map[string]bool)
	for _, name := range names {
		declaration := source.declarations[name]
		if declaration == nil {
			continue
		}
		ast.Inspect(declaration.node, func(node ast.Node) bool {
			identifier, ok := node.(*ast.Ident)
			if !ok {
				return true
			}
			obj := source.info.Uses[identifier]
			if obj != nil && obj.Pkg() != nil && obj.Pkg() != source.pkg {
				imports[obj.Pkg().Path()] = true
			}
			return true
		})
	}
	var result []string
	for name := range imports {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func cohesiveGroup(source *checkedSource, atoms map[string][]string, names []string) bool {
	if len(names) == 0 {
		return false
	}
	members := make(map[string]bool)
	adjacent := make(map[string][]string)
	for _, name := range names {
		members[name] = true
	}
	for _, name := range names {
		for _, other := range append(append([]string(nil), source.declarations[name].dependencies...), atoms[name]...) {
			if members[other] {
				adjacent[name] = append(adjacent[name], other)
				adjacent[other] = append(adjacent[other], name)
			}
		}
	}
	seen := make(map[string]bool)
	pending := []string{names[0]}
	for len(pending) > 0 {
		name := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if seen[name] {
			continue
		}
		seen[name] = true
		pending = append(pending, adjacent[name]...)
	}
	return len(seen) == len(names)
}
