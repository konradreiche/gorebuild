package explain

import (
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/konradreiche/gorebuild/internal/profile"
)

type Options struct {
	Commit  string
	Package string
	Top     int
}

func Describe(data *Dataset, options Options) (string, error) {
	if (options.Commit == "") == (options.Package == "") {
		return "", fmt.Errorf("select exactly one of --commit or --package")
	}
	if options.Top < 1 {
		options.Top = 5
	}
	if options.Commit != "" {
		var found *Event
		for index := range data.Events {
			if strings.HasPrefix(data.Events[index].Commit, options.Commit) {
				if found != nil {
					return "", fmt.Errorf("ambiguous commit prefix %q", options.Commit)
				}
				found = &data.Events[index]
			}
		}
		if found == nil {
			return "", fmt.Errorf("commit %q not found in this history", options.Commit)
		}
		return describeEvent(data, found, options.Top)
	}
	pkg, err := data.ResolvePackage(options.Package)
	if err != nil {
		return "", err
	}
	return describePackage(data, pkg, options.Top), nil
}

// Local paths are relative to the captured repository, not the caller's cwd.
// Resolving a copied artifact never needs the original source checkout.
func (data *Dataset) ResolvePackage(query string) (string, error) {
	dir := filepath.ToSlash(filepath.Clean(query))
	if filepath.IsAbs(query) {
		relative, err := filepath.Rel(data.Metadata.Repository, query)
		if err != nil {
			return "", err
		}
		dir = filepath.ToSlash(relative)
	}
	matches := make(map[string]bool)
	add := func(name, relative string) {
		if name != "" && (query == name || (relative != "" && dir == relative)) {
			matches[name] = true
		}
	}
	for _, node := range data.Graph {
		var relative string
		if node.Dir != "" {
			if rel, err := filepath.Rel(data.Metadata.Repository, node.Dir); err != nil {
				return "", err
			} else if rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				relative = filepath.ToSlash(rel)
			}
		}
		add(node.ImportPath, relative)
	}
	for _, pkg := range data.Packages {
		add(pkg.ImportPath, pkg.Dir)
	}
	for _, event := range data.Events {
		if event.Replay == nil {
			continue
		}
		for relative, name := range event.Replay.PackageImports {
			add(name, relative)
		}
	}
	if len(matches) != 1 {
		return "", fmt.Errorf("package %q resolves to %d import paths in this capture; use an unambiguous captured import path", query, len(matches))
	}
	for name := range matches {
		return name, nil
	}
	return "", fmt.Errorf("package not found")
}

func (data *Dataset) label(name string) string {
	for _, pkg := range data.Packages {
		if pkg.ImportPath == name {
			return pkg.Dir
		}
	}
	return name
}

func (data *Dataset) changedImports(event *Event) []string {
	var names []string
	for _, dir := range event.BuildPackageDirs {
		if event.Replay != nil && event.Replay.PackageImports[dir] != "" {
			names = append(names, event.Replay.PackageImports[dir])
			continue
		}
		if name, err := data.ResolvePackage(dir); err != nil {
			continue
		} else {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return slices.Compact(names)
}

func buildActions(replay *Replay, pkg string) []profile.ExecutedAction {
	var result []profile.ExecutedAction
	if !replay.Measured() {
		return result
	}
	for _, action := range replay.Graph.ExecutedActionDetails {
		if action.Mode == "build" && (pkg == "" || action.Package == pkg) {
			result = append(result, action)
		}
	}
	return result
}

func recompiled(replay *Replay, pkg string) bool {
	for _, action := range buildActions(replay, pkg) {
		if action.CompileCommands > 0 {
			return true
		}
	}
	return false
}

func onPath(graph profile.GraphSummary, id int) bool {
	for _, step := range graph.ExecutedActionChain {
		if step.ID == id && step.ElapsedNS > 0 {
			return true
		}
	}
	return false
}

// Follow the measured action DAG backwards from consumer to dependency. A
// route shows a build dependency, not which declaration caused invalidation.
func dependencyPath(graph profile.GraphSummary, dependency, consumer string) []string {
	byID := make(map[int]profile.Action, len(graph.ActionGraph))
	var roots []int
	for _, action := range graph.ActionGraph {
		byID[action.ID] = action
		if action.Package == consumer && action.Mode == "build" {
			roots = append(roots, action.ID)
		}
	}
	sort.Ints(roots)
	seen := make(map[int]bool)
	var visit func(int) []string
	visit = func(id int) []string {
		if seen[id] {
			return nil
		}
		seen[id] = true
		action := byID[id]
		if action.Package == dependency && action.Mode == "build" {
			return []string{dependency}
		}
		deps := slices.Clone(action.Deps)
		sort.Slice(deps, func(i, j int) bool {
			left := byID[deps[i]]
			right := byID[deps[j]]
			if left.Package != right.Package {
				return left.Package < right.Package
			}
			return left.ID < right.ID
		})
		for _, dep := range deps {
			if route := visit(dep); len(route) > 0 {
				if action.Mode == "build" && action.Package != "" && route[len(route)-1] != action.Package {
					route = append(route, action.Package)
				}
				return route
			}
		}
		return nil
	}
	for _, id := range roots {
		if route := visit(id); len(route) > 0 {
			return route
		}
	}
	return nil
}

func median(values []int64) (int64, bool) {
	if len(values) < 2 {
		return 0, false
	}
	ordered := slices.Clone(values)
	slices.Sort(ordered)
	middle := len(ordered) / 2
	value := ordered[middle]
	if len(ordered)%2 == 0 {
		value = ordered[middle-1] + (ordered[middle]-ordered[middle-1])/2
	}
	return value, true
}

func shortCommit(commit string) string { return commit[:min(12, len(commit))] }
func actionName(data *Dataset, action profile.ExecutedAction) string {
	name := data.label(action.Package)
	if action.Mode != "build" || name == "" {
		name = strings.TrimSpace(action.Mode + " " + name)
	}
	return fmt.Sprintf("%s [#%d]", name, action.ID)
}

func packageName(importPath string) string {
	base := path.Base(importPath)
	if strings.HasPrefix(base, "v") && len(base) > 1 && base[1] >= '0' && base[1] <= '9' {
		base = path.Base(path.Dir(importPath))
	}
	return base
}
