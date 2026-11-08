package explain

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

// Export builds are separate from historical replays and require permission.
// Cache-only go list -export -n can read available export data, but cannot run
// compilers when data is missing. go/packages may type-check required imported
// source as a fallback; any unresolved data remains an explicit limitation.
type TypeOptions struct{ AllowExportBuilds bool }

type DeclarationUse struct {
	Name               string   `json:"name"`
	Kind               string   `json:"kind"`
	References         int      `json:"references"`
	Position           string   `json:"position"`
	Dependencies       []string `json:"dependencies"`
	DirectDependencies []string `json:"direct_dependencies"`
}

type DeclarationDetail struct {
	Name         string   `json:"name"`
	Kind         string   `json:"kind"`
	Exported     bool     `json:"exported"`
	Dependencies []string `json:"direct_dependencies"`
	Position     string   `json:"position"`
}

type Inspection struct {
	ModulePath             string              `json:"module_path"`
	Provider               string              `json:"provider"`
	Consumer               string              `json:"consumer"`
	Complete               bool                `json:"complete"`
	Diagnostics            []string            `json:"diagnostics"`
	Declarations           []DeclarationUse    `json:"declarations"`
	DeclarationGraph       []DeclarationDetail `json:"reachable_declaration_graph"`
	Initialization         bool                `json:"provider_initialization"`
	BlankImports           []string            `json:"provider_blank_imports"`
	Pragmas                bool                `json:"provider_generation_or_link_pragmas"`
	GeneratedFiles         []string            `json:"generated_files"`
	State                  []string            `json:"package_state"`
	DependencyRoutes       []string            `json:"inferred_historical_routes"`
	Constraints            []string            `json:"constraints"`
	Flags                  []string            `json:"build_flags"`
	Environment            map[string]string   `json:"build_environment"`
	Toolchain              string              `json:"toolchain"`
	SuccessfulEvents       int                 `json:"successful_events"`
	ConsumerRecompilations int                 `json:"consumer_recompilations"`
	JointRecompilations    int                 `json:"joint_recompilations"`
	Text                   string              `json:"-"`
}

func InspectTypes(data *Dataset, dependency, consumer, root string, top int) (string, error) {
	result, err := InspectRelationship(context.Background(), data, dependency, consumer, root, top, TypeOptions{})
	if err != nil {
		return "", err
	}
	return result.Text, nil
}

func InspectRelationship(ctx context.Context, data *Dataset, dependency, consumer, root string, top int, options TypeOptions) (*Inspection, error) {
	result := &Inspection{Provider: dependency}
	text, err := inspectTypes(ctx, data, dependency, consumer, root, top, options, result)
	if err != nil {
		return nil, err
	}
	result.Text = text
	return result, nil
}

