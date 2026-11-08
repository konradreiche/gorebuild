package explain

import (
	"context"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/konradreiche/gorebuild/internal/history"
	"github.com/konradreiche/gorebuild/internal/profile"
)

type CacheInputChange struct {
	Category string   `json:"category"`
	Block    string   `json:"block"`
	Removed  []string `json:"removed"`
	Added    []string `json:"added"`
}

type CacheDiagnostic struct {
	Commit      string                   `json:"commit"`
	Package     string                   `json:"package"`
	Status      string                   `json:"status"`
	Stage       string                   `json:"stage"`
	Reason      string                   `json:"reason,omitempty"`
	Complete    bool                     `json:"complete"`
	ParentLog   string                   `json:"parent_hash_log"`
	WarmLog     string                   `json:"warm_hash_log"`
	ChildLog    string                   `json:"child_hash_log"`
	Changes     []CacheInputChange       `json:"input_changes"`
	Executed    []profile.ExecutedAction `json:"selected_executed_actions"`
	Toolchain   string                   `json:"toolchain"`
	Flags       []string                 `json:"build_flags"`
	Environment map[string]string        `json:"build_environment"`
	Files       []history.FileChange     `json:"recorded_changed_files"`
	DirectFiles []string                 `json:"recorded_selected_package_changes"`
	Limitations []string                 `json:"limitations"`
}

// ReplayCache is deliberately separate from Describe/InspectRelationship. It
// must only be called after explicit replay permission and a destination for
// the selected diagnostic evidence have been obtained by the command.
func ReplayCache(ctx context.Context, data *Dataset, commit, query, root string) (*CacheDiagnostic, error) {
	pkg, err := data.ResolvePackage(query)
	if err != nil {
		return nil, err
	}
	var selected *Event
	for index := range data.Events {
		if strings.HasPrefix(data.Events[index].Commit, commit) {
			if selected != nil {
				return nil, fmt.Errorf("ambiguous commit prefix")
			}
			selected = &data.Events[index]
		}
	}
	if selected == nil {
		return nil, fmt.Errorf("commit not found")
	}
	if !selected.Replay.Measured() {
		return nil, fmt.Errorf("cache replay requires an existing usable measured event")
	}
	report := &history.Report{Metadata: data.Metadata, Graph: slices.Clone(data.Graph), Events: []history.Event{{Commit: selected.Commit, Parents: selected.Parents, Files: selected.Files, PackageDirs: selected.PackageDirs, BuildPackageDirs: selected.BuildPackageDirs}}}
	if selected.Replay.BuildFlags != nil {
		report.Metadata.BuildFlags = selected.Replay.BuildFlags
	}
	replay, err := history.DiagnoseCache(ctx, report, root, pkg, selected.Replay.WorkingDirectory)
	if err != nil {
		return nil, err
	}
	result := &CacheDiagnostic{Commit: selected.Commit, Package: pkg, Status: replay.Status, Stage: replay.Stage, Reason: replay.Reason, Toolchain: report.Metadata.GoVersion, Flags: report.Metadata.BuildFlags, Environment: report.Metadata.BuildEnvironment, Files: selected.Files, Limitations: []string{
		"Fresh diagnostic replay, not original capture timings. No wall-time savings inference.",
		"Changed hash inputs establish cache-key differences, not source-level declaration causality or the sole cause of a cache miss.",
		"Import content hashes are observed inputs, not evidence that every upstream edit changes exports.",
		"Generated-file provenance and generator execution remain unverified; no generators run.",
		"Only selected package and target main build/link hash blocks are retained. Go diagnostics are an internal format.",
	}}
	for _, file := range selected.Files {
		for _, dir := range append([]string{file.PackageDir}, file.AffectedPackageDirs...) {
			if selected.Replay.PackageImports[dir] == pkg {
				result.DirectFiles = append(result.DirectFiles, file.Path)
				break
			}
		}
	}
	sort.Strings(result.DirectFiles)
	if replay.Baseline != nil {
		result.ParentLog = replay.Baseline.Log
	}
	if replay.Warm != nil {
		result.WarmLog = replay.Warm.Log
	}
	if replay.Rebuild != nil {
		result.ChildLog = replay.Rebuild.Log
		for _, action := range replay.Rebuild.Run.Graph.ExecutedActionDetails {
			if action.Package == pkg || action.Package == data.Metadata.ImportPath {
				result.Executed = append(result.Executed, action)
			}
		}
	}
	result.Changes = compareCacheInputs(result.WarmLog, result.ChildLog)
	result.Complete = replay.Status == "measured" && completeCacheBlock(result.WarmLog, "build "+pkg) && completeCacheBlock(result.ChildLog, "build "+pkg) && !strings.Contains(result.ParentLog+result.WarmLog+result.ChildLog, "evidence incomplete")
	return result, nil
}

type cacheHashBlock struct {
	Inputs    []string
	Complete  bool
	Ambiguous bool
}

