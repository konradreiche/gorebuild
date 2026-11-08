package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/konradreiche/gorebuild/internal/artifact"
	"github.com/konradreiche/gorebuild/internal/explain"
	"github.com/konradreiche/gorebuild/internal/presentation"
)

const explainUsage = `Usage: gorebuild explain --history capture.json[.gz] (--commit HASH | --package PKG) [flags]

Diagnose saved history without builds by default.
Package paths are relative to the captured repository. Commit prefixes must be unique.
Opt-in go/packages inspection uses real cached export data; compiling missing exports
requires --allow-export-builds. --replay-cache explicitly runs an isolated historical
parent/warm/child build sequence for one event and retains selected cache-hash evidence.
Source analysis and cache diagnosis are not refactoring plans.

Options:`

type explainFlags struct {
	history           string
	commit            string
	pkg               string
	top               int
	consumer          string
	source            string
	allowExportBuilds bool
	cachePackage      string
	output            string
	details           bool
}

func parseExplainFlags(args []string) (explainFlags, error) {
	fs := flag.NewFlagSet("explain", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { fmt.Fprintln(fs.Output(), explainUsage); fs.PrintDefaults() }
	var options explainFlags
	fs.BoolVar(&options.details, "details", false, "show additional paths, typed declarations, counterfactual bounds and diagnostics")
	fs.StringVar(&options.history, "history", "", "existing history JSON or gzip-compressed JSON artifact")
	fs.StringVar(&options.commit, "commit", "", "full historical commit hash or unique prefix")
	fs.StringVar(&options.pkg, "package", "", "package import path or captured repository-relative directory")
	fs.IntVar(&options.top, "top", 5, "maximum expensive actions, examples and dependency relationships to display")
	fs.StringVar(&options.consumer, "inspect-consumer", "", "opt-in targeted type inspection of this consumer importing --package")
	fs.StringVar(&options.source, "source", "", "explicit checkout for current-source inspection or isolated cache replay")
	fs.BoolVar(&options.allowExportBuilds, "allow-export-builds", false, "permit go/packages to compile missing dependency export data (requires --inspect-consumer)")
	fs.StringVar(&options.cachePackage, "replay-cache", "", "EXPLICITLY BUILD cold parent/warm parent/child for this package's cache-hash diagnostics (requires --commit, --source, --out)")
	fs.StringVar(&options.output, "out", "", "new JSON file for complete selected declaration/diagnostic evidence (.gz compresses)")
	if err := fs.Parse(args); err != nil {
		return explainFlags{}, err
	}
	if fs.NArg() != 0 {
		return explainFlags{}, errors.New("explain accepts flags only, not positional targets")
	}
	if options.history == "" {
		return explainFlags{}, errors.New("--history is required")
	}
	if (options.commit == "") == (options.pkg == "") {
		return explainFlags{}, errors.New("select exactly one of --commit or --package")
	}
	if options.top < 1 {
		return explainFlags{}, errors.New("--top must be positive")
	}
	if options.consumer != "" && (options.pkg == "" || options.source == "") {
		return explainFlags{}, errors.New("--inspect-consumer requires --package and an explicit --source checkout")
	}
	if options.source != "" && options.consumer == "" && options.cachePackage == "" {
		return explainFlags{}, errors.New("--source is only used with --inspect-consumer or --replay-cache")
	}
	if options.allowExportBuilds && options.consumer == "" {
		return explainFlags{}, errors.New("--allow-export-builds requires --inspect-consumer")
	}
	if options.cachePackage != "" && (options.commit == "" || options.source == "" || options.output == "") {
		return explainFlags{}, errors.New("--replay-cache requires --commit, --source and --out; it runs new builds")
	}
	return options, nil
}

func runExplain(args []string) error {
	options, err := parseExplainFlags(args)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	var output *os.File
	var outputComplete bool
	if options.output != "" {
		output, err = os.OpenFile(options.output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		defer func() {
			output.Close()
			if !outputComplete {
				os.Remove(options.output)
			}
		}()
	}
	data, err := explain.Read(options.history)
	if err != nil {
		return err
	}
	text, err := explain.Describe(data, explain.Options{Commit: options.commit, Package: options.pkg, Top: options.top})
	if err != nil {
		return err
	}
	var inspection *explain.Inspection
	var cache *explain.CacheDiagnostic
	if options.consumer != "" {
		dependency, err := data.ResolvePackage(options.pkg)
		if err != nil {
			return err
		}
		inspection, err = explain.InspectRelationship(ctx, data, dependency, options.consumer, options.source, options.top, explain.TypeOptions{AllowExportBuilds: options.allowExportBuilds})
		if err != nil {
			return err
		}
		text += inspection.Text
	}
	if options.cachePackage != "" {
		fmt.Fprintln(os.Stderr, "Explicit cache replay: cold parent, warm verification, then changed revision; isolated cache/worktree. This may be expensive.")
		cache, err = explain.ReplayCache(ctx, data, options.commit, options.cachePackage, options.source)
		if err != nil {
			return err
		}
	}
	rendered, err := explain.Render(data, explain.Options{Commit: options.commit, Package: options.pkg, Top: options.top}, inspection, cache, presentation.Options{Details: options.details, Top: options.top})
	if err != nil {
		return err
	}
	if _, err := fmt.Print(rendered); err != nil {
		return err
	}
	if output != nil {
		result := struct {
			SchemaVersion int                      `json:"schema_version"`
			History       string                   `json:"history"`
			Explanation   string                   `json:"explanation"`
			Inspection    *explain.Inspection      `json:"inspection,omitempty"`
			Cache         *explain.CacheDiagnostic `json:"cache_diagnostic,omitempty"`
		}{1, options.history, text, inspection, cache}
		if err := artifact.Encode(output, options.output, result); err != nil {
			return err
		}
		if err := output.Close(); err != nil {
			return err
		}
		outputComplete = true
	}
	return nil
}
