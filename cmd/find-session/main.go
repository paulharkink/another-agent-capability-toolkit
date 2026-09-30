package main

import (
	"context"
	"fmt"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/packagehelpers"
	"golang.org/x/term"
	"os"
)

func main() {
	home, err := os.UserHomeDir()
	if err == nil {
		err = packagehelpers.RunSessionSearch(context.Background(), os.Args[1:], home, term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd())))
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
