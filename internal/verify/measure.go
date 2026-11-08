package verify

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const probeName = "GorebuildVerificationProbe"

func makeProbe(packageName string) Probe {
	content := "// Controlled additive compilation input; no initialization or runtime calls.\npackage " + packageName + "\n\nfunc " + probeName + "() string { return \"gorebuild-verify-input-v1\" }\n"
	return Probe{File: "zz_gorebuild_verify_probe.go", Content: content, SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(content))), Purpose: "same additive exported function in both revisions; changes package/export compilation inputs, leaves existing runtime behavior unchanged"}
}

func probeCollision(file *ast.File) bool {
	for _, node := range file.Decls {
		switch node := node.(type) {
		case *ast.FuncDecl:
			if node.Recv == nil && node.Name.Name == probeName {
				return true
			}
		case *ast.GenDecl:
			for _, spec := range node.Specs {
				if probeSpecCollision(spec) {
					return true
				}
			}
		}
	}
	return false
}

func probeSpecCollision(spec ast.Spec) bool {
	switch spec := spec.(type) {
	case *ast.TypeSpec:
		return spec.Name.Name == probeName
	case *ast.ValueSpec:
		for _, name := range spec.Names {
			if name.Name == probeName {
				return true
			}
		}
	}
	return false
}

func applyProbe(work *tree, revision *Revision, probe Probe) error {
	directory := filepath.Join(work.path, filepath.FromSlash(revision.PackageDirectory))
	if !inside(work.path, directory) {
		return fmt.Errorf("controlled package directory escapes worktree")
	}
	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return err
	}
	if !inside(work.path, resolved) {
		return fmt.Errorf("controlled package symlink escapes worktree")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(directory, entry.Name()), nil, 0)
		if err != nil {
			return err
		}
		if probeCollision(file) {
			return fmt.Errorf("controlled probe symbol already exists")
		}
	}
	file, err := os.OpenFile(filepath.Join(directory, probe.File), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.WriteString(probe.Content); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

func measure(ctx context.Context, runner *engine, revision *Revision, label, target string, probe Probe) *Measurement {
	result := &Measurement{Revision: label, Commit: revision.Commit, Status: "failed", CachePolicy: "fresh initially empty independent cache; cold -> verified warm no-op -> identical probe -> incremental"}
	if err := measureBuilds(ctx, runner, revision, label, target, probe, result); err != nil {
		result.Status = "failed"
		result.Reason = err.Error()
	} else {
		result.Status = "measured"
	}
	return result
}

func measureBuilds(ctx context.Context, runner *engine, revision *Revision, label, target string, probe Probe, result *Measurement) (err error) {
	work, err := runner.worktree(ctx, revision.Commit, "benchmark-"+label)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, work.close()) }()
	cache, err := runner.cache("benchmark-" + label)
	if err != nil {
		return err
	}
	defer os.RemoveAll(cache)
	result.Cold = runner.capture(ctx, work, cache, target)
	if result.Cold.Failure != "" {
		return fmt.Errorf("%s cold build: %s", label, result.Cold.Failure)
	}
	if result.Cold.Run.Graph.ExecutedBuildActions == 0 {
		return fmt.Errorf("%s initially empty cache executed no build actions", label)
	}
	result.Warm = runner.capture(ctx, work, cache, target)
	if result.Warm.Failure != "" {
		return fmt.Errorf("%s warm build: %s", label, result.Warm.Failure)
	}
	if result.Warm.Run.Graph.ExecutedActions != 0 {
		return fmt.Errorf("%s warm build was not a no-op (%d executed nodes)", label, result.Warm.Run.Graph.ExecutedActions)
	}
	if err := applyProbe(work, revision, probe); err != nil {
		return err
	}
	result.ProbeSHA256 = probe.SHA256
	result.Incremental = runner.capture(ctx, work, cache, target)
	if result.Incremental.Failure != "" {
		return fmt.Errorf("%s incremental build: %s", label, result.Incremental.Failure)
	}
	var probeCompiled bool
	for _, action := range result.Incremental.Run.Graph.ExecutedActionDetails {
		if action.CompileCommands > 0 {
			result.Recompiled = append(result.Recompiled, action.Package)
			if action.Package == revision.ChangedPackage {
				probeCompiled = true
			}
		}
		if action.LinkCommands > 0 {
			result.Linked = append(result.Linked, action.Package)
		}
	}
	sort.Strings(result.Recompiled)
	sort.Strings(result.Linked)
	if !probeCompiled {
		return fmt.Errorf("%s probe did not execute the controlled package's Go compiler action", label)
	}
	return nil
}
