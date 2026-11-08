// Package verify measures explicit existing revisions; it never refactors code.
package verify

import (
	"github.com/konradreiche/gorebuild/internal/history"
	"github.com/konradreiche/gorebuild/internal/profile"
	"io"
)

type Options struct {
	Target              string
	Before              string
	After               string
	Package             string
	Runs                int
	GoBinary            string
	BuildFlags          []string
	TestPackages        []string
	OutputFile          string
	Scenarios           []string
	ColdRuns            int
	Controls            bool
	HistoryFile         string
	HistoricalCommits   []string
	HistoricalDirection string
	HistoricalSubset    bool
	RaceTests           bool
	RuntimeArgs         []string
	AfterTestOverlays   []string
	Progress            io.Writer
	overlays            []TestOverlay
}

type CommandCheck struct {
	Arguments   []string          `json:"arguments"`
	WallTimeNS  int64             `json:"wall_time_ns"`
	Environment map[string]string `json:"environment,omitempty"`
	Log         string            `json:"log"`
	Failure     string            `json:"failure,omitempty"`
}

type Revision struct {
	Ref              string                `json:"requested_ref"`
	Commit           string                `json:"commit"`
	Graph            []history.PackageNode `json:"dependency_graph"`
	ImportPath       string                `json:"target_import_path"`
	ChangedPackage   string                `json:"controlled_package"`
	PackageDirectory string                `json:"controlled_package_directory"`
	PackageName      string                `json:"controlled_package_name"`
	TestPackages     []string              `json:"test_packages"`
	Build            *profile.BuildCapture `json:"correctness_build,omitempty"`
	Tests            *CommandCheck         `json:"correctness_tests,omitempty"`
	Correct          bool                  `json:"correctness_checks_passed"`
	Runtime          *CommandCheck         `json:"runtime_check,omitempty"`
	SourceFiles      []string              `json:"controlled_source_files"`
	CallerAudit      []CallerSite          `json:"repository_caller_audit"`
	ExtraCallerTests []CommandCheck        `json:"out_of_target_caller_tests,omitempty"`
	correctnessCache string
}

type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
}
type Reach struct {
	Package string `json:"package"`
	Before  bool   `json:"before_reaches_controlled_package"`
	After   bool   `json:"after_reaches_controlled_package"`
}
type GraphComparison struct {
	Removed      []Edge  `json:"removed_import_edges"`
	Added        []Edge  `json:"added_import_edges"`
	Reachability []Reach `json:"repository_package_reachability"`
}

type Probe struct {
	File    string `json:"file"`
	Content string `json:"content"`
	SHA256  string `json:"sha256"`
	Purpose string `json:"purpose"`
}
type Measurement struct {
	Revision    string                `json:"revision"`
	Commit      string                `json:"commit"`
	Status      string                `json:"status"`
	Reason      string                `json:"reason,omitempty"`
	CachePolicy string                `json:"cache_policy"`
	Cold        *profile.BuildCapture `json:"cold,omitempty"`
	Warm        *profile.BuildCapture `json:"warm,omitempty"`
	Incremental *profile.BuildCapture `json:"incremental,omitempty"`
	ProbeSHA256 string                `json:"applied_probe_sha256,omitempty"`
	Recompiled  []string              `json:"recompiled_packages"`
	Linked      []string              `json:"linked_packages"`
}
type Pair struct {
	Index  int          `json:"index"`
	Order  []string     `json:"execution_order"`
	Before *Measurement `json:"before,omitempty"`
	After  *Measurement `json:"after,omitempty"`
}

type Distribution struct {
	Count           int   `json:"count"`
	MedianNS        int64 `json:"median_ns,omitempty"`
	MedianAvailable bool  `json:"median_available"`
	MinNS           int64 `json:"min_ns"`
	MaxNS           int64 `json:"max_ns"`
}
type MetricComparison struct {
	Before      Distribution `json:"before"`
	After       Distribution `json:"after"`
	PairedDelta Distribution `json:"paired_after_minus_before"`
}
type Summary struct {
	ValidPairs          int              `json:"valid_pairs"`
	ColdWall            MetricComparison `json:"cold_wall"`
	IncrementalWall     MetricComparison `json:"incremental_wall"`
	CompilationWork     MetricComparison `json:"incremental_compilation_tool_work"`
	LinkWork            MetricComparison `json:"incremental_link_tool_work"`
	CriticalPath        MetricComparison `json:"incremental_executed_critical_path"`
	ColdCompilationWork MetricComparison `json:"cold_compilation_tool_work"`
	ColdLinkWork        MetricComparison `json:"cold_link_tool_work"`
	ColdCriticalPath    MetricComparison `json:"cold_executed_critical_path"`
	Interpretation      string           `json:"interpretation"`
}

type Report struct {
	SchemaVersion     int               `json:"schema_version"`
	Repository        string            `json:"repository"`
	Target            string            `json:"target"`
	Configuration     map[string]string `json:"go_configuration"`
	BuildFlags        []string          `json:"build_flags"`
	CachePolicy       string            `json:"cache_policy"`
	Before            Revision          `json:"before"`
	After             Revision          `json:"after"`
	DependencyChanges GraphComparison   `json:"dependency_changes"`
	Probe             Probe             `json:"controlled_change"`
	TestOverlays      []TestOverlay     `json:"correctness_only_test_overlays,omitempty"`
	Scenarios         []ScenarioResult  `json:"scenarios,omitempty"`
	ColdPairs         []Pair            `json:"cold_pairs,omitempty"`
	ColdSummary       Summary           `json:"cold_summary"`
	ScenarioSelection []string          `json:"scenario_selection_diagnostics,omitempty"`
	Untested          []string          `json:"untested_configurations"`
	Pairs             []Pair            `json:"pairs,omitempty"`
	Summary           Summary           `json:"summary"`
	PerformanceValid  bool              `json:"performance_comparison_valid"`
	Errors            []string          `json:"errors"`
	Limitations       []string          `json:"limitations"`
}
