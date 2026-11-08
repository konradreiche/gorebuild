package main

import (
	"errors"
	"flag"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseProfileFlagsDefaults(t *testing.T) {
	opts, err := parseProfileFlags([]string{"./cmd/gorebuild"})
	if err != nil {
		t.Fatal(err)
	}
	want := profileFlags{target: "./cmd/gorebuild", top: 10, goBinary: "go"}
	if !reflect.DeepEqual(opts, want) {
		t.Fatalf("got %+v, want %+v", opts, want)
	}
}

func TestParseProfileFlags(t *testing.T) {
	opts, err := parseProfileFlags([]string{
		"-out", "report.json", "-json", "-top", "5", "-dependency-order", "-go", "/custom/go",
		"-timeout", "2m", "-build-flag=-tags=integration", "-build-flag=--race", ".",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := profileFlags{
		target: ".", output: "report.json", json: true, top: 5, dependencyOrder: true,
		goBinary: "/custom/go", timeout: 2 * time.Minute,
		buildFlags: flags{"-tags=integration", "--race"},
	}
	if !reflect.DeepEqual(opts, want) {
		t.Fatalf("got %+v, want %+v", opts, want)
	}
}

func TestParseProfileFlagsInvalid(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{""},
		{".", "./other"},
		{"-top=0", "."},
		{"-top=-1", "."},
		{"-timeout=-1s", "."},
		{"-timeout=bad", "."},
		{"-unknown", "."},
		{".", "-top", "5"},
		{"-build-flag=tags=integration", "."},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			if _, err := parseProfileFlags(args); err != nil {
				return
			}
			t.Fatalf("expected error for %q", args)
		})
	}
}

func TestManagedBuildFlags(t *testing.T) {
	for _, name := range []string{"o", "C", "debug-actiongraph", "n", "work"} {
		for _, prefix := range []string{"-", "--"} {
			for _, suffix := range []string{"", "=value"} {
				value := prefix + name + suffix
				t.Run(value, func(t *testing.T) {
					var values flags
					if err := values.Set(value); err != nil {
						if len(values) != 0 {
							t.Fatal("invalid flag appended")
						}
						return
					}
					t.Fatalf("accepted managed flag %q", value)
				})
			}
		}
	}
}

func TestProfileHelp(t *testing.T) {
	if _, err := parseProfileFlags([]string{"-h"}); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("got %v, want flag.ErrHelp", err)
	}
	if err := run([]string{"profile", "-h"}); err != nil {
		t.Fatal(err)
	}
}

func TestRunDispatch(t *testing.T) {
	for _, args := range [][]string{nil, {"unknown"}} {
		if err := run(args); err != nil {
			continue
		}
		t.Fatalf("expected error for %q", args)
	}
}
