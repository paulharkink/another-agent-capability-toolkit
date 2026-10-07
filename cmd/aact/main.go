package main

import (
	"context"
	"fmt"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/cli"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/packagehelpers"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if len(os.Args) > 1 && os.Args[1] == "__aact_internal_inspector_helper" {
		helper := &packagehelpers.Helper{OnStderr: func(b []byte) { os.Stderr.Write(b) }}
		if err := packagehelpers.ExecuteAction(ctx, os.Args[2:], os.Stdin, os.Stdout, helper.Run); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	os.Exit(cli.Run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
