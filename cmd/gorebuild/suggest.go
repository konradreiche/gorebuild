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

const suggestUsage = `Usage: gorebuild suggest PACKAGE --history capture.json[.gz] [flags]

Investigate incoming/outgoing dependency boundaries and compiler-profiling areas
using measured rebuilds, typed source, Git history and optional saved cache inputs. Recommendations only: no source edits, target
builds or historical replays. Go package loading uses -n unless export compilation
is explicitly permitted. Structural proposals and fixed-DAG bounds are conditional,
not verified refactoring savings. Paths are captured-repository-relative.

Options:`

type suggestFlags struct {
	target, history, source, consumer, output, cacheEvidence string
	top                                                      int
	allowExports                                             bool
	details                                                  bool
}

func parseSuggestFlags(args []string) (suggestFlags, error) {
	fs := flag.NewFlagSet("suggest", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { fmt.Fprintln(fs.Output(), suggestUsage); fs.PrintDefaults() }
	var options suggestFlags
	fs.BoolVar(&options.details, "details", false, "show individual rejections, constraints and modeled bounds")
	fs.StringVar(&options.history, "history", "", "existing history artifact with measured replay evidence")
	fs.StringVar(&options.source, "source", ".", "current repository root (read-only source and Git inspection)")
	fs.StringVar(&options.consumer, "consumer", "", "restrict to one captured direct consumer (default: all direct repository consumers in target closure)")
	fs.StringVar(&options.cacheEvidence, "cache-evidence", "", "existing selected explain cache diagnostic JSON; reanalyzed without builds")
	fs.StringVar(&options.output, "out", "", "new JSON file containing all groups, rejections, edges and historical/model evidence (.gz compresses)")
	fs.IntVar(&options.top, "top", 5, "maximum candidate explanations in terminal; JSON remains complete")
	fs.BoolVar(&options.allowExports, "allow-export-builds", false, "permit compilation of dependency export data; no target builds or links")
	if err := parseInterspersed(fs, args); err != nil {
		return suggestFlags{}, err
	}
	if fs.NArg() != 1 || options.history == "" {
		return suggestFlags{}, errors.New("suggest requires one package and --history")
	}
	if options.top < 1 {
		return suggestFlags{}, errors.New("--top must be positive")
	}
	options.target = fs.Arg(0)
	return options, nil
}

func runSuggest(args []string) error {
	options, err := parseSuggestFlags(args)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	var output *os.File
	var complete bool
	if options.output != "" {
		output, err = os.OpenFile(options.output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		defer func() {
			output.Close()
			if !complete {
				os.Remove(options.output)
			}
		}()
	}
	data, err := explain.Read(options.history)
	if err != nil {
		return err
	}
	report, err := explain.Suggest(ctx, data, options.target, options.source, options.consumer, explain.TypeOptions{AllowExportBuilds: options.allowExports}, options.cacheEvidence)
	if err != nil {
		return err
	}
	if _, err := fmt.Print(report.Render(data, presentation.Options{Details: options.details, Top: options.top})); err != nil {
		return err
	}
	if output != nil {
		if err := artifact.Encode(output, options.output, report); err != nil {
			return err
		}
		if err := output.Close(); err != nil {
			return err
		}
		complete = true
	}
	return nil
}
