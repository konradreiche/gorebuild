package main

import (
	"errors"
	"flag"
	"reflect"
	"testing"
)

func TestExplainFlags(t *testing.T) {
	got, err := parseExplainFlags([]string{"--history", "history.json.gz", "--commit", "503180645ef0"})
	want := explainFlags{history: "history.json.gz", commit: "503180645ef0", top: 5}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal(got, err)
	}
	got, err = parseExplainFlags([]string{"--history=h.json", "--package=./web/api/v1", "--inspect-consumer=./web", "--source=.", "--top=10"})
	if err != nil || got.consumer != "./web" || got.source != "." || got.top != 10 {
		t.Fatal(got, err)
	}
	got, err = parseExplainFlags([]string{"--history=h", "--commit=c", "--replay-cache=./p", "--source=.", "--out=cache.json"})
	if err != nil || got.cachePackage != "./p" {
		t.Fatal(got, err)
	}
	for _, args := range [][]string{
		nil,
		{"--history=h"},
		{"--package=p"},
		{"--history=h", "--commit=a", "--package=p"},
		{"--history=h", "--commit=c", "--replay-cache=p"},
		{"--history=h", "--package=p", "--replay-cache=p", "--source=.", "--out=c.json"},
		{"--history=h", "--commit=c", "--allow-export-builds"},
		{"--history=h", "--commit=a", "--top=0"},
		{"--history=h", "--package=p", "--inspect-consumer=c"},
		{"--history=h", "--commit=a", "--source=src"},
		{"--history=h", "--package=p", "extra"},
	} {
		if _, err := parseExplainFlags(args); err != nil {
			continue
		}
		t.Fatal("invalid options accepted", args)
	}
}

func TestExplainHelp(t *testing.T) {
	if _, err := parseExplainFlags([]string{"-h"}); !errors.Is(err, flag.ErrHelp) {
		t.Fatal(err)
	}
	if err := run([]string{"explain", "-h"}); err != nil {
		t.Fatal(err)
	}
}
