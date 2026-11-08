package profile

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Options struct {
	Target     string
	GoBinary   string
	BuildFlags []string
	OutputFile string
}

type Metadata struct {
	Target       string   `json:"target"`
	ImportPath   string   `json:"import_path"`
	GoVersion    string   `json:"go_version"`
	GOOS         string   `json:"goos"`
	GOARCH       string   `json:"goarch"`
	GOFLAGS      string   `json:"goflags,omitempty"`
	BuildFlags   []string `json:"build_flags"`
	GOMODCACHE   string   `json:"gomodcache"`
	GOWORK       string   `json:"gowork,omitempty"`
	TimestampUTC string   `json:"timestamp_utc"`
	CachePolicy  string   `json:"cache_policy"`
}

type BuildRun struct {
	WallTimeNS int64        `json:"wall_time_ns"`
	Graph      GraphSummary `json:"graph"`
}

type Report struct {
	SchemaVersion int      `json:"schema_version"`
	Metadata      Metadata `json:"metadata"`
	LocalPackages []string `json:"local_packages"`
	Cold          BuildRun `json:"cold"`
	WarmNoop      BuildRun `json:"warm_noop"`
}

// ProfileFile is the self-contained, versioned on-disk capture format.
// The raw action graphs retain fields not currently understood by the
// analyzer, allowing them to be inspected or reprocessed later.
type ProfileFile struct {
	FormatVersion int                  `json:"format_version"`
	Report        Report               `json:"report"`
	ActionGraphs  CapturedActionGraphs `json:"action_graphs"`
	BuildLogs     CapturedBuildLogs    `json:"build_logs"`
}

type CapturedActionGraphs struct {
	Cold     json.RawMessage `json:"cold"`
	WarmNoop json.RawMessage `json:"warm_noop"`
}

type CapturedBuildLogs struct {
	Cold     string `json:"cold"`
	WarmNoop string `json:"warm_noop"`
}

type listedPackage struct {
	ImportPath string `json:"ImportPath"`
	Name       string `json:"Name"`
	DepOnly    bool   `json:"DepOnly"`
	Module     *struct {
		Main bool `json:"Main"`
	} `json:"Module"`
}

