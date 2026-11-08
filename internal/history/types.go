// Package history combines Git change events with the current target's import
// graph. It deliberately does not claim that reverse dependencies recompile.
package history

import (
	"time"

	"github.com/konradreiche/gorebuild/internal/profile"
)

type Options struct {
	Target            string
	Since             string
	MaxCommits        int
	Package           string
	GoBinary          string
	BuildFlags        []string
	OutputFile        string
	Replay            int
	CompareVCS        bool
	cacheHashPackages []string // selected explain diagnostic only
}

type Metadata struct {
	Target           string            `json:"target"`
	ImportPath       string            `json:"import_path"`
	Repository       string            `json:"repository"`
	Head             string            `json:"head"`
	Dirty            bool              `json:"dirty"`
	Shallow          bool              `json:"shallow"`
	SinceUTC         time.Time         `json:"since_utc"`
	SinceRequested   string            `json:"since_requested"`
	OldestEventUTC   *time.Time        `json:"oldest_event_utc"`
	OldestCommit     string            `json:"oldest_commit"`
	EventsAnalyzed   int               `json:"events_analyzed"`
	CoverageComplete bool              `json:"coverage_complete"`
	CapturedUTC      time.Time         `json:"captured_utc"`
	MaxCommits       int               `json:"max_commits"`
	Truncated        bool              `json:"truncated"`
	GoVersion        string            `json:"go_version"`
	GOOS             string            `json:"goos"`
	GOARCH           string            `json:"goarch"`
	GOFLAGS          string            `json:"goflags"`
	GOWORK           string            `json:"gowork"`
	BuildFlags       []string          `json:"build_flags"`
	BuildEnvironment map[string]string `json:"build_environment"`
	TargetModuleFile string            `json:"target_module_file,omitempty"`
}

// PackageNode is a package in the current build configuration, not a graph
// reconstructed at each historical revision.
type PackageNode struct {
	ImportPath    string   `json:"import_path"`
	Dir           string   `json:"dir"`
	Imports       []string `json:"imports"`
	EmbedFiles    []string `json:"embed_files,omitempty"`
	EmbedPatterns []string `json:"embed_patterns,omitempty"`
	BuildFiles    []string `json:"build_files,omitempty"`
	ModuleGoMod   string   `json:"module_go_mod,omitempty"`
}

type FileChange struct {
	Path                string   `json:"path"`
	PackageDir          string   `json:"package_dir,omitempty"`
	TestOnly            bool     `json:"test_only,omitempty"`
	Classification      string   `json:"classification"`
	Reason              string   `json:"reason"`
	AffectedPackageDirs []string `json:"affected_package_dirs,omitempty"`
}

// Event is one first-parent commit delta, including merge commits. Its union of
// affected packages represents one possible build, never one build per package.
type Event struct {
	Commit                    string            `json:"commit"`
	Parents                   []string          `json:"parents"`
	TimeUTC                   time.Time         `json:"time_utc"`
	Files                     []FileChange      `json:"files"`
	PackageDirs               []string          `json:"package_dirs"`
	BuildPackageDirs          []string          `json:"build_package_dirs"`
	EstimatedAffectedPackages []string          `json:"estimated_affected_packages"`
	UnmodeledFiles            []string          `json:"unmodeled_files"`
	Replay                    *ReplayResult     `json:"replay,omitempty"`
	NoVCSReplay               *ReplayResult     `json:"no_vcs_replay,omitempty"`
	VCSComparison             *ReplayDifference `json:"vcs_comparison,omitempty"`
	ReplayOrder               []string          `json:"replay_order,omitempty"`
}

// These categories apply to non-Go files, not proven cache invalidations.
type ClassificationCounts struct {
	Unlikely       int `json:"unlikely"`
	PotentialBuild int `json:"potential_build"`
	Unknown        int `json:"unknown"`
}

type NonGoSummary struct {
	Files  ClassificationCounts `json:"files"`
	Events ClassificationCounts `json:"events"`
}

type MonthCount struct {
	Month   string `json:"month"`
	Changes int    `json:"changes"`
}

type PackageImpact struct {
	Dir                 string               `json:"dir"`
	ImportPath          string               `json:"import_path,omitempty"`
	InTarget            bool                 `json:"in_target"`
	Changes             int                  `json:"changes"`
	BuildChanges        int                  `json:"build_changes"`
	Commits             []string             `json:"commits"`
	Monthly             []MonthCount         `json:"monthly"`
	ReverseDependencies []string             `json:"reverse_dependencies"`
	PotentialPath       []string             `json:"potential_path"`
	Measured            *PackageMeasurements `json:"measured,omitempty"`
}

// Replay evidence belongs to the event, not to each co-changing package.
// Missing/failed/excluded samples never become zero-duration observations.
type ReplayResult struct {
	Status              string                `json:"status"` // measured, failed, or excluded
	SelectionReasons    []string              `json:"selection_reasons"`
	BuildTarget         string                `json:"build_target,omitempty"`
	WorkingDirectory    string                `json:"working_directory,omitempty"`
	BuildFlags          []string              `json:"build_flags,omitempty"`
	Stage               string                `json:"stage"`
	Reason              string                `json:"reason,omitempty"`
	Baseline            *profile.BuildCapture `json:"baseline,omitempty"`
	Warm                *profile.BuildCapture `json:"warm,omitempty"`
	Rebuild             *profile.BuildCapture `json:"rebuild,omitempty"`
	PackageImports      map[string]string     `json:"package_imports,omitempty"`
	RebuiltPackages     []string              `json:"rebuilt_packages"`
	LinkedPackages      []string              `json:"linked_packages"`
	RecompiledPackages  []string              `json:"recompiled_packages"`
	ConfigurationReview []ConfigurationChange `json:"configuration_review,omitempty"`
}

