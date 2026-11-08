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

	"github.com/konradreiche/gorebuild/internal/presentation"
	"github.com/konradreiche/gorebuild/internal/profile"
)

const profileUsage = `Usage: gorebuild profile [flags] <one-go-package>

Run an empty-GOCACHE build followed by an unchanged, warmed build.
The module download cache is shared; the normal build cache is untouched.
Flags must appear before the package target.

Options:`

type profileFlags struct {
	target          string
	output          string
	json            bool
	details         bool
	top             int
	dependencyOrder bool
	goBinary        string
	timeout         time.Duration
	buildFlags      flags
}

func runProfile(args []string) error {
	opts, err := parseProfileFlags(args)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	return executeProfile(opts)
}

func executeProfile(opts profileFlags) error {
	ctx := context.Background()
	if opts.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.timeout)
		defer cancel()
	}
	report, err := profile.Run(ctx, profile.Options{
		Target:     opts.target,
		GoBinary:   opts.goBinary,
		BuildFlags: opts.buildFlags,
		OutputFile: opts.output,
	})
	if err != nil {
		return err
	}
	if opts.json {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}
	_, err = fmt.Print(profile.Render(report, presentation.Options{Details: opts.details, Top: opts.top}, opts.dependencyOrder))
	return err
}

func parseProfileFlags(args []string) (profileFlags, error) {
	fs := flag.NewFlagSet("profile", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), profileUsage)
		fs.PrintDefaults()
	}
	var opts profileFlags
	fs.BoolVar(&opts.details, "details", false, "show additional action breakdowns, configuration and diagnostics")
	fs.StringVar(&opts.output, "out", "", "save report, raw action graphs, and logs to one new JSON file (.gz compresses)")
	fs.BoolVar(&opts.json, "json", false, "print machine-readable report to stdout")
	fs.IntVar(&opts.top, "top", 10, "maximum critical-path actions to display (off-path list capped at 5)")
	fs.BoolVar(&opts.dependencyOrder, "dependency-order", false, "show critical-path actions in dependency order instead of ranked by duration")
	fs.StringVar(&opts.goBinary, "go", "go", "Go executable to invoke")
	fs.DurationVar(&opts.timeout, "timeout", 0, "deadline for the whole profile, e.g. 10m (0 disables)")
	fs.Var(&opts.buildFlags, "build-flag", "Go build flag (repeatable; use -build-flag=-tags=integration)")
	if err := fs.Parse(args); err != nil {
		return profileFlags{}, err
	}
	if fs.NArg() != 1 {
		return profileFlags{}, fmt.Errorf("expected exactly one package target, got %d; flags must appear before the target", fs.NArg())
	}
	opts.target = fs.Arg(0)
	if err := opts.validate(); err != nil {
		return profileFlags{}, err
	}
	return opts, nil
}

func (o profileFlags) validate() error {
	if o.target == "" {
		return errors.New("empty package target")
	}
	if o.top < 1 {
		return errors.New("-top must be positive")
	}
	if o.timeout < 0 {
		return errors.New("-timeout cannot be negative")
	}
	return nil
}

type flags []string

func (f *flags) Set(value string) error {
	// Go accepts both -name and --name, with an optional =value.
	name, _, _ := strings.Cut(value, "=")
	if !strings.HasPrefix(name, "-") {
		return fmt.Errorf("build flag %q must start with '-'", value)
	}
	name = strings.TrimPrefix(strings.TrimPrefix(name, "-"), "-")
	if name == "" || strings.HasPrefix(name, "-") {
		return fmt.Errorf("invalid build flag %q", value)
	}
	switch name {
	case "o", "C", "debug-actiongraph", "n", "work":
		return fmt.Errorf("-%s is managed by gorebuild and cannot be passed through", name)
	}
	*f = append(*f, value)
	return nil
}

func (f *flags) String() string { return strings.Join(*f, " ") }
