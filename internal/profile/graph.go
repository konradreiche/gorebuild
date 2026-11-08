package profile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// This is an adapter for the undocumented -debug-actiongraph JSON format.
// Durations in CmdReal/CmdUser/CmdSys are encoded as nanoseconds by Go.
// Ignore unrelated fields so future Go releases can add new metadata.
type Action struct {
	ID        int       `json:"ID"`
	Mode      string    `json:"Mode"`
	Package   string    `json:"Package"`
	Deps      []int     `json:"Deps"`
	NeedBuild bool      `json:"NeedBuild"` // never used as evidence a compiler ran
	TimeStart time.Time `json:"TimeStart"`
	TimeDone  time.Time `json:"TimeDone"`
	Cmd       []string  `json:"Cmd"`
	CmdReal   int64     `json:"CmdReal"`
	CmdUser   int64     `json:"CmdUser"`
	CmdSys    int64     `json:"CmdSys"`
}

func (a Action) Executed() bool { return len(a.Cmd) > 0 }

func (a Action) Elapsed() time.Duration {
	if a.TimeStart.IsZero() || a.TimeDone.IsZero() || a.TimeDone.Before(a.TimeStart) {
		return 0
	}
	return a.TimeDone.Sub(a.TimeStart)
}

// ToolDuration prefers CmdReal (recorded by Go), with an elapsed fallback for
// Go versions that emit commands but not the duration field. Never count an
// action without a Cmd, even if its NeedBuild flag is set.
func (a Action) ToolDuration() time.Duration {
	if !a.Executed() {
		return 0
	}
	if a.CmdReal > 0 {
		return time.Duration(a.CmdReal)
	}
	return a.Elapsed()
}

// PackageCost is the total recorded tool time for executed package build
// actions, not necessarily pure Go compile time (cgo and assembly may appear).
type PackageCost struct {
	Package         string `json:"package"`
	ToolTimeNS      int64  `json:"tool_time_ns"`
	ActionElapsedNS int64  `json:"action_elapsed_ns"`
	Actions         int    `json:"actions"`
}

type ChainStep struct {
	ID        int    `json:"id"`
	Mode      string `json:"mode"`
	Package   string `json:"package,omitempty"`
	ElapsedNS int64  `json:"elapsed_ns"`
}

// ExecutedAction records aggregate command work for one Go DAG node. CmdReal
// does not contain per-command timings when a node runs several tools.
type ExecutedAction struct {
	ID               int    `json:"id"`
	Mode             string `json:"mode"`
	Package          string `json:"package,omitempty"`
	ElapsedNS        int64  `json:"elapsed_ns"`
	ToolTimeNS       int64  `json:"tool_time_ns"`
	CompileCommands  int    `json:"compile_commands"`
	CgoCommands      int    `json:"cgo_commands"`
	AssemblyCommands int    `json:"assembly_commands"`
	LinkCommands     int    `json:"link_commands"`
}

type GraphSummary struct {
	ExecutedActionDetails   []ExecutedAction `json:"executed_action_details"`
	ExecutedCgoActions      int              `json:"executed_cgo_actions"`
	ExecutedAssemblyActions int              `json:"executed_assembly_actions"`
	CompileCommands         int              `json:"compile_commands"`
	CgoCommands             int              `json:"cgo_commands"`
	AssemblyCommands        int              `json:"assembly_commands"`
	LinkCommands            int              `json:"link_commands"`
	CompilationToolTimeNS   int64            `json:"compilation_tool_time_ns"`
	ExecutedActionChainNS   int64            `json:"executed_action_chain_ns"`
	ExecutedActionChain     []ChainStep      `json:"executed_action_chain"`

	ActionGraph           []Action      `json:"action_graph"`
	Actions               int           `json:"actions"`
	ExecutedActions       int           `json:"executed_actions"`
	ExecutedBuildActions  int           `json:"executed_build_actions"`
	ExecutedLinkActions   int           `json:"executed_link_actions"`
	ToolTimeNS            int64         `json:"tool_time_ns"`
	BuildToolTimeNS       int64         `json:"build_tool_time_ns"`
	LinkToolTimeNS        int64         `json:"link_tool_time_ns"`
	LongestActionChainNS  int64         `json:"longest_action_chain_ns"`
	LongestActionChain    []ChainStep   `json:"longest_action_chain"`
	PackageBuildToolTimes []PackageCost `json:"package_build_tool_times"`
}