func Run(ctx context.Context, options Options) (Report, error) {
	if options.Target == "" {
		return Report{}, errors.New("empty package target")
	}
	if options.GoBinary == "" {
		options.GoBinary = "go"
	}
	if options.OutputFile != "" {
		if _, err := os.Stat(options.OutputFile); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return Report{}, err
			}
		} else {
			return Report{}, fmt.Errorf("output file %q already exists; refusing to overwrite", options.OutputFile)
		}
	}
	workDir, err := os.MkdirTemp("", "gorebuild-profile-*")
	if err != nil {
		return Report{}, err
	}
	defer os.RemoveAll(workDir)
	cwd, err := os.Getwd()
	if err != nil {
		return Report{}, err
	}
	// Separate caches: the go list preflight must not accidentally warm the
	// cache that we intend to measure as empty.
	preflightCache := filepath.Join(workDir, "list-cache")
	buildCache := filepath.Join(workDir, "build-cache")
	for _, p := range []string{preflightCache, buildCache, filepath.Join(workDir, "binary")} {
		if err := os.MkdirAll(p, 0o700); err != nil {
			return Report{}, err
		}
	}

	var envInfo struct {
		GOVERSION  string `json:"GOVERSION"`
		GOOS       string `json:"GOOS"`
		GOARCH     string `json:"GOARCH"`
		GOFLAGS    string `json:"GOFLAGS"`
		GOMODCACHE string `json:"GOMODCACHE"`
		GOWORK     string `json:"GOWORK"`
	}
	if err := goJSON(ctx, options.GoBinary, cwd, withCache(preflightCache), &envInfo, "env", "-json", "GOVERSION", "GOOS", "GOARCH", "GOFLAGS", "GOMODCACHE", "GOWORK"); err != nil {
		return Report{}, fmt.Errorf("go env: %w", err)
	}

	listArgs := append([]string{"list", "-deps", "-json"}, options.BuildFlags...)
	listArgs = append(listArgs, options.Target)
	listCmd := exec.CommandContext(ctx, options.GoBinary, listArgs...)
	listCmd.Dir = cwd
	listCmd.Env = withCache(preflightCache)
	listBytes, err := listCmd.CombinedOutput()
	if err != nil {
		return Report{}, fmt.Errorf("go list failed: %w\n%s", err, tail(string(listBytes), 4096))
	}
	dec := json.NewDecoder(bytes.NewReader(listBytes))
	localSet := map[string]bool{}
	var roots []listedPackage
	for {
		var pkg listedPackage
		if err := dec.Decode(&pkg); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return Report{}, fmt.Errorf("parse go list -deps -json: %w", err)
		}
		if pkg.Module != nil && pkg.Module.Main {
			localSet[pkg.ImportPath] = true
		}
		if !pkg.DepOnly {
			roots = append(roots, pkg)
		}
	}
	if len(roots) != 1 {
		return Report{}, fmt.Errorf("profile expects exactly one package, go list returned %d roots", len(roots))
	}
	var report Report
	for name := range localSet {
		report.LocalPackages = append(report.LocalPackages, name)
	}
	sort.Strings(report.LocalPackages)

	report.SchemaVersion = 1
	report.Metadata = Metadata{
		Target: options.Target, ImportPath: roots[0].ImportPath,
		GoVersion: envInfo.GOVERSION, GOOS: envInfo.GOOS, GOARCH: envInfo.GOARCH,
		GOFLAGS: envInfo.GOFLAGS, BuildFlags: append([]string{}, options.BuildFlags...),
		GOMODCACHE: envInfo.GOMODCACHE, GOWORK: envInfo.GOWORK,
		TimestampUTC: time.Now().UTC().Format(time.RFC3339),
		CachePolicy:  "empty isolated GOCACHE for cold, reused unchanged for warm; inherited GOMODCACHE",
	}
	binary := filepath.Join(workDir, "binary", "build-output")
	if envInfo.GOOS == "windows" {
		binary += ".exe"
	}

	captures := make(map[string]*BuildCapture)
	profileBuild := func(name string) (BuildRun, error) {
		capture := CaptureBuild(ctx, BuildOptions{
			Directory: cwd, GoBinary: options.GoBinary, Target: options.Target,
			BuildFlags: options.BuildFlags, CacheDir: buildCache, OutputPath: binary,
		})
		if ctx.Err() != nil {
			return BuildRun{}, fmt.Errorf("%s build: %w", name, ctx.Err())
		}
		if capture.Failure != "" {
			return BuildRun{}, fmt.Errorf("%s: %s", name, capture.Failure)
		}
		captures[name] = capture
		return capture.Run, nil
	}
	if report.Cold, err = profileBuild("cold"); err != nil {
		return Report{}, err
	}
	if report.WarmNoop, err = profileBuild("warm"); err != nil {
		return Report{}, err
	}
	if options.OutputFile != "" {
		capture := ProfileFile{FormatVersion: 1, Report: report}
		for _, item := range []struct {
			name  string
			graph *json.RawMessage
			log   *string
		}{
			{"cold", &capture.ActionGraphs.Cold, &capture.BuildLogs.Cold},
			{"warm", &capture.ActionGraphs.WarmNoop, &capture.BuildLogs.WarmNoop},
		} {
			*item.graph = captures[item.name].ActionGraph
			*item.log = captures[item.name].Log
		}
		if err := writeProfileFile(options.OutputFile, capture); err != nil {
			return Report{}, err
		}
	}
	return report, nil
}

func goJSON(ctx context.Context, executable, dir string, env []string, dest any, args ...string) error {
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Dir = dir
	cmd.Env = env
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, tail(string(output), 4096))
	}
	return json.Unmarshal(output, dest)
}

func withCache(path string) []string {
	var env []string
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GOCACHE=") {
			env = append(env, entry)
		}
	}
	return append(env, "GOCACHE="+path)
}

func tail(text string, max int) string {
	if len(text) > max {
		return "...\n" + text[len(text)-max:]
	}
	return text
}

// writeProfileFile creates exactly one output artifact. O_EXCL prevents
// overwriting a capture from a previous run, even if the path appears after
// the initial check. Remove a partial file if a write or close fails.
func writeProfileFile(path string, capture ProfileFile) error {
	data, err := json.MarshalIndent(capture, "", "  ")
	if err != nil {
		return fmt.Errorf("encode profile: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("create output file %q: %w", path, err)
	}
	var output io.Writer = file
	var compressed *gzip.Writer
	if strings.HasSuffix(path, ".gz") {
		compressed = gzip.NewWriter(file)
		output = compressed
	}
	_, err = output.Write(append(data, '\n'))
	if compressed != nil {
		err = errors.Join(err, compressed.Close())
	}
	err = errors.Join(err, file.Close())
	if err != nil {
		_ = os.Remove(path)
	}
	return err
}
