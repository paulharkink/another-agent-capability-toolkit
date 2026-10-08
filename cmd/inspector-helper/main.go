package main

import (
	"context"
	"fmt"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/packagehelpers"
	"io"
	"os"
)

type actionHandler = packagehelpers.ActionHandler

func execute(ctx context.Context, args []string, in io.Reader, out, diagnostics io.Writer, handler actionHandler) error {
	_ = diagnostics
	return packagehelpers.ExecuteAction(ctx, args, in, out, handler)
}
func main() {
	helper := &packagehelpers.Helper{OnStderr: func(b []byte) { os.Stderr.Write(b) }}
	if err := execute(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr, helper.Run); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
