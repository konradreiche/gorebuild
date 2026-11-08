package explain

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/konradreiche/gorebuild/internal/profile"
)

func sourceGit(ctx context.Context, root string, args ...string) ([]byte, error) {
	args = append([]string{"-c", "core.fsmonitor=false", "-c", "core.hooksPath=/dev/null", "-C", root}, args...)
	command := exec.CommandContext(ctx, "git", args...)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GIT_") {
			command.Env = append(command.Env, entry)
		}
	}
	return command.Output()
}

func gitBlob(ctx context.Context, root, revision, file string) ([]byte, error) {
	if strings.ContainsAny(revision+file, "\n\r") {
		return nil, fmt.Errorf("unsupported newline in Git object name")
	}
	command := exec.CommandContext(ctx, "git", "-c", "core.fsmonitor=false", "-C", root, "cat-file", "--batch")
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GIT_") {
			command.Env = append(command.Env, entry)
		}
	}
	command.Stdin = strings.NewReader(revision + ":" + file + "\n")
	output, err := command.Output()
	if err != nil {
		return nil, err
	}
	header, content, ok := bytes.Cut(output, []byte("\n"))
	if !ok {
		return nil, fmt.Errorf("invalid Git blob response")
	}
	if bytes.HasSuffix(header, []byte(" missing")) {
		return nil, nil
	}
	fields := strings.Fields(string(header))
	if len(fields) != 3 || fields[1] != "blob" {
		return nil, fmt.Errorf("not a Git blob")
	}
	size, err := strconv.Atoi(fields[2])
	if err != nil {
		return nil, err
	}
	if size < 0 || size > 16*1024*1024 || len(content) < size {
		return nil, fmt.Errorf("unsupported/truncated source blob")
	}
	return content[:size], nil
}

// Fingerprints are syntactic declaration evidence, not historical type checking.
// Import/build-directive changes conservatively affect declarations in a file.
func declarationFingerprints(content []byte) (map[string]string, error) {
	result := make(map[string]string)
	if content == nil {
		return result, nil
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "source.go", content, parser.ParseComments|parser.AllErrors)
	if err != nil {
		return nil, err
	}
	var context bytes.Buffer
	for _, spec := range file.Imports {
		if err := format.Node(&context, fset, spec); err != nil {
			return nil, err
		}
	}
	for _, group := range file.Comments {
		if group.Pos() > file.Package {
			continue
		}
		for _, comment := range group.List {
			context.WriteString(comment.Text)
		}
	}
	add := func(name string, node ast.Node) {
		var text bytes.Buffer
		text.Write(context.Bytes())
		if err := format.Node(&text, fset, node); err != nil {
			return
		}
		result[name] = fmt.Sprintf("%x", sha256.Sum256(text.Bytes()))
	}
	for _, node := range file.Decls {
		switch declaration := node.(type) {
		case *ast.FuncDecl:
			name := declaration.Name.Name
			if declaration.Recv != nil {
				name = receiverName(declaration.Recv.List[0].Type) + "." + name
			}
			add(name, declaration)
		case *ast.GenDecl:
			fingerprintSpecs(declaration, add)
		}
	}
	return result, nil
}

func fingerprintSpecs(declaration *ast.GenDecl, add func(string, ast.Node)) {
	for _, spec := range declaration.Specs {
		switch spec := spec.(type) {
		case *ast.ValueSpec:
			for _, name := range spec.Names {
				if declaration.Tok == token.CONST {
					add(name.Name, declaration)
				} else {
					add(name.Name, spec)
				}
			}
		case *ast.TypeSpec:
			add(spec.Name.Name, spec)
			var fields *ast.FieldList
			switch node := spec.Type.(type) {
			case *ast.StructType:
				fields = node.Fields
			case *ast.InterfaceType:
				fields = node.Methods
			}
			if fields == nil {
				continue
			}
			for _, field := range fields.List {
				for _, name := range field.Names {
					add(spec.Name.Name+"."+name.Name, spec)
				}
				if len(field.Names) == 0 {
					if identifier := embeddedIdentifier(field.Type); identifier != nil {
						add(spec.Name.Name+"."+identifier.Name, spec)
					}
				}
			}
		}
	}
}