func ReadGraph(path string) ([]Action, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var actions []Action
	if err := json.Unmarshal(data, &actions); err != nil {
		return nil, fmt.Errorf("invalid action graph: %w", err)
	}
	if len(actions) == 0 {
		return nil, fmt.Errorf("empty action graph at %s", path)
	}
	return actions, nil
}

func Analyze(actions []Action) (GraphSummary, error) {
	var result GraphSummary
	result.Actions = len(actions)
	result.ActionGraph = actions
	byID := make(map[int]Action, len(actions))
	byPackage := make(map[string]*PackageCost)
	for _, action := range actions {
		if _, exists := byID[action.ID]; exists {
			return GraphSummary{}, fmt.Errorf("duplicate graph action ID %d", action.ID)
		}
		byID[action.ID] = action
		if !action.Executed() {
			continue
		}
		result.ExecutedActions++
		detail := executedAction(action)
		result.ExecutedActionDetails = append(result.ExecutedActionDetails, detail)
		result.CompileCommands += detail.CompileCommands
		result.CgoCommands += detail.CgoCommands
		result.AssemblyCommands += detail.AssemblyCommands
		result.LinkCommands += detail.LinkCommands
		if detail.CgoCommands > 0 {
			result.ExecutedCgoActions++
		}
		if detail.AssemblyCommands > 0 {
			result.ExecutedAssemblyActions++
		}
		toolNS := int64(action.ToolDuration())
		if action.Mode != "link" && (action.Mode == "build" || detail.CompileCommands+detail.CgoCommands+detail.AssemblyCommands > 0) {
			result.CompilationToolTimeNS += toolNS
		}
		result.ToolTimeNS += toolNS
		switch action.Mode {
		case "build":
			result.ExecutedBuildActions++
			result.BuildToolTimeNS += toolNS
			if action.Package != "" {
				cost := byPackage[action.Package]
				if cost == nil {
					cost = &PackageCost{Package: action.Package}
					byPackage[action.Package] = cost
				}
				cost.ToolTimeNS += toolNS
				cost.ActionElapsedNS += int64(action.Elapsed())
				cost.Actions++
			}
		case "link":
			result.ExecutedLinkActions++
			result.LinkToolTimeNS += toolNS
		}
	}
	for _, p := range byPackage {
		result.PackageBuildToolTimes = append(result.PackageBuildToolTimes, *p)
	}
	sort.Slice(result.PackageBuildToolTimes, func(i, j int) bool {
		a := result.PackageBuildToolTimes[i]
		b := result.PackageBuildToolTimes[j]
		if a.ToolTimeNS != b.ToolTimeNS {
			return a.ToolTimeNS > b.ToolTimeNS
		}
		return a.Package < b.Package
	})

	var err error
	result.LongestActionChainNS, result.LongestActionChain, err = longestChain(actions, byID, false, nil)
	if err != nil {
		return GraphSummary{}, err
	}
	result.ExecutedActionChainNS, result.ExecutedActionChain, err = longestChain(actions, byID, true, nil)
	if err != nil {
		return GraphSummary{}, err
	}
	return result, nil
}

