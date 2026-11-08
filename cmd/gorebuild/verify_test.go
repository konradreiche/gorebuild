package main

import (
	"errors"
	"flag"
	"testing"
)

func TestVerifyRequiresExplicitRevisionsAndArtifact(t *testing.T) {
	got, err := parseVerifyFlags([]string{"./cmd/app", "--before=main", "--after=refactor", "--package=./leaf", "--out=result.json", "--runs=3"})
	if err != nil || got.options.Before != "main" || got.options.After != "refactor" || got.options.Target != "./cmd/app" || got.options.Runs != 3 {
		t.Fatal(got, err)
	}
	got, err = parseVerifyFlags([]string{"./cmd/app", "--before=a", "--after=b", "--package=./leaf", "--out=x", "--test-package=./api", "--historical-commit=abc123", "--runtime-arg=--version", "--after-test-overlay=api/extra_test.go=/tmp/extra.go"})
	if err != nil || len(got.options.HistoricalCommits) != 1 || len(got.options.TestPackages) != 1 || len(got.options.AfterTestOverlays) != 1 || len(got.options.RuntimeArgs) != 1 {
		t.Fatal("non-build repeated values rejected", got, err)
	}
	for _, args := range [][]string{nil, {"./cmd/app", "--before=main"}, {"./cmd/app", "--before=a", "--after=b", "--package=p"}, {"./cmd/app", "--before=a", "--after=b", "--package=p", "--out=x", "--runs=0"}} {
		if _, err := parseVerifyFlags(args); err != nil {
			continue
		}
		t.Fatal("incomplete verification options accepted", args)
	}
	if _, err := parseVerifyFlags([]string{"-h"}); !errors.Is(err, flag.ErrHelp) {
		t.Fatal(err)
	}
	if err := run([]string{"verify", "-h"}); err != nil {
		t.Fatal(err)
	}
}