func historicalDeclarations(ctx context.Context, root, revision string, files map[string]bool, cache map[string]map[string]string) (map[string]string, error) {
	result := make(map[string]string)
	for file := range files {
		key := revision + ":" + file
		values, known := cache[key]
		if !known {
			blob, err := gitBlob(ctx, root, revision, file)
			if err != nil {
				return nil, err
			}
			values, err = declarationFingerprints(blob)
			if err != nil {
				return nil, err
			}
			cache[key] = values
		}
		for name, value := range values {
			if previous, ok := result[name]; ok && previous != value {
				return nil, fmt.Errorf("ambiguous declarations across build variants")
			}
			result[name] = value
		}
	}
	return result, nil
}

func candidateHistory(ctx context.Context, data *Dataset, root, depDir string, candidate *Candidate, fset *token.FileSet, source *checkedSource, cache map[string]map[string]string) error {
	candidate.HistoryAnalyzed = true
	relative, err := filepath.Rel(root, depDir)
	if err != nil {
		return err
	}
	packageDir := filepath.ToSlash(relative)
	files := make(map[string]bool)
	for _, name := range candidate.Declarations {
		relative, err := filepath.Rel(root, fset.Position(source.declarations[name].node.Pos()).Filename)
		if err != nil {
			return err
		}
		if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			candidate.Constraints = append(candidate.Constraints, "generated source outside checkout prevents historical declaration attribution")
			candidate.Status = "requires investigation"
			return nil
		}
		files[filepath.ToSlash(relative)] = true
	}
	for _, event := range data.Events {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		compiled := recompiled(event.Replay, candidate.Consumer)
		var relevant bool
		eventFiles := make(map[string]bool)
		for file := range files {
			eventFiles[file] = true
		}
		for _, file := range event.Files {
			if path.Dir(file.Path) == packageDir && strings.HasSuffix(file.Path, ".go") && !strings.HasSuffix(file.Path, "_test.go") {
				relevant = true
				eventFiles[file.Path] = true
			}
		}
		// Sampled upstream-only events also matter: candidate external imports or
		// other consumer inputs can still invalidate it despite unchanged group text.
		if !relevant && !compiled {
			continue
		}
		evidence := CandidateEvent{Commit: event.Commit, ConsumerRecompiled: compiled}
		if relevant {
			candidate.ProviderChangeEvents++
		}
		if len(event.Parents) == 0 {
			evidence.Unknown = true
			evidence.Reason = "no comparable first parent"
		} else {
			compareHistoricalGroup(ctx, root, event.Parents[0], event.Commit, eventFiles, cache, candidate.Declarations, &evidence)
		}
		if relevant {
			if evidence.Unknown {
				candidate.UnknownEvents++
			} else if evidence.GroupChanged {
				candidate.GroupChangeEvents++
			} else {
				candidate.IndependentlyChangingEvents++
			}
		}
		modelCandidateEvent(data, &event, candidate, packageDir, &evidence)
		candidate.Events = append(candidate.Events, evidence)
	}
	if candidate.ConditionalEvents == 0 {
		candidate.Status = "requires investigation"
		candidate.Constraints = append(candidate.Constraints, "no measured event supports even conditional consumer-action avoidance")
	}
	return nil
}