// Both profile and history use the same DAG traversal. In executed-only mode,
// cache/synthetic nodes still connect dependencies but have zero weight.
func longestChain(actions []Action, byID map[int]Action, executedOnly bool, zeroID *int) (int64, []ChainStep, error) {
	weight := func(action Action) time.Duration {
		if (executedOnly && !action.Executed()) || (zeroID != nil && action.ID == *zeroID) {
			return 0
		}
		return action.Elapsed()
	}
	memo := make(map[int]time.Duration, len(actions))
	state := make(map[int]uint8, len(actions))
	bestDep := make(map[int]int, len(actions))
	var visit func(int) (time.Duration, error)
	visit = func(id int) (time.Duration, error) {
		if state[id] == 2 {
			return memo[id], nil
		}
		if state[id] == 1 {
			return 0, fmt.Errorf("cycle in action graph at ID %d", id)
		}
		action, ok := byID[id]
		if !ok {
			return 0, fmt.Errorf("unknown action ID %d", id)
		}
		state[id] = 1
		var longest time.Duration
		best := -1
		for _, dep := range action.Deps {
			duration, err := visit(dep)
			if err != nil {
				return 0, err
			}
			if best == -1 || duration > longest {
				longest = duration
				best = dep
			}
		}
		bestDep[id] = best
		memo[id] = longest + weight(action)
		state[id] = 2
		return memo[id], nil
	}
	bestID := -1
	var longest time.Duration
	for _, action := range actions {
		duration, err := visit(action.ID)
		if err != nil {
			return 0, nil, err
		}
		if bestID == -1 || duration > longest {
			longest = duration
			bestID = action.ID
		}
	}
	// Retain the entire chain in leaf-to-root dependency order, including
	// instantaneous synthetic actions. Terminal output can filter and truncate it.
	var backwards []ChainStep
	id := bestID
	for id != -1 {
		action := byID[id]
		backwards = append(backwards, ChainStep{ID: action.ID, Mode: action.Mode, Package: action.Package, ElapsedNS: int64(weight(action))})
		id = bestDep[id]
	}
	var chain []ChainStep
	for _, backward := range slices.Backward(backwards) {
		chain = append(chain, backward)
	}
	return int64(longest), chain, nil
}

// CriticalPathSaving is a zero-duration intervention on one executed DAG node.
// Dependencies, all other weights and cache behavior are held fixed. It is an
// optimistic model bound, not a prediction of measured wall-clock savings.
type CriticalPathSaving struct {
	ActionID        int
	ObservedNS      int64
	CurrentNS       int64
	WithoutNS       int64
	MaximumSavingNS int64
	RemainingChain  []ChainStep
}

func EstimateCriticalPathSaving(actions []Action, id int) (CriticalPathSaving, error) {
	graph, err := Analyze(actions)
	if err != nil {
		return CriticalPathSaving{}, err
	}
	byID := make(map[int]Action, len(actions))
	for _, action := range actions {
		byID[action.ID] = action
	}
	action, exists := byID[id]
	if !exists || !action.Executed() {
		return CriticalPathSaving{}, fmt.Errorf("action %d is not an executed action", id)
	}
	without, chain, err := longestChain(actions, byID, true, &id)
	if err != nil {
		return CriticalPathSaving{}, err
	}
	return CriticalPathSaving{ActionID: id, ObservedNS: int64(action.Elapsed()), CurrentNS: graph.ExecutedActionChainNS, WithoutNS: without, MaximumSavingNS: graph.ExecutedActionChainNS - without, RemainingChain: chain}, nil
}

func executedAction(action Action) ExecutedAction {
	result := ExecutedAction{ID: action.ID, Mode: action.Mode, Package: action.Package, ElapsedNS: int64(action.Elapsed()), ToolTimeNS: int64(action.ToolDuration())}
	for _, command := range action.Cmd {
		// Go records each invoked command as one joined string, not argv.
		// Recognize only an unambiguous executable; wrappers are not guessed.
		executable, _, _ := strings.Cut(command, " ")
		if strings.HasPrefix(command, "\"") {
			for end := range len(command) {
				if end == 0 || command[end] != '"' {
					continue
				}
				decoded, err := strconv.Unquote(command[:end+1])
				if err != nil {
					continue
				}
				executable = decoded
				break
			}
		}
		switch strings.TrimSuffix(filepath.Base(executable), ".exe") {
		case "compile":
			result.CompileCommands++
		case "cgo":
			result.CgoCommands++
		case "asm":
			result.AssemblyCommands++
		case "link":
			result.LinkCommands++
		}
	}
	return result
}
