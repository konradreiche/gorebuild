package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/konradreiche/gorebuild/internal/history"
	"github.com/konradreiche/gorebuild/internal/presentation"
)

const historyUsage = `Usage: gorebuild history [flags] <one-go-package> [flags]

Analyze first-parent Git change events against the current target's import graph.
By default only reverse-dependency reach is estimated. --replay N measures sampled
historical rebuilds in temporary worktrees, with a fresh cache per event. This can
require N cold builds. Only replay trusted repositories; this is not a sandbox.
Graph discovery uses an isolated Go cache and -mod=readonly by default.
Use -build-flag=-mod=vendor for vendored dependencies. Module downloads are shared.

Options:`

type historyFlags struct {
	target        string
	since         string
	maxCommits    int
	replay        int
	compareVCS    bool
	packageFilter string
	output        string
	json          bool
	details       bool
	top           int
	goBinary      string
	timeout       time.Duration
	buildFlags    flags
}

func runHistory(args []string) error {
	opts, err := parseHistoryFlags(args)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	ctx := context.Background()
	if opts.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.timeout)
		defer cancel()
	}
	report, err := history.Run(ctx, history.Options{
		Target: opts.target, Since: opts.since, MaxCommits: opts.maxCommits,
		Package: opts.packageFilter, OutputFile: opts.output,
		GoBinary: opts.goBinary, BuildFlags: opts.buildFlags, Replay: opts.replay, CompareVCS: opts.compareVCS,
	})
	if err != nil {
		return err
	}
	if opts.json {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(report)
	}
	_, err = fmt.Print(history.Render(report, presentation.Options{Details: opts.details, Top: opts.top}))
	return err
}

func parseHistoryFlags(args []string) (historyFlags, error) {
	fs := flag.NewFlagSet("history", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), historyUsage)
		fs.PrintDefaults()
	}
	var opts historyFlags
	fs.BoolVar(&opts.details, "details", false, "show distributions, dependency paths, exclusions and diagnostics")
	fs.StringVar(&opts.since, "since", "90d", "history start: days (90d), UTC date, or RFC3339 timestamp")
	fs.IntVar(&opts.maxCommits, "max-commits", 0, "optional first-parent event limit (0 analyzes the entire requested period)")
	fs.IntVar(&opts.replay, "replay", 0, "sample up to N historical events; each requires a cold parent build (0 disables)")
	fs.BoolVar(&opts.compareVCS, "compare-vcs", false, "also replay with -buildvcs=false in independent caches (doubles cold builds)")
	fs.StringVar(&opts.packageFilter, "package", "", "inspect a repository-relative directory, local path, or full import path")
	fs.StringVar(&opts.output, "out", "", "save all events, graphs, replay evidence and logs to one new JSON file (.gz compresses)")
	fs.BoolVar(&opts.json, "json", false, "print complete machine-readable report to stdout")
	fs.IntVar(&opts.top, "top", 10, "maximum rows or representative events to show (JSON is not truncated)")
	fs.StringVar(&opts.goBinary, "go", "go", "Go executable to invoke")
	fs.DurationVar(&opts.timeout, "timeout", 0, "deadline for the whole analysis including replay, e.g. 30m (0 disables)")
	fs.Var(&opts.buildFlags, "build-flag", "Go build flag for graph selection and replay (repeatable)")
	if err := parseInterspersed(fs, args); err != nil {
		return historyFlags{}, err
	}
	if fs.NArg() != 1 || fs.Arg(0) == "" {
		return historyFlags{}, errors.New("history expects exactly one package target")
	}
	opts.target = fs.Arg(0)
	if opts.maxCommits < 0 {
		return historyFlags{}, errors.New("--max-commits cannot be negative (0 means unlimited)")
	}
	if opts.replay < 0 || opts.replay > 100000 {
		return historyFlags{}, errors.New("--replay must be between 0 and 100000")
	}
	if opts.compareVCS && opts.replay == 0 {
		return historyFlags{}, errors.New("--compare-vcs requires --replay N")
	}
	if opts.top < 1 {
		return historyFlags{}, errors.New("--top must be positive")
	}
	if opts.timeout < 0 {
		return historyFlags{}, errors.New("--timeout cannot be negative")
	}
	if _, err := history.ParseSince(opts.since, time.Now()); err != nil {
		return historyFlags{}, err
	}
	return opts, nil
}

// flag.FlagSet normally stops at the first positional argument. History accepts
// the target-before-flags examples too, while preserving flag values and --.
func parseInterspersed(fs *flag.FlagSet, args []string) error {
	var flagArgs, positional []string
	for len(args) > 0 {
		arg := args[0]
		args = args[1:]
		if arg == "--" {
			positional = append(positional, args...)
			break
		}
		if arg == "-" || !strings.HasPrefix(arg, "-") {
			positional = append(positional, arg)
			continue
		}
		flagArgs = append(flagArgs, arg)
		name, _, hasValue := strings.Cut(strings.TrimPrefix(strings.TrimPrefix(arg, "-"), "-"), "=")
		definition := fs.Lookup(name)
		if definition == nil || hasValue {
			continue // Let FlagSet report unknown flags or validate =values.
		}
		boolean, ok := definition.Value.(interface{ IsBoolFlag() bool })
		if ok && boolean.IsBoolFlag() {
			continue
		}
		if len(args) == 0 {
			return fmt.Errorf("flag needs an argument: %s", arg)
		}
		flagArgs = append(flagArgs, args[0])
		args = args[1:]
	}
	return fs.Parse(append(append(flagArgs, "--"), positional...))
}