func compareHistoricalGroup(ctx context.Context, root, parent, commit string, files map[string]bool, cache map[string]map[string]string, names []string, evidence *CandidateEvent) {
	before, err := historicalDeclarations(ctx, root, parent, files, cache)
	if err != nil {
		evidence.Unknown = true
		evidence.Reason = "historical parent source unavailable, ambiguous or unparseable"
		return
	}
	after, err := historicalDeclarations(ctx, root, commit, files, cache)
	if err != nil {
		evidence.Unknown = true
		evidence.Reason = "historical child source unavailable, ambiguous or unparseable"
		return
	}
	for _, name := range names {
		left, lok := before[name]
		right, rok := after[name]
		if !lok || !rok {
			evidence.Unknown = true
			evidence.Reason = "current group declarations not present in both historical snapshots"
			return
		}
		if left != right {
			evidence.GroupChanged = true
		}
	}
}

func modelCandidateEvent(data *Dataset, event *Event, candidate *Candidate, packageDir string, evidence *CandidateEvent) {
	if !evidence.ConsumerRecompiled || evidence.Unknown || evidence.GroupChanged {
		return
	}
	if reason := conditionalExclusion(data, event, candidate, packageDir); reason != "" {
		evidence.Reason = reason
		return
	}
	actions := buildActions(event.Replay, candidate.Consumer)
	if len(actions) != 1 {
		evidence.Reason = "multiple/missing consumer compilation nodes; single-action model withheld"
		return
	}
	bound, err := profile.EstimateCriticalPathSaving(event.Replay.Graph.ActionGraph, actions[0].ID)
	if err != nil {
		evidence.Reason = "invalid counterfactual graph"
		return
	}
	evidence.ConditionalAvoidance = true
	evidence.ActionID = actions[0].ID
	evidence.CriticalPathNS = bound.CurrentNS
	evidence.WithoutActionNS = bound.WithoutNS
	evidence.MaximumSavingNS = bound.MaximumSavingNS
	candidate.ConditionalEvents++
	if onPath(event.Replay.Graph, actions[0].ID) {
		candidate.OnCriticalPath++
	}
}

func conditionalExclusion(data *Dataset, event *Event, candidate *Candidate, packageDir string) string {
	if candidate.Consumer == data.Metadata.ImportPath && event.Replay.Graph.ExecutedLinkActions > 0 && !strings.Contains(strings.Join(event.Replay.BuildFlags, " ")+" "+data.Metadata.GOFLAGS, "-buildvcs=false") {
		return "main-package stamping/link inputs remain; compilation avoidance not established"
	}
	changed := data.changedImports(event)
	if len(changed) == 0 {
		return "no mapped changed build packages; VCS/unmodeled inputs are not attributed"
	}
	if len(event.BuildPackageDirs) != len(changed) {
		return "unmapped changed build packages"
	}
	for _, file := range event.Files {
		if file.Classification != "go_source" && file.Classification != "test_only" && !file.TestOnly {
			return "non-Go/unmodeled inputs need separate cache validation"
		}
	}
	var providerExposed bool
	for _, source := range changed {
		if source == data.ProviderName(candidate) || len(dependencyPath(event.Replay.Graph, source, data.ProviderName(candidate))) > 0 {
			providerExposed = true
		}
		if source == candidate.Consumer {
			return "consumer changed directly"
		}
		for _, imported := range candidate.Imports {
			if source == imported || len(dependencyPath(event.Replay.Graph, source, imported)) > 0 {
				return "moved group still depends on changed upstream inputs"
			}
		}
		// Another current consumer import may retain exposure to this change even
		// after its direct provider edge disappears. Do not claim downstream savings.
		for _, node := range data.Graph {
			if node.ImportPath != candidate.Consumer {
				continue
			}
			for _, imported := range node.Imports {
				if imported == data.ProviderName(candidate) {
					continue
				}
				if source == imported || len(dependencyPath(event.Replay.Graph, source, imported)) > 0 {
					return "another consumer import remains exposed to changed inputs"
				}
			}
		}
	}
	if !providerExposed {
		return "no observed changed-package route to provider; invalidation cause unresolved"
	}
	return ""
}

func (data *Dataset) ProviderName(candidate *Candidate) string {
	if len(candidate.RemovedEdges) > 0 {
		return candidate.RemovedEdges[0].To
	}
	return ""
}
