package main

import (
	"errors"
	"flag"
	"testing"
)

func TestSuggestFlagOrderAndPermissions(t *testing.T) {
	for _, args := range [][]string{{"./web/api/v1", "--history=h.json"}, {"--history=h.json", "./web/api/v1"}} {
		options, err := parseSuggestFlags(args)
		if err != nil || options.target != "./web/api/v1" || options.history != "h.json" || options.allowExports || options.source != "." {
			t.Fatal(options, err)
		}
	}
	options, err := parseSuggestFlags([]string{"p", "--history=h.json", "--cache-evidence=cache.json"})
	if err != nil || options.cacheEvidence != "cache.json" {
		t.Fatal(options, err)
	}
	for _, args := range [][]string{nil, {"--history=h"}, {"p"}, {"p", "q", "--history=h"}, {"p", "--history=h", "--top=0"}} {
		if _, err := parseSuggestFlags(args); err != nil {
			continue
		}
		t.Fatal("invalid arguments accepted", args)
	}
	if _, err := parseSuggestFlags([]string{"-h"}); !errors.Is(err, flag.ErrHelp) {
		t.Fatal(err)
	}
	if err := run([]string{"suggest", "-h"}); err != nil {
		t.Fatal(err)
	}
}
