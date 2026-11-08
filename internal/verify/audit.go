package verify

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// Census all tracked Go sources, including tests/build-constrained files outside
// the executable closure. This is lexical evidence, not whole-repository typing.
func auditCallers(ctx context.Context, runner *engine, report *Report, options Options) (err error) {
	watched := make(map[string]bool)
	for _, edge := range report.DependencyChanges.Removed {
		watched[edge.From] = true
	}
	if len(watched) == 0 {
		return nil
	}
	for _, revision := range []*Revision{&report.Before, &report.After} {
		if err := auditRevision(ctx, runner, report, options, revision, watched); err != nil {
			return err
		}
	}
	report.Untested = append(report.Untested, "caller census is lexical; dot imports, reflection, generated/untracked source, external modules and inactive build configurations require review")
	return nil
}

func auditRevision(ctx context.Context, runner *engine, report *Report, options Options, revision *Revision, watched map[string]bool) (err error) {
	work, err := runner.worktree(ctx, revision.Commit, "caller-audit")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, work.close()) }()
	output, err := runner.git(ctx, "ls-tree", "-r", "--name-only", "-z", revision.Commit)
	if err != nil {
		return err
	}
	inTarget := make(map[string]bool)
	for _, node := range revision.Graph {
		inTarget[node.Dir] = true
	}
	extra := make(map[string]bool)
	for _, file := range strings.Split(string(output), "\x00") {
		if !strings.HasSuffix(file, ".go") {
			continue
		}
		syntax, err := parser.ParseFile(token.NewFileSet(), filepath.Join(work.path, filepath.FromSlash(file)), nil, parser.ParseComments)
		if err != nil {
			report.Untested = append(report.Untested, "caller census could not parse "+file)
			continue
		}
		sites := callerSites(syntax, file, watched, inTarget[path.Dir(file)])
		revision.CallerAudit = append(revision.CallerAudit, sites...)
		if len(sites) > 0 && !inTarget[path.Dir(file)] {
			extra[path.Dir(file)] = true
		}
	}
	for dir := range extra {
		// A distinct nested module is outside this fixed module configuration.
		relative, err := filepath.Rel(filepath.Join(work.path, runner.cwd), filepath.Join(work.path, dir))
		if err != nil {
			return err
		}
		args := append([]string{"test", "-count=1"}, runner.flags...)
		if options.RaceTests {
			args = append(args, "-race")
		}
		args = append(args, "./"+filepath.ToSlash(relative))
		_, check := runner.command(ctx, filepath.Join(work.path, runner.cwd), revision.correctnessCache, args...)
		revision.ExtraCallerTests = append(revision.ExtraCallerTests, *check)
		if check.Failure != "" {
			return fmt.Errorf("out-of-target caller correctness failed: %s", check.Log+check.Failure)
		}
	}
	return nil
}

func callerSites(file *ast.File, name string, watched map[string]bool, inTarget bool) []CallerSite {
	var result []CallerSite
	for _, spec := range file.Imports {
		imported, err := strconv.Unquote(spec.Path.Value)
		if err != nil || !watched[imported] {
			continue
		}
		alias := path.Base(imported)
		if spec.Name != nil {
			alias = spec.Name.Name
		}
		symbols := make(map[string]bool)
		ast.Inspect(file, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			identifier, ok := selector.X.(*ast.Ident)
			if ok && identifier.Name == alias {
				symbols[selector.Sel.Name] = true
			}
			return true
		})
		site := CallerSite{File: name, ImportedPackage: imported, InTarget: inTarget, TestFile: strings.HasSuffix(name, "_test.go"), Status: "lexical qualified references; selected-configuration tests checked"}
		for _, group := range file.Comments {
			for _, comment := range group.List {
				if strings.HasPrefix(comment.Text, "//go:build") || strings.HasPrefix(comment.Text, "// +build") {
					site.BuildConstraint = true
				}
			}
		}
		for symbol := range symbols {
			site.Symbols = append(site.Symbols, symbol)
		}
		slices.Sort(site.Symbols)
		result = append(result, site)
	}
	return result
}
