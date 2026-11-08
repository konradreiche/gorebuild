package history

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

func configurationFile(name string) bool {
	switch path.Base(name) {
	case "go.mod", "go.sum", "go.work", "go.work.sum", "modules.txt", ".go-version", ".tool-versions":
		return true
	}
	return false
}

type configurationContext struct {
	files       map[string]string
	packageDirs []string
	mainDir     string
	moduleRoots []string
	vendor      bool
}

// Review both historical revisions, not just HEAD. An unrelated nested module
// cannot influence a GOWORK=off build unless it is imported/replaced or creates
// a module boundary above a selected package. Ambiguity is never ignored.
func reviewConfiguration(ctx context.Context, tree, cwd, cache string, config replayConfig, nodes []PackageNode, event Event, revision string) []ConfigurationChange {
	var changed []string
	for _, file := range event.Files {
		if configurationFile(file.Path) {
			changed = append(changed, file.Path)
		}
	}
	if len(changed) == 0 {
		return nil
	}
	moduleContext, err := replayConfigurationContext(ctx, tree, cwd, cache, config, nodes)
	var reviews []ConfigurationChange
	for _, name := range changed {
		review := ConfigurationChange{Path: name, Revision: revision}
		if err != nil {
			review.Relevance = "uncertain"
			review.Reason = "cannot establish effective module configuration: " + err.Error()
		} else {
			review.Relevance, review.Reason = moduleContext.relevance(name)
		}
		reviews = append(reviews, review)
	}
	return reviews
}

func replayConfigurationContext(ctx context.Context, tree, cwd, cache string, config replayConfig, nodes []PackageNode) (*configurationContext, error) {
	output, err := runGoWithEnv(ctx, cwd, cache, config.toolchain, config.env, "env", "GOMOD")
	if err != nil {
		return nil, err
	}
	mainFile := strings.TrimSpace(string(output))
	main, inside := repositoryDir(tree, mainFile)
	if !inside || path.Base(main) != "go.mod" {
		return nil, fmt.Errorf("main module file is not inside the historical worktree: %s", mainFile)
	}
	result := &configurationContext{files: make(map[string]string), mainDir: path.Dir(main)}
	for _, flag := range config.flags {
		name, value, _ := strings.Cut(strings.TrimLeft(flag, "-"), "=")
		if name == "mod" {
			result.vendor = value == "vendor"
		}
	}
	addModule := func(name, reason string) {
		if _, exists := result.files[name]; exists {
			return
		}
		result.files[name] = reason
		result.moduleRoots = append(result.moduleRoots, path.Dir(name))
		if !result.vendor {
			result.files[path.Join(path.Dir(name), "go.sum")] = reason
		}
	}
	addModule(main, "selected main module")
	for _, node := range nodes {
		if dir, inside := repositoryDir(tree, node.Dir); inside {
			result.packageDirs = append(result.packageDirs, dir)
		}
		if name, inside := repositoryDir(tree, node.ModuleGoMod); inside && !result.vendor {
			addModule(name, "module in the selected dependency graph")
		}
	}
	if result.vendor {
		return result, nil // Dependency module files are not read in vendor mode.
	}
	// Include currently-unused local replacements: the child may start using
	// one without changing the main module's go.mod.
	output, err = runGoWithEnv(ctx, cwd, cache, config.toolchain, config.env, "mod", "edit", "-json")
	if err != nil {
		return nil, err
	}
	var module struct {
		Replace []struct {
			New struct{ Path, Version string }
		}
	}
	if err := json.Unmarshal(output, &module); err != nil {
		return nil, err
	}
	for _, replacement := range module.Replace {
		if replacement.New.Version != "" {
			continue
		}
		dir := replacement.New.Path
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(filepath.Dir(mainFile), dir)
		}
		if name, inside := repositoryDir(tree, filepath.Join(dir, "go.mod")); inside {
			addModule(name, "local replacement declared by the selected main module")
		}
	}
	return result, nil
}

func withinDir(name, dir string) bool {
	return dir == "." || name == dir || strings.HasPrefix(name, dir+"/")
}

func (config *configurationContext) relevance(name string) (string, string) {
	base := path.Base(name)
	dir := path.Dir(name)
	if base == "go.work" || base == "go.work.sum" {
		return "unrelated", "workspace discovery is explicitly disabled (GOWORK=off)"
	}
	if base == "modules.txt" {
		if name == path.Join(config.mainDir, "vendor", "modules.txt") && config.vendor {
			return "relevant", "active vendor manifest for the selected main module"
		}
		return "unrelated", "not an active vendor manifest in this configuration"
	}
	if config.vendor && withinDir(name, path.Join(config.mainDir, "vendor")) && (base == "go.mod" || base == "go.sum") {
		return "unrelated", "vendored module go.mod/go.sum files are not consulted by -mod=vendor"
	}
	if base == "go.sum" && config.vendor {
		return "unrelated", "module checksum files are not consulted by -mod=vendor"
	}
	if reason, exists := config.files[name]; exists {
		return "relevant", reason
	}
	if base == "go.sum" {
		return "unrelated", "not a checksum file of an active module or declared local replacement"
	}
	if (base == ".go-version" || base == ".tool-versions") && withinDir(config.mainDir, dir) {
		return "relevant", "toolchain-selection configuration at or above the selected main module"
	}
	roots := config.moduleRoots
	if len(roots) == 0 {
		roots = []string{config.mainDir}
	}
	for _, pkg := range config.packageDirs {
		for _, root := range roots {
			if withinDir(pkg, dir) && withinDir(dir, root) {
				return "relevant", "configuration/module boundary within an active module above a selected dependency package"
			}
		}
	}
	return "unrelated", "nested module/tool configuration outside the selected module and dependency graph"
}

func recordUncertainConfiguration(result *ReplayResult, event Event, revision string) {
	for _, file := range event.Files {
		if configurationFile(file.Path) {
			result.ConfigurationReview = append(result.ConfigurationReview, ConfigurationChange{
				Path: file.Path, Revision: revision, Relevance: "uncertain", Reason: "historical preflight could not establish relevance: " + result.Reason,
			})
		}
	}
}

func excludeConfigurationChange(result *ReplayResult, reviews []ConfigurationChange) bool {
	result.ConfigurationReview = append(result.ConfigurationReview, reviews...)
	for _, review := range reviews {
		if review.Relevance == "unrelated" {
			continue
		}
		result.Status = "excluded"
		result.Stage = "configuration"
		result.Reason = fmt.Sprintf("dependency/toolchain configuration changed: %s (%s: %s)", review.Path, review.Relevance, review.Reason)
		return true
	}
	return false
}
