package history

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os/exec"
	"path/filepath"
	"strings"
)

// DiagnoseCache reuses the normal isolated replay, exclusions, warm verification
// and cleanup. The caller must obtain explicit permission: this runs a cold
// parent build, a warm verification build, and a child build, not an offline read.
// Only selected package/main cache-hash blocks are retained in capture logs.
func DiagnoseCache(ctx context.Context, report *Report, sourceRoot, selectedPackage, workingDirectory string) (*ReplayResult, error) {
	if len(report.Events) != 1 {
		return nil, fmt.Errorf("cache diagnostics require exactly one selected event")
	}
	root, err := filepath.Abs(sourceRoot)
	if err != nil {
		return nil, err
	}
	command := exec.CommandContext(ctx, "go", "env", "-json", "GOROOT", "GOVERSION")
	command.Dir = root
	output, err := command.Output()
	if err != nil {
		return nil, err
	}
	var tool struct{ GOROOT, GOVERSION string }
	if err := json.Unmarshal(output, &tool); err != nil {
		return nil, err
	}
	if report.Metadata.GoVersion != "" && tool.GOVERSION != report.Metadata.GoVersion {
		return nil, fmt.Errorf("cache replay toolchain %s differs from captured %s; select the captured Go toolchain on PATH", tool.GOVERSION, report.Metadata.GoVersion)
	}
	relative := filepath.Clean(filepath.FromSlash(workingDirectory))
	if filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("invalid captured working directory")
	}
	// Rebase captured source directories; never inspect/build the developer tree.
	oldRoot := report.Metadata.Repository
	for index := range report.Graph {
		dir, err := filepath.Rel(oldRoot, report.Graph[index].Dir)
		if err != nil {
			return nil, err
		}
		if dir != ".." && !strings.HasPrefix(dir, ".."+string(filepath.Separator)) {
			report.Graph[index].Dir = filepath.Join(root, dir)
		}
	}
	report.Metadata.Repository = root
	report.Metadata.BuildEnvironment = maps.Clone(report.Metadata.BuildEnvironment)
	if report.Metadata.BuildEnvironment == nil {
		report.Metadata.BuildEnvironment = make(map[string]string)
	}
	debug := report.Metadata.BuildEnvironment["GODEBUG"]
	report.Metadata.BuildEnvironment["GODEBUG"] = debug + ",gocachehash=1"
	options := Options{Replay: 1, BuildFlags: report.Metadata.BuildFlags, cacheHashPackages: []string{selectedPackage, report.Metadata.ImportPath}}
	replayHistory(ctx, report, options, filepath.Join(root, relative), tool.GOROOT)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return report.Events[0].Replay, nil
}
