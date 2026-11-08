package profile

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// BuildOptions describes one build. The caller owns the cache and output paths
// so multiple captures can share a controlled cache and the same output binary.
type BuildOptions struct {
	Directory  string
	GoBinary   string
	Target     string
	BuildFlags []string
	CacheDir   string
	OutputPath string
	// Env, when non-nil, is the complete base environment. GOCACHE is replaced.
	Env []string
	// HashPackages filters gocachehash diagnostics during capture when nonempty.
	HashPackages []string
}

// BuildCapture retains evidence even when a command or graph analysis fails.
type BuildCapture struct {
	Run                  BuildRun        `json:"run"`
	ActionGraph          json.RawMessage `json:"action_graph,omitempty"`
	Log                  string          `json:"log"`
	Failure              string          `json:"failure,omitempty"`
	MalformedActionGraph string          `json:"malformed_action_graph,omitempty"`
}

// CaptureBuild always returns the evidence record. Failure distinguishes an
// unsuccessful capture from a valid observation, without discarding partial logs.
func CaptureBuild(ctx context.Context, options BuildOptions) *BuildCapture {
	capture := &BuildCapture{}
	if err := captureBuild(ctx, options, capture); err != nil {
		capture.Failure = err.Error()
	}
	return capture
}

func captureBuild(ctx context.Context, options BuildOptions, capture *BuildCapture) error {
	artifacts, err := os.MkdirTemp("", "gorebuild-action-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(artifacts)
	graphPath := filepath.Join(artifacts, "actions.json")
	logPath := filepath.Join(artifacts, "build.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		return err
	}
	args := append([]string{"build", "-debug-actiongraph=" + graphPath, "-o", options.OutputPath}, options.BuildFlags...)
	args = append(args, options.Target)
	cmd := exec.CommandContext(ctx, options.GoBinary, args...)
	cmd.Dir = options.Directory
	baseEnv := options.Env
	if baseEnv == nil {
		baseEnv = os.Environ()
	}
	cmd.Env = replaceEnv(baseEnv, "GOCACHE", options.CacheDir)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	var hashOutput *hashLog
	if len(options.HashPackages) > 0 {
		hashOutput = &hashLog{output: logFile, packages: options.HashPackages}
		cmd.Stderr = hashOutput
	}
	start := time.Now()
	// Retain errors in phase order while still collecting evidence from failed
	// commands. Cancellation and command failure take precedence below.
	var failures []error
	err = cmd.Run()
	capture.Run.WallTimeNS = int64(time.Since(start))
	failures = append(failures, err)
	if hashOutput != nil {
		failures = append(failures, hashOutput.flush())
	}
	err = logFile.Close()
	failures = append(failures, err)
	logBytes, err := os.ReadFile(logPath)
	failures = append(failures, err)
	capture.Log = string(logBytes)
	// Only valid JSON belongs in a RawMessage; retain a malformed graph as
	// text instead of making the whole artifact unencodable.
	graphBytes, err := os.ReadFile(graphPath)
	if err != nil {
		failures = append(failures, fmt.Errorf("read action graph: %w", err))
	} else {
		if json.Valid(graphBytes) {
			capture.ActionGraph = json.RawMessage(graphBytes)
		} else {
			capture.MalformedActionGraph = string(graphBytes)
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err := failures[0]; err != nil {
		return fmt.Errorf("build failed: %w\n%s", err, tail(capture.Log, 4096))
	}
	for _, err := range failures[1:] {
		if err != nil {
			return err
		}
	}
	actions, err := ReadGraph(graphPath)
	if err != nil {
		return fmt.Errorf("build action graph: %w; Go may have changed -debug-actiongraph", err)
	}
	capture.Run.Graph, err = Analyze(actions)
	return err
}

func replaceEnv(env []string, key, value string) []string {
	prefix := key + "="
	result := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if len(entry) < len(prefix) || entry[:len(prefix)] != prefix {
			result = append(result, entry)
		}
	}
	return append(result, prefix+value)
}