func loadSourcePair(ctx context.Context, data *Dataset, root, depDir, consumerDir string, options TypeOptions, result *Inspection) (*checkedSource, *checkedSource, *token.FileSet, error) {
	env := make(map[string]string)
	for _, entry := range os.Environ() {
		key, value, _ := strings.Cut(entry, "=")
		env[key] = value
	}
	for key, value := range data.Metadata.BuildEnvironment {
		env[key] = value
	}
	env["GOFLAGS"] = data.Metadata.GOFLAGS
	if data.Metadata.GOOS != "" {
		env["GOOS"] = data.Metadata.GOOS
	}
	if data.Metadata.GOARCH != "" {
		env["GOARCH"] = data.Metadata.GOARCH
	}
	env["GOWORK"] = data.Metadata.GOWORK
	if env["GOWORK"] == "" {
		env["GOWORK"] = "off"
	}
	if filepath.IsAbs(env["GOWORK"]) {
		relative, err := filepath.Rel(data.Metadata.Repository, env["GOWORK"])
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return nil, nil, nil, fmt.Errorf("captured external workspace cannot be relocated safely")
		}
		env["GOWORK"] = filepath.Join(root, relative)
	}
	// Do not delegate to an arbitrary external package/cache driver. Honor module
	// and build settings but forbid changing go.mod as a side effect of analysis.
	env["GOPACKAGESDRIVER"] = "off"
	env["GOCACHEPROG"] = ""
	flags := append([]string(nil), data.Metadata.BuildFlags...)
	if !strings.Contains(data.Metadata.GOFLAGS, "-mod=") && !hasModuleFlag(flags) {
		flags = append(flags, "-mod=readonly")
	}
	for _, flag := range append(strings.Fields(data.Metadata.GOFLAGS), flags...) {
		name, _, _ := strings.Cut(strings.TrimLeft(flag, "-"), "=")
		switch name {
		case "overlay", "modfile", "pkgdir", "toolexec":
			return nil, nil, nil, fmt.Errorf("source inspection cannot safely relocate captured -%s configuration", name)
		}
	}
	if !options.AllowExportBuilds {
		flags = append(flags, "-n")
	}
	var environment []string
	for key, value := range env {
		environment = append(environment, key+"="+value)
	}
	sort.Strings(environment)
	result.Flags = flags
	result.Environment = make(map[string]string)
	command := exec.CommandContext(ctx, "go", "env", "-json", "GOVERSION", "GOOS", "GOARCH", "GOFLAGS", "GOWORK", "GOMOD", "GOROOT", "CGO_ENABLED", "GOEXPERIMENT", "GOAMD64", "GOARM", "GOTOOLCHAIN", "GOCACHE")
	command.Dir = root
	command.Env = environment
	version, err := command.Output()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("inspect Go toolchain: %w", err)
	}
	if err := json.Unmarshal(version, &result.Environment); err != nil {
		return nil, nil, nil, err
	}
	result.Toolchain = result.Environment["GOVERSION"]
	cfg := &packages.Config{Context: ctx, Dir: root, Env: environment, BuildFlags: flags, Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles | packages.NeedImports | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedSyntax | packages.NeedTypesSizes | packages.NeedModule}
	// Only these roots request syntax/type information. Imports use real export
	// data or go/packages source fallback. NeedDeps would retain dependency ASTs
	// unnecessarily; resolving required imports is not loading the whole repo.
	loaded, err := packages.Load(cfg, depDir, consumerDir)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("load selected source packages: %s", shortDiagnostic(err.Error()))
	}
	if ctx.Err() != nil {
		return nil, nil, nil, ctx.Err()
	}
	var dep, use *packages.Package
	seenErrors := make(map[string]bool)
	packages.Visit(loaded, nil, func(pkg *packages.Package) {
		for _, problem := range pkg.Errors {
			text := problem.Error()
			if !seenErrors[text] {
				seenErrors[text] = true
				result.Diagnostics = append(result.Diagnostics, text)
			}
		}
	})
	for _, pkg := range loaded {
		if pkg.PkgPath == result.Provider {
			dep = pkg
		}
		if pkg.PkgPath == result.Consumer {
			use = pkg
		}
	}
	if dep == nil || use == nil {
		return nil, nil, nil, fmt.Errorf("selected package loading failed (provider/consumer module paths may differ): %s", strings.Join(result.Diagnostics[:min(3, len(result.Diagnostics))], "; "))
	}
	if dep.Module != nil {
		result.ModulePath = dep.Module.Path
	}
	result.Complete = len(result.Diagnostics) == 0 && !dep.IllTyped && !use.IllTyped && dep.Types != nil && use.Types != nil && dep.TypesInfo != nil && use.TypesInfo != nil
	if result.Complete {
		result.Complete = dep.Types.Complete() && use.Types.Complete()
	}
	if !result.Complete && len(result.Diagnostics) == 0 {
		result.Diagnostics = append(result.Diagnostics, "selected package type information is incomplete")
	}
	sort.Strings(result.Diagnostics)
	return checkedPackage(dep, result), checkedPackage(use, result), dep.Fset, nil
}

func hasModuleFlag(flags []string) bool {
	for _, flag := range flags {
		if flag == "-mod" || strings.HasPrefix(flag, "-mod=") {
			return true
		}
	}
	return false
}

func checkedPackage(pkg *packages.Package, result *Inspection) *checkedSource {
	source := &checkedSource{pkg: pkg.Types, files: pkg.Syntax, info: pkg.TypesInfo, declarations: make(map[string]*sourceDeclaration), objects: make(map[types.Object]string)}
	if source.info == nil {
		source.info = &types.Info{}
	}
	for _, problem := range pkg.Errors {
		source.errors = append(source.errors, problem.Error())
	}
	for _, file := range source.files {
		if ast.IsGenerated(file) {
			result.GeneratedFiles = append(result.GeneratedFiles, pkg.Fset.Position(file.Pos()).Filename)
		}
		for _, group := range file.Comments {
			for _, comment := range group.List {
				if strings.Contains(comment.Text, "go:generate") || strings.Contains(comment.Text, "go:linkname") {
					source.pragmas = true
				}
			}
		}
		for _, spec := range file.Imports {
			if spec.Name != nil && spec.Name.Name == "_" {
				source.blankImports = append(source.blankImports, spec.Path.Value)
			}
		}
	}
	indexDeclarations(source)
	return source
}
