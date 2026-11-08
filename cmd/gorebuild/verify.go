package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/konradreiche/gorebuild/internal/presentation"
	"github.com/konradreiche/gorebuild/internal/verify"
)

const verifyUsage = `Usage: gorebuild verify TARGET --before REF --after REF --package PACKAGE --out FILE [flags]

Verify existing explicit Git revisions; never generate or apply a refactor.
EXPENSIVE: correctness builds/tests, then independent cold/warm/controlled-change
scenarios and same-revision controls, with cold benchmarking kept separate. Only trusted repositories: builds
and tests execute code, temporary worktrees are not a sandbox. Developer checkout
and normal GOCACHE are not used as build inputs/cache. Module downloads are shared.
Primary experiment pins -buildvcs=false and read-only modules. Default scenarios
are implementation-only and exported edits; historical patches are opt-in.
Identical frozen edits are applied only inside disposable worktrees.

Options:`

type verifyFlags struct {
	options                                           verify.Options
	timeout                                           time.Duration
	details                                           bool
	buildFlags                                        flags
	testPackages                                      verifyStrings
	scenarios                                         string
	historicalCommits, runtimeArgs, afterTestOverlays verifyStrings
}

type verifyStrings []string

func (values *verifyStrings) String() string { return strings.Join(*values, ",") }
func (values *verifyStrings) Set(value string) error {
	if value == "" {
		return errors.New("value must not be empty")
	}
	*values = append(*values, value)
	return nil
}

func parseVerifyFlags(args []string) (verifyFlags, error) {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { fmt.Fprintln(fs.Output(), verifyUsage); fs.PrintDefaults() }
	var value verifyFlags
	fs.BoolVar(&value.details, "details", false, "show supporting measurements, distributions, diagnostics and limitations")
	fs.StringVar(&value.options.Before, "before", "", "explicit baseline revision")
	fs.StringVar(&value.options.After, "after", "", "explicit existing refactored revision")
	fs.StringVar(&value.options.Package, "package", "", "package whose compilation inputs receive identical controlled scenario edits")
	fs.StringVar(&value.options.OutputFile, "out", "", "new single JSON evidence artifact (.gz compresses); never overwritten")
	fs.IntVar(&value.options.Runs, "runs", 3, "incremental paired repetitions per scenario (cold repetitions are separate)")
	fs.StringVar(&value.scenarios, "scenario", "implementation,exported", "comma-separated scenarios: implementation,exported,historical")
	fs.IntVar(&value.options.ColdRuns, "cold-runs", 3, "independent cold pairs, separate from incremental repetitions")
	fs.BoolVar(&value.options.Controls, "same-revision-control", true, "pair independent same-before-revision cache copies to estimate timing noise")
	fs.StringVar(&value.options.HistoryFile, "history", "", "existing history artifact: automatically select one isolated applicable historical edit")
	fs.Var(&value.historicalCommits, "historical-commit", "explicit historical source commit (repeatable); applicability and scope checked")
	fs.StringVar(&value.options.HistoricalDirection, "historical-direction", "auto", "historical patch direction: auto,forward,reverse (inverse edits labeled)")
	fs.BoolVar(&value.options.HistoricalSubset, "historical-subset", false, "permit selected-package subset of multi-file/package event; never label as isolated event")
	fs.BoolVar(&value.options.RaceTests, "race-tests", false, "run correctness tests with race detector (untimed)")
	fs.Var(&value.afterTestOverlays, "after-test-overlay", "new repository-relative _test.go=local file; correctness-only after-revision tests, never benchmark source (repeatable)")
	fs.Var(&value.runtimeArgs, "runtime-arg", "explicit executable smoke-check argument, e.g. --runtime-arg=--version (repeatable)")
	fs.StringVar(&value.options.GoBinary, "go", "go", "Go executable; selected GOROOT is pinned for both revisions")
	fs.Var(&value.buildFlags, "build-flag", "consistent Go build flag (repeatable; buildvcs/output/cache-affecting control flags managed)")
	fs.Var(&value.testPackages, "test-package", "correctness-test package (repeatable); default: changed Go packages in target closure plus controlled package")
	fs.DurationVar(&value.timeout, "timeout", 0, "whole verification deadline, e.g. 90m; zero disables")
	if err := parseInterspersed(fs, args); err != nil {
		return verifyFlags{}, err
	}
	if fs.NArg() != 1 || value.options.Before == "" || value.options.After == "" || value.options.Package == "" || value.options.OutputFile == "" {
		return verifyFlags{}, errors.New("verify requires one target and explicit --before, --after, --package, --out")
	}
	if value.options.Runs < 1 || value.options.Runs > 100 {
		return verifyFlags{}, errors.New("--runs must be between 1 and 100")
	}
	if value.timeout < 0 {
		return verifyFlags{}, errors.New("--timeout must not be negative")
	}
	if value.options.ColdRuns < 1 || value.options.ColdRuns > 100 {
		return verifyFlags{}, errors.New("--cold-runs must be between 1 and 100")
	}
	value.options.Scenarios = strings.Split(value.scenarios, ",")
	for _, kind := range value.options.Scenarios {
		if kind != "implementation" && kind != "exported" && kind != "historical" {
			return verifyFlags{}, errors.New("unsupported --scenario")
		}
		if kind == "historical" && len(value.historicalCommits) == 0 && value.options.HistoryFile == "" {
			return verifyFlags{}, errors.New("historical scenario requires --history or --historical-commit")
		}
	}
	value.options.HistoricalCommits = value.historicalCommits
	value.options.RuntimeArgs = value.runtimeArgs
	value.options.AfterTestOverlays = value.afterTestOverlays
	value.options.Target = fs.Arg(0)
	value.options.BuildFlags = value.buildFlags
	value.options.TestPackages = value.testPackages
	return value, nil
}

func runVerify(args []string) error {
	options, err := parseVerifyFlags(args)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if options.timeout > 0 {
		var stop context.CancelFunc
		ctx, stop = context.WithTimeout(ctx, options.timeout)
		defer stop()
	}
	fmt.Fprintln(os.Stderr, "Explicit verification: correctness builds/tests and paired cold/incremental builds in isolated worktrees/caches. This may take substantial time and disk space.")
	if options.details {
		options.options.Progress = os.Stderr
	}
	report := verify.Run(ctx, options.options)
	if _, err := fmt.Print(verify.Render(report, presentation.Options{Details: options.details})); err != nil {
		return err
	}
	if len(report.Errors) > 0 {
		return errors.New("verification failed; see diagnostics and --out artifact")
	}
	return nil
}
