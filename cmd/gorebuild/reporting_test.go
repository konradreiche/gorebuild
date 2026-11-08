package main

import "testing"

func TestDetailsFlagsAcrossCommands(t *testing.T) {
	profile, err := parseProfileFlags([]string{"--details", "--out=x.json.gz", "."})
	if err != nil || !profile.details || profile.output != "x.json.gz" {
		t.Fatal(profile, err)
	}
	history, err := parseHistoryFlags([]string{".", "--details", "--out=x.json.gz"})
	if err != nil || !history.details {
		t.Fatal(history, err)
	}
	explain, err := parseExplainFlags([]string{"--history=h.json.gz", "--package=api", "--details", "--out=x.json.gz"})
	if err != nil || !explain.details {
		t.Fatal(explain, err)
	}
	suggest, err := parseSuggestFlags([]string{"api", "--history=h.json.gz", "--details", "--out=x.json.gz"})
	if err != nil || !suggest.details {
		t.Fatal(suggest, err)
	}
	verify, err := parseVerifyFlags([]string{".", "--before=old", "--after=new", "--package=leaf", "--details", "--out=x.json.gz"})
	if err != nil || !verify.details {
		t.Fatal(verify, err)
	}
}
