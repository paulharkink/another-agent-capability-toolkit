package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/release"
)

func main() {
	bump := flag.String("bump", "", "major, minor, or patch")
	readmePath := flag.String("readme", "README.md", "README file to update")
	flag.Parse()

	readme, err := os.ReadFile(*readmePath)
	if err != nil {
		fail(err)
	}
	current, err := release.ReadmeInstallVersion(string(readme))
	if err != nil {
		fail(err)
	}
	next, err := release.NextVersion(current, *bump)
	if err != nil {
		fail(err)
	}
	updated, err := release.UpdateReadmeInstallVersion(string(readme), next)
	if err != nil {
		fail(err)
	}
	info, err := os.Stat(*readmePath)
	if err != nil {
		fail(err)
	}
	if err := os.WriteFile(*readmePath, []byte(updated), info.Mode()); err != nil {
		fail(err)
	}
	fmt.Println(next)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
