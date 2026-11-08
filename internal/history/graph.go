package history

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

type listedPackage struct {
	ImportPath    string
	Dir           string
	Imports       []string
	ImportMap     map[string]string
	DepOnly       bool
	EmbedFiles    []string
	EmbedPatterns []string
	Module        *struct {
		GoMod   string
		Replace *struct{ GoMod string }
	}
	CgoFiles, CFiles, CXXFiles, MFiles, HFiles, FFiles, SFiles, SwigFiles, SwigCXXFiles, SysoFiles []string
}

// Graph discovery uses a separate cache from measured build phases.
func loadGraph(ctx context.Context, cwd, cache string, options Options) ([]PackageNode, string, error) {
	return loadGraphWithEnv(ctx, cwd, cache, options, os.Environ())
}

func loadGraphWithEnv(ctx context.Context, cwd, cache string, options Options, env []string) ([]PackageNode, string, error) {
	args := append([]string{"list", "-deps", "-json"}, options.BuildFlags...)
	args = append(args, options.Target)
	output, err := runGoWithEnv(ctx, cwd, cache, options.GoBinary, env, args...)
	if err != nil {
		return nil, "", err
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	var nodes []PackageNode
	var roots []string
	for {
		var pkg listedPackage
		if err := decoder.Decode(&pkg); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, "", fmt.Errorf("decode go list: %w", err)
		}
		if !pkg.DepOnly {
			roots = append(roots, pkg.ImportPath)
		}
		var imports []string
		for _, imported := range pkg.Imports {
			if imported == "C" {
				continue
			}
			if resolved, ok := pkg.ImportMap[imported]; ok {
				imported = resolved
			}
			imports = append(imports, imported)
		}
		sort.Strings(imports)
		var buildFiles []string
		for _, group := range [][]string{pkg.CgoFiles, pkg.CFiles, pkg.CXXFiles, pkg.MFiles, pkg.HFiles, pkg.FFiles, pkg.SFiles, pkg.SwigFiles, pkg.SwigCXXFiles, pkg.SysoFiles} {
			buildFiles = append(buildFiles, group...)
		}
		sort.Strings(buildFiles)
		var moduleFile string
		if pkg.Module != nil {
			moduleFile = pkg.Module.GoMod
			if pkg.Module.Replace != nil {
				moduleFile = pkg.Module.Replace.GoMod
			}
		}
		nodes = append(nodes, PackageNode{
			ImportPath: pkg.ImportPath, Dir: pkg.Dir, Imports: slices.Compact(imports),
			EmbedFiles: pkg.EmbedFiles, EmbedPatterns: pkg.EmbedPatterns, BuildFiles: slices.Compact(buildFiles), ModuleGoMod: moduleFile,
		})
	}
	if len(roots) != 1 {
		return nil, "", fmt.Errorf("history expects exactly one package, go list returned %d roots", len(roots))
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ImportPath < nodes[j].ImportPath })
	return nodes, roots[0], nil
}

func runGo(ctx context.Context, cwd, cache, executable string, args ...string) ([]byte, error) {
	return runGoWithEnv(ctx, cwd, cache, executable, os.Environ(), args...)
}

func runGoWithEnv(ctx context.Context, cwd, cache, executable string, env []string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Dir = cwd
	for _, entry := range env {
		if !strings.HasPrefix(entry, "GOCACHE=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "GOCACHE="+cache)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, fmt.Errorf("go %s: %w\n%s", strings.Join(args, " "), err, stderr.String())
	}
	return output, nil
}

func repositoryDir(root, dir string) (string, bool) {
	if dir == "" {
		return "", false
	}
	// Resolve symlinked checkouts the same way Git resolves its top-level path.
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		resolved = dir // Historical paths need not exist in the current checkout.
	}
	dir = resolved
	relative, err := filepath.Rel(root, dir)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(relative), true
}

type graphImpact struct {
	consumers map[string][]string
	paths     map[string][]string
}

// All nodes are in the target closure. Reach and paths are unweighted: they
// describe potential invalidation, not compiler execution or build latency.
func analyzeGraph(nodes []PackageNode) (graphImpact, error) {
	reverse := make(map[string][]string, len(nodes))
	for _, node := range nodes {
		if _, exists := reverse[node.ImportPath]; exists {
			return graphImpact{}, fmt.Errorf("duplicate package %s", node.ImportPath)
		}
		reverse[node.ImportPath] = nil
	}
	for _, node := range nodes {
		for _, imported := range node.Imports {
			if _, exists := reverse[imported]; !exists {
				return graphImpact{}, fmt.Errorf("missing imported package %s", imported)
			}
			reverse[imported] = append(reverse[imported], node.ImportPath)
		}
	}
	for _, consumers := range reverse {
		sort.Strings(consumers)
	}
	impact := graphImpact{consumers: make(map[string][]string), paths: make(map[string][]string)}
	state := make(map[string]uint8)
	var visit func(string) error
	visit = func(pkg string) error {
		if state[pkg] == 1 {
			return fmt.Errorf("cycle in package graph at %s", pkg)
		}
		if state[pkg] == 2 {
			return nil
		}
		state[pkg] = 1
		reachable := make(map[string]bool)
		longest := []string{pkg}
		for _, consumer := range reverse[pkg] {
			if err := visit(consumer); err != nil {
				return err
			}
			reachable[consumer] = true
			for _, descendant := range impact.consumers[consumer] {
				reachable[descendant] = true
			}
			if len(impact.paths[consumer])+1 > len(longest) {
				longest = append([]string{pkg}, impact.paths[consumer]...)
			}
		}
		for consumer := range reachable {
			impact.consumers[pkg] = append(impact.consumers[pkg], consumer)
		}
		sort.Strings(impact.consumers[pkg])
		impact.paths[pkg] = longest
		state[pkg] = 2
		return nil
	}
	for _, node := range nodes {
		if err := visit(node.ImportPath); err != nil {
			return graphImpact{}, err
		}
	}
	return impact, nil
}
