package explain

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
)

// CacheEvidence is a compact, revalidated projection of a separate diagnostic
// replay, not evidence that the original timing capture had identical inputs.
type CacheEvidence struct {
	Path     string             `json:"path"`
	Commit   string             `json:"commit"`
	Package  string             `json:"package"`
	Complete bool               `json:"complete"`
	Warnings []string           `json:"warnings"`
	Changes  []CacheInputChange `json:"observed_input_changes"`
}

func readCacheEvidence(file string, data *Dataset, target string) (*CacheEvidence, error) {
	content, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var saved struct {
		SchemaVersion int              `json:"schema_version"`
		Cache         *CacheDiagnostic `json:"cache_diagnostic"`
	}
	if err := json.Unmarshal(content, &saved); err != nil {
		return nil, fmt.Errorf("cache evidence: %w", err)
	}
	if saved.SchemaVersion != 1 || saved.Cache == nil {
		return nil, fmt.Errorf("unsupported cache diagnostic artifact")
	}
	cache := saved.Cache
	if cache.Package != target {
		return nil, fmt.Errorf("cache evidence package %s differs from selected %s", cache.Package, target)
	}
	var event *Event
	for index := range data.Events {
		if data.Events[index].Commit == cache.Commit {
			event = &data.Events[index]
			break
		}
	}
	if event == nil {
		return nil, fmt.Errorf("cache evidence commit is not in selected history")
	}
	result := &CacheEvidence{Path: file, Commit: cache.Commit, Package: target}
	block := "build " + target
	result.Changes = compareCacheInputs(cache.WarmLog, cache.ChildLog)
	// Only target compile inputs may support target invalidation reasoning. Main
	// stamping/link hashes must never be misattributed to a library compilation.
	result.Changes = slices.DeleteFunc(result.Changes, func(change CacheInputChange) bool { return change.Block != block })
	if !cache.Complete || cache.Status != "measured" || !completeCacheBlock(cache.WarmLog, block) || !completeCacheBlock(cache.ChildLog, block) {
		result.Warnings = append(result.Warnings, "incomplete or ambiguous selected cache-hash evidence")
	}
	if !event.Replay.Measured() {
		result.Warnings = append(result.Warnings, "original history event is not a usable successful replay")
	}
	if cache.Toolchain != data.Metadata.GoVersion {
		result.Warnings = append(result.Warnings, "diagnostic and historical toolchains differ")
	}
	flags := data.Metadata.BuildFlags
	if event.Replay != nil && event.Replay.BuildFlags != nil {
		flags = event.Replay.BuildFlags
	}
	if !slices.Equal(flags, cache.Flags) {
		result.Warnings = append(result.Warnings, "diagnostic and historical build flags differ")
	}
	if normalizedCacheDebug(cache.Environment["GODEBUG"]) != normalizedCacheDebug(data.Metadata.BuildEnvironment["GODEBUG"]) {
		result.Warnings = append(result.Warnings, "diagnostic environment differs: GODEBUG beyond cache hashing")
	}
	for key, value := range data.Metadata.BuildEnvironment {
		if key == "GODEBUG" {
			continue
		} // Only gocachehash is deliberately different in diagnostics.
		if cache.Environment[key] != value {
			result.Warnings = append(result.Warnings, "diagnostic environment differs: "+key)
		}
	}
	if strings.Contains(cache.WarmLog+cache.ChildLog, "evidence incomplete") {
		result.Warnings = append(result.Warnings, "truncated cache-hash diagnostics")
	}
	result.Complete = len(result.Warnings) == 0
	return result, nil
}

func normalizedCacheDebug(value string) string {
	var settings []string
	for _, setting := range strings.Split(value, ",") {
		if setting != "" && !strings.HasPrefix(setting, "gocachehash=") {
			settings = append(settings, setting)
		}
	}
	return strings.Join(settings, ",")
}

func (evidence *CacheEvidence) inputChanges(commit, dependency string) (bool, []string, []string) {
	if evidence == nil || !evidence.Complete || evidence.Commit != commit {
		return false, nil, nil
	}
	var imports, files []string
	for _, change := range evidence.Changes {
		for _, input := range append(append([]string(nil), change.Removed...), change.Added...) {
			fields := strings.Fields(input)
			if len(fields) >= 3 && fields[0] == "import" {
				if !slices.Contains(imports, fields[1]) {
					imports = append(imports, fields[1])
				}
			}
			if len(fields) >= 3 && fields[0] == "file" {
				if !slices.Contains(files, fields[1]) {
					files = append(files, fields[1])
				}
			}
		}
	}
	return slices.Contains(imports, dependency), imports, files
}