type ConfigurationChange struct {
	Path      string `json:"path"`
	Revision  string `json:"revision"`
	Relevance string `json:"relevance"` // relevant, unrelated, uncertain
	Reason    string `json:"reason"`
}

// Differences are no-VCS minus normal, for the same event. They are observed
// contrasts between two runs, not causal attribution to stamping.
type ReplayDifference struct {
	WallTimeNS        int64 `json:"wall_time_ns"`
	CompilationToolNS int64 `json:"compilation_tool_ns"`
	LinkToolNS        int64 `json:"link_tool_ns"`
	ExecutedActions   int   `json:"executed_actions"`
	BuildActions      int   `json:"build_actions"`
	LinkActions       int   `json:"link_actions"`
	CriticalPathNS    int64 `json:"critical_path_ns"`
	CgoActions        int   `json:"cgo_actions"`
	AssemblyActions   int   `json:"assembly_actions"`
	CompileCommands   int   `json:"compile_commands"`
}

type RebuiltPackage struct {
	Package               string `json:"package"`
	Replays               int    `json:"replays"`
	RecompiledReplays     int    `json:"recompiled_replays"`
	ExecutedActions       int    `json:"executed_actions"`
	MedianToolTimeNS      *int64 `json:"median_tool_time_ns"`
	MedianActionElapsedNS *int64 `json:"median_action_elapsed_ns"`
}

type ReplayWorkSummary struct {
	Samples                 int              `json:"samples"`
	MedianWallTimeNS        *int64           `json:"median_wall_time_ns"`
	MedianWarmWallTimeNS    *int64           `json:"median_warm_wall_time_ns"`
	MedianCompilationToolNS *int64           `json:"median_compilation_tool_ns"`
	MedianLinkToolNS        *int64           `json:"median_link_tool_ns"`
	MedianCriticalPathNS    *int64           `json:"median_critical_path_ns"`
	TotalExecutedActions    int              `json:"total_executed_actions"`
	TotalBuildActions       int              `json:"total_build_actions"`
	TotalCompileCommands    int              `json:"total_compile_commands"`
	TotalCgoActions         int              `json:"total_cgo_actions"`
	TotalAssemblyActions    int              `json:"total_assembly_actions"`
	TotalLinkActions        int              `json:"total_link_actions"`
	RebuiltPackages         []RebuiltPackage `json:"rebuilt_packages"`
}

type VCSComparisonSummary struct {
	PairedEvents      int               `json:"paired_events"`
	NoVCSMeasured     int               `json:"no_vcs_measured"`
	NoVCSFailed       int               `json:"no_vcs_failed"`
	NoVCSExcluded     int               `json:"no_vcs_excluded"`
	Normal            ReplayWorkSummary `json:"normal_paired"`
	NoVCS             ReplayWorkSummary `json:"no_vcs_paired"`
	MedianWallDeltaNS *int64            `json:"median_wall_delta_ns"`
	MedianLinkDeltaNS *int64            `json:"median_link_delta_ns"`
}

type ReplaySummary struct {
	Requested               int                `json:"requested"`
	Selected                int                `json:"selected"`
	Measured                int                `json:"measured"`
	Failed                  int                `json:"failed"`
	Excluded                int                `json:"excluded"`
	TotalMeasuredWallTimeNS int64              `json:"total_measured_wall_time_ns"`
	Sampling                string             `json:"sampling"`
	Toolchain               string             `json:"toolchain"`
	CachePolicy             string             `json:"cache_policy"`
	Work                    *ReplayWorkSummary `json:"work,omitempty"`
}

// Durations describe potential build-event cohorts (BuildPackageDirs), not
// test-only changes, allocations of cost, or isolated package-edit latency.
// P90 needs ten successful samples; a range needs two.
type PackageMeasurements struct {
	Selected               int    `json:"selected"`
	Samples                int    `json:"samples"`
	Failed                 int    `json:"failed"`
	Excluded               int    `json:"excluded"`
	MedianWallTimeNS       *int64 `json:"median_wall_time_ns"`
	P90WallTimeNS          *int64 `json:"p90_wall_time_ns"`
	MinWallTimeNS          *int64 `json:"min_wall_time_ns"`
	MaxWallTimeNS          *int64 `json:"max_wall_time_ns"`
	MedianOwnBuildToolNS   *int64 `json:"median_own_build_tool_ns"`
	MedianOtherBuildToolNS *int64 `json:"median_other_build_tool_ns"`
	MedianLinkToolNS       *int64 `json:"median_link_tool_ns"`
}

type CoChange struct {
	Packages       [2]string  `json:"package_dirs"`
	Commits        int        `json:"commits"`
	PackageChanges [2]int     `json:"package_changes"`
	Shares         [2]float64 `json:"shares"` // commits / changes for each package; not coupling confidence
}

type Report struct {
	SchemaVersion int                   `json:"schema_version"`
	Analysis      string                `json:"analysis"`
	Metadata      Metadata              `json:"metadata"`
	FocusPackage  string                `json:"focus_package,omitempty"`
	Graph         []PackageNode         `json:"graph"`
	Events        []Event               `json:"events"`
	Packages      []PackageImpact       `json:"packages"`
	CoChanges     []CoChange            `json:"co_changes"`
	NonGoChanges  NonGoSummary          `json:"non_go_changes"`
	Warnings      []string              `json:"warnings"`
	Replay        *ReplaySummary        `json:"replay,omitempty"`
	VCSComparison *VCSComparisonSummary `json:"vcs_comparison,omitempty"`
}
