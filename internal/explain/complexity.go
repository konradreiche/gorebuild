package explain

import (
	"fmt"
	"go/ast"
	"go/token"
	"path/filepath"
	"slices"
	"strings"
)

type DeclarationComplexity struct {
	Name     string `json:"name"`
	ASTNodes int    `json:"ast_nodes"`
	Position string `json:"position"`
}
type CompilationComplexity struct {
	CompilationSamples    int                     `json:"compilation_samples"`
	MedianToolTimeNS      int64                   `json:"median_recorded_tool_time_ns,omitempty"`
	MedianAvailable       bool                    `json:"median_available"`
	GeneratedFiles        []string                `json:"generated_files"`
	LargestDeclarations   []DeclarationComplexity `json:"largest_declarations"`
	GenericInstantiations int                     `json:"generic_instantiations"`
	CompositeLiterals     int                     `json:"composite_literals"`
	Status                string                  `json:"status"`
	Limitations           []string                `json:"limitations"`
}

func compilationComplexity(data *Dataset, target string, source *checkedSource, fset *token.FileSet) *CompilationComplexity {
	result := &CompilationComplexity{Status: "compiler profiling required; no specific optimization recommended", Limitations: []string{"AST size/counts are source inventory, not compiler-cost attribution.", "Generated code, generics and composite literals are investigation areas only; no speculative compiler optimizations.", "Package compilation work is not wall-clock savings."}}
	var times []int64
	for _, event := range data.Events {
		actions := buildActions(event.Replay, target)
		if len(actions) != 1 {
			continue
		}
		result.CompilationSamples++
		times = append(times, actions[0].ToolTimeNS)
	}
	result.MedianToolTimeNS, result.MedianAvailable = median(times)
	for _, file := range source.files {
		if ast.IsGenerated(file) {
			result.GeneratedFiles = append(result.GeneratedFiles, fset.Position(file.Pos()).Filename)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			switch node := node.(type) {
			case *ast.CompositeLit:
				result.CompositeLiterals++
			case *ast.Ident:
				if _, known := source.info.Instances[node]; known {
					result.GenericInstantiations++
				}
			}
			return true
		})
	}
	seen := make(map[ast.Node]bool)
	var names []string
	for name := range source.declarations {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		declaration := source.declarations[name]
		if seen[declaration.node] {
			continue
		}
		seen[declaration.node] = true
		var count int
		ast.Inspect(declaration.node, func(node ast.Node) bool {
			if node != nil {
				count++
			}
			return true
		})
		result.LargestDeclarations = append(result.LargestDeclarations, DeclarationComplexity{Name: name, ASTNodes: count, Position: fset.Position(declaration.node.Pos()).String()})
	}
	slices.SortFunc(result.LargestDeclarations, func(left, right DeclarationComplexity) int {
		if left.ASTNodes != right.ASTNodes {
			return right.ASTNodes - left.ASTNodes
		}
		return strings.Compare(left.Name, right.Name)
	})
	slices.Sort(result.GeneratedFiles)
	return result
}

func complexitySummary(complexity *CompilationComplexity) string {
	if complexity == nil {
		return "\nC. Reduce compilation complexity: source analysis unavailable; compiler profiling required.\n"
	}
	var out strings.Builder
	fmt.Fprintln(&out, "\nC. Reduce compilation complexity")
	if complexity.MedianAvailable {
		fmt.Fprintf(&out, "  Measured median target compilation tool work: %s (%d samples); not potential savings.\n", seconds(complexity.MedianToolTimeNS), complexity.CompilationSamples)
	}
	if len(complexity.LargestDeclarations) > 0 {
		item := complexity.LargestDeclarations[0]
		fmt.Fprintf(&out, "  Source inventory: largest declaration %s (%d AST nodes, %s); %d generated files, %d generic instantiations.\n", item.Name, item.ASTNodes, filepath.Base(item.Position), len(complexity.GeneratedFiles), complexity.GenericInstantiations)
	}
	fmt.Fprintf(&out, "  %s.\n", complexity.Status)
	return out.String()
}