// A warm cache hit can hash the same action twice. Compare hash invocations,
// not their concatenation, or every unchanged input looks spuriously removed.
func readCacheBlock(log, block string) cacheHashBlock {
	var result cacheHashBlock
	var previousSum string
	header := "HASH[" + block + "]"
	prefix := header + ": "
	for _, line := range strings.Split(log, "\n") {
		if line == header {
			result.Inputs = nil
			result.Complete = false
			continue
		}
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		value := strings.TrimPrefix(line, prefix)
		input, err := strconv.Unquote(value)
		if err != nil {
			sum, err := hex.DecodeString(value)
			if err != nil {
				continue
			}
			if len(sum) != 32 {
				continue
			}
			if previousSum != "" && previousSum != value {
				result.Ambiguous = true
			}
			previousSum = value
			result.Complete = true
			continue
		}
		result.Inputs = append(result.Inputs, strings.TrimSuffix(input, "\n"))
	}
	return result
}

func completeCacheBlock(log, block string) bool {
	result := readCacheBlock(log, block)
	return result.Complete && !result.Ambiguous && len(result.Inputs) > 0
}

func cacheInputs(log, block string) []string { return readCacheBlock(log, block).Inputs }

func inputCategory(input string) string {
	if strings.HasPrefix(input, "file ") {
		name, _, _ := strings.Cut(strings.TrimPrefix(input, "file "), " ")
		if filepath.Ext(name) == ".go" {
			return "Go source inputs (generated provenance unverified)"
		}
		return "non-Go/embed/native file inputs"
	}
	if strings.HasPrefix(input, "import ") {
		return "imported package content inputs"
	}
	if strings.HasPrefix(input, "modinfo ") {
		return "module/build metadata including VCS stamping"
	}
	if strings.HasPrefix(input, "link ") || strings.HasPrefix(input, "packagefile ") || strings.HasPrefix(input, "packagemain ") || strings.HasPrefix(input, "packageshlib ") {
		return "link inputs"
	}
	return "toolchain/build flags/environment or other Go hash inputs"
}

func compareCacheInputs(parent, child string) []CacheInputChange {
	blocks := make(map[string]bool)
	for _, log := range []string{parent, child} {
		for _, line := range strings.Split(log, "\n") {
			if !strings.HasPrefix(line, "HASH[") {
				continue
			}
			block, _, ok := strings.Cut(strings.TrimPrefix(line, "HASH["), "]")
			if ok && (strings.HasPrefix(block, "build ") || strings.HasPrefix(block, "link ")) {
				blocks[block] = true
			}
		}
	}
	var changes []CacheInputChange
	for block := range blocks {
		before := cacheInputs(parent, block)
		after := cacheInputs(child, block)
		categories := make(map[string]*CacheInputChange)
		add := func(input string, removed bool) {
			category := inputCategory(input)
			change := categories[category]
			if change == nil {
				change = &CacheInputChange{Category: category, Block: block}
				categories[category] = change
			}
			if removed {
				change.Removed = append(change.Removed, input)
			} else {
				change.Added = append(change.Added, input)
			}
		}
		// Multiset comparison preserves duplicate hash writes.
		remaining := make(map[string]int)
		for _, input := range after {
			remaining[input]++
		}
		for _, input := range before {
			if remaining[input] > 0 {
				remaining[input]--
				continue
			}
			add(input, true)
		}
		for input, count := range remaining {
			for range count {
				add(input, false)
			}
		}
		for _, change := range categories {
			sort.Strings(change.Removed)
			sort.Strings(change.Added)
			changes = append(changes, *change)
		}
	}
	slices.SortFunc(changes, func(left, right CacheInputChange) int {
		if order := strings.Compare(left.Block, right.Block); order != 0 {
			return order
		}
		return strings.Compare(left.Category, right.Category)
	})
	return changes
}

func (diagnostic *CacheDiagnostic) Describe(top int) string {
	var out strings.Builder
	fmt.Fprintf(&out, "\nOpt-in cache diagnostic replay: %s; %s at %s; complete selected hash evidence: %t\n", diagnostic.Package, diagnostic.Status, diagnostic.Stage, diagnostic.Complete)
	if diagnostic.Reason != "" {
		fmt.Fprintf(&out, "  %s\n", shortDiagnostic(diagnostic.Reason))
	}
	fmt.Fprintf(&out, "Measured: %d selected/main executed actions in this new replay.\n", len(diagnostic.Executed))
	fmt.Fprintf(&out, "Recorded direct package-scope changes: %d (not all are build inputs).\n", len(diagnostic.DirectFiles))
	for _, change := range diagnostic.Changes[:min(top, len(diagnostic.Changes))] {
		fmt.Fprintf(&out, "  Observed hash change: %s; %s (%d removed / %d added inputs)\n", change.Block, change.Category, len(change.Removed), len(change.Added))
	}
	if !diagnostic.Complete {
		fmt.Fprintln(&out, "Incomplete: missing/changed-format hash evidence or replay failure; no invalidation conclusion.")
	}
	fmt.Fprintln(&out, "Inferred: changed imported content may explain recompilation; declaration references alone do not prove this. Full selected hash inputs are in --out.")
	fmt.Fprintln(&out, "VCS/build metadata and linking are separate inputs, not provider compilation time. No refactoring or savings claim.")
	return out.String()
}
