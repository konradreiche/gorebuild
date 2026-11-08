package history

import (
	"path"
	"slices"
	"sort"
	"strings"
)

// Rules are deliberately conservative. Only Go source and potentially relevant
// files with known package owners seed static reach. Unknown/config-wide changes
// are retained without inventing a target-wide invalidation set.
func classifyFile(name string, nodes map[string]PackageNode, knownDirs map[string]bool) FileChange {
	change := FileChange{Path: name}
	if strings.HasSuffix(name, ".go") {
		change.PackageDir = path.Dir(name)
		change.TestOnly = strings.HasSuffix(name, "_test.go")
		change.Classification = "go_source"
		change.Reason = "Go source; historical build constraints are not reconstructed"
		if change.TestOnly {
			change.Classification = "test_only"
			change.Reason = "test files are not compiled by go build"
		}
		return change
	}
	// Check real inputs before filename heuristics: a README or testdata file
	// can be embedded and must not be dismissed as documentation/test-only.
	for dir, node := range nodes {
		relative := name
		if dir != "." {
			var inside bool
			relative, inside = strings.CutPrefix(name, dir+"/")
			if !inside {
				continue
			}
		}
		if slices.Contains(node.EmbedFiles, relative) || matchesEmbed(node.EmbedPatterns, relative) {
			change.AffectedPackageDirs = append(change.AffectedPackageDirs, dir)
			change.Reason = "matches current embed inputs/patterns; historical embed rules may differ"
		} else if slices.Contains(node.BuildFiles, relative) {
			change.AffectedPackageDirs = append(change.AffectedPackageDirs, dir)
			if change.Reason == "" {
				change.Reason = "native build input in current go list graph; historical configuration may differ"
			}
		}
	}
	if len(change.AffectedPackageDirs) > 0 {
		sort.Strings(change.AffectedPackageDirs)
		change.Classification = "potential_build"
		return change
	}
	base := strings.ToLower(path.Base(name))
	switch base {
	case "go.mod", "go.sum", "go.work", "go.work.sum", "modules.txt", ".go-version", ".tool-versions", "makefile", "gnumakefile", "dockerfile", "cmakelists.txt":
		change.Classification = "potential_build"
		change.Reason = "build/dependency/generation configuration; target-wide impact is uncertain and not inferred"
		return change
	}
	switch strings.ToLower(path.Ext(name)) {
	case ".s", ".syso", ".c", ".h", ".cc", ".cpp", ".cxx", ".m", ".mm", ".f", ".f90", ".swig", ".swigcxx", ".inc", ".proto", ".y", ".yacc", ".l", ".lex":
		change.Classification = "potential_build"
		change.Reason = "possible native/generated input; ownership is directory-based, generation is not executed by go build"
		for dir := path.Dir(name); ; dir = path.Dir(dir) {
			if knownDirs[dir] {
				change.AffectedPackageDirs = []string{dir}
				break
			}
			if dir == "." {
				break
			}
		}
		return change
	case ".md", ".rst", ".adoc":
		change.Classification = "unlikely"
		change.Reason = "documentation-looking file, not a current embed input; historical rules may differ"
		return change
	}
	if strings.HasPrefix(base, "readme") || strings.HasPrefix(base, "license") || base == "notice" || base == "authors" || base == "changelog" || strings.HasPrefix(name, ".github/") || strings.Contains("/"+name, "/testdata/") {
		change.Classification = "unlikely"
		change.Reason = "documentation, CI or test data outside current embed inputs; may still affect historical generation or VCS linking"
		return change
	}
	change.Classification = "unknown"
	change.Reason = "no reliable mapping to current build inputs; historical embedding/generation/configuration may differ"
	return change
}

func matchesEmbed(patterns []string, name string) bool {
	for _, pattern := range patterns {
		pattern = strings.TrimPrefix(pattern, "all:")
		// An embed pattern can name a directory and include its descendants.
		for candidate := name; candidate != "."; candidate = path.Dir(candidate) {
			if matched, _ := path.Match(pattern, candidate); matched {
				return true
			}
		}
	}
	return false
}

func countClassification(counts *ClassificationCounts, category string) {
	switch category {
	case "unlikely":
		counts.Unlikely++
	case "potential_build":
		counts.PotentialBuild++
	case "unknown":
		counts.Unknown++
	}
}
