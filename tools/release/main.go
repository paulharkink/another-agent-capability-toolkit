package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/release"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
)

func main() {
	version := flag.String("version", "0.1.0-dev", "archive version")
	out := flag.String("out", "dist", "artifact directory")
	target := flag.String("target", "all", "all or OS/architecture")
	source := flag.String("source", ".", "project source root")
	tool := flag.String("go", "", "Go executable (development build only)")
	verifyOnly := flag.Bool("verify-only", false, "verify existing matching archives without compiling")
	flag.Parse()
	targets := []string{"darwin/amd64", "darwin/arm64", "linux/amd64", "linux/arm64", "windows/amd64", "windows/arm64"}
	if *target != "all" {
		targets = []string{*target}
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	builder := release.Builder{SourceDir: *source, Go: *tool}
	for _, t := range targets {
		parts := strings.Split(t, "/")
		if len(parts) != 2 {
			fmt.Fprintln(os.Stderr, "invalid target", t)
			os.Exit(1)
		}
		var artifact release.Artifact
		var err error
		if *verifyOnly {
			ext := ".tar.gz"
			if parts[0] == "windows" {
				ext = ".zip"
			}
			artifact.Path = filepath.Join(*out, "aact_"+*version+"_"+parts[0]+"_"+parts[1]+ext)
		} else {
			artifact, err = builder.Build(ctx, *version, parts[0], parts[1], *out)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if err = release.Verify(artifact.Path, parts[0], parts[1]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("%s  %s\n", artifact.SHA256, artifact.Path)
	}
}
