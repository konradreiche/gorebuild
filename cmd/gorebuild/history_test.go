package main

import (
	"errors"
	"flag"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseHistoryDefaults(t *testing.T) {
	opts, err := parseHistoryFlags([]string{"./cmd/app"})
	want := historyFlags{target: "./cmd/app", since: "90d", maxCommits: 0, top: 10, goBinary: "go"}
	if err != nil || !reflect.DeepEqual(opts, want) {
		t.Fatalf("got %+v, %v; want %+v", opts, err, want)
	}
}

func TestParseHistoryInterspersedFlags(t *testing.T) {
	for _, args := range [][]string{
		{"./cmd/app", "--since", "30d", "--package", "./leaf", "--json", "--out=history.json", "--max-commits=40", "--timeout=1m", "--top=3", "--go=/custom/go", "--build-flag=-tags=test", "--replay=20"},
		{"--since=30d", "--package=./leaf", "--json", "--out", "history.json", "--max-commits", "40", "--timeout", "1m", "--top", "3", "--go", "/custom/go", "--build-flag=-tags=test", "--replay", "20", "./cmd/app"},
	} {
		opts, err := parseHistoryFlags(args)
		want := historyFlags{target: "./cmd/app", since: "30d", packageFilter: "./leaf", json: true, output: "history.json", maxCommits: 40, timeout: time.Minute, top: 3, goBinary: "/custom/go", buildFlags: flags{"-tags=test"}, replay: 20}
		if err != nil || !reflect.DeepEqual(opts, want) {
			t.Fatalf("got %+v, %v; want %+v", opts, err, want)
		}
	}
	opts, err := parseHistoryFlags([]string{"--", "-literal-target"})
	if err != nil || opts.target != "-literal-target" {
		t.Fatal("-- not respected", opts, err)
	}
}

func TestParseHistoryInvalid(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{""},
		{".", "./other"},
		{".", "--top=0"},
		{".", "--since=bad"},
		{".", "--since"},
		{".", "--max-commits=-1"},
		{".", "--timeout=-1s"},
		{".", "--unknown"},
		{".", "--build-flag=-o=bad"},
		{".", "--replay=-1"},
		{".", "--replay=100001"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			if _, err := parseHistoryFlags(args); err != nil {
				return
			}
			t.Fatalf("accepted %q", args)
		})
	}
}

func TestHistoryVCSComparisonFlags(t *testing.T) {
	opts, err := parseHistoryFlags([]string{"./cmd/app", "--replay", "3", "--compare-vcs"})
	if err != nil || !opts.compareVCS || opts.replay != 3 {
		t.Fatal("comparison options not accepted", opts, err)
	}
	if _, err := parseHistoryFlags([]string{"./cmd/app", "--compare-vcs"}); err != nil {
		return
	}
	t.Fatal("comparison accepted without an explicit replay budget")
}

func TestHistoryHelp(t *testing.T) {
	if _, err := parseHistoryFlags([]string{"--help"}); !errors.Is(err, flag.ErrHelp) {
		t.Fatal(err)
	}
	if err := run([]string{"history", "-h"}); err != nil {
		t.Fatal(err)
	}
}
