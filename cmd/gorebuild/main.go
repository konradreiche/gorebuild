package main

import (
	"errors"
	"fmt"
	"os"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "gorebuild:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: gorebuild <profile|history|explain|suggest|verify> [flags] (try 'gorebuild explain -h', 'gorebuild history -h' or 'gorebuild profile -h')")
	}
	switch args[0] {
	case "profile":
		return runProfile(args[1:])
	case "history":
		return runHistory(args[1:])
	case "explain":
		return runExplain(args[1:])
	case "suggest":
		return runSuggest(args[1:])
	case "verify":
		return runVerify(args[1:])
	default:
		return fmt.Errorf("unknown command %q (try 'gorebuild explain -h', 'gorebuild history -h' or 'gorebuild profile -h')", args[0])
	}
}
