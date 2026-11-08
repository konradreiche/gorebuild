package verify

// Scenario inputs are frozen before timing and retained verbatim. Historical
// inverse patches are explicitly distinguished from replaying an original edit.
type Scenario struct {
	Name                    string   `json:"name"`
	Kind                    string   `json:"kind"`
	File                    string   `json:"file,omitempty"`
	OriginalSHA256          string   `json:"original_sha256,omitempty"`
	Content                 string   `json:"content,omitempty"`
	Patch                   string   `json:"patch,omitempty"`
	SHA256                  string   `json:"input_sha256"`
	Commit                  string   `json:"historical_commit,omitempty"`
	Parent                  string   `json:"historical_parent,omitempty"`
	Direction               string   `json:"historical_direction,omitempty"`
	EventFiles              []string `json:"all_historical_event_files,omitempty"`
	SelectedFiles           []string `json:"selected_files"`
	IsolatedHistoricalEvent bool     `json:"isolated_historical_event"`
	Behavior                string   `json:"behavior_assumption"`
}

type ScenarioResult struct {
	Scenario       Scenario          `json:"scenario"`
	Status         string            `json:"status"`
	Diagnostics    []string          `json:"diagnostics"`
	Correctness    []ScenarioCheck   `json:"edited_correctness_checks"`
	Pairs          []Pair            `json:"pairs"`
	Controls       []Pair            `json:"same_revision_controls"`
	Schedule       []ExecutionStep   `json:"incremental_execution_schedule"`
	Summary        Summary           `json:"summary"`
	ControlSummary Summary           `json:"same_revision_summary"`
	Noise          NoiseConclusion   `json:"timing_conclusion"`
	Structure      []StructuralCount `json:"structural_observations"`
}
type ScenarioCheck struct {
	Revision string        `json:"revision"`
	Commit   string        `json:"commit"`
	Tests    *CommandCheck `json:"tests,omitempty"`
	Failure  string        `json:"failure,omitempty"`
}
type ExecutionStep struct {
	Run    int    `json:"run"`
	Label  string `json:"label"`
	Commit string `json:"commit"`
}
type StructuralCount struct {
	Package        string `json:"package"`
	BeforeCompiled int    `json:"before_compiled"`
	AfterCompiled  int    `json:"after_compiled"`
	Pairs          int    `json:"paired_observations"`
	Conclusion     string `json:"conclusion"`
}
type NoiseConclusion struct {
	Confidence              string `json:"confidence"`
	Direction               string `json:"direction"`
	ControlAbsoluteMedianNS int64  `json:"control_absolute_delta_median_ns,omitempty"`
	ControlMaxAbsoluteNS    int64  `json:"control_max_absolute_delta_ns,omitempty"`
	Reason                  string `json:"reason"`
}
type CallerSite struct {
	File            string   `json:"file"`
	ImportedPackage string   `json:"imported_package"`
	Symbols         []string `json:"lexical_symbols"`
	InTarget        bool     `json:"in_target_closure"`
	TestFile        bool     `json:"test_file"`
	BuildConstraint bool     `json:"has_build_constraint"`
	Status          string   `json:"status"`
}
