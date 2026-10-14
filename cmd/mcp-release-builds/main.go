package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
)

type outputBuild struct {
	PackageID    string `json:"package_id"`
	Profile      string `json:"profile"`
	BuildContext string `json:"build_context"`
	ReleaseImage string `json:"release_image"`
}

func main() {
	root := flag.String("root", ".", "repository root")
	version := flag.String("version", "", "release version without the leading v")
	flag.Parse()
	if err := run(*root, *version, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(root, version string, stdout *os.File) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve repository root: %w", err)
	}
	builds, err := catalog.DiscoverMCPReleaseBuilds(filepath.Join(root, "packages"))
	if err != nil {
		return err
	}
	out := make([]outputBuild, 0, len(builds))
	for _, build := range builds {
		context := filepath.Clean(filepath.Join("packages", build.PackageID, build.BuildContext))
		image := build.ReleaseImage
		if version != "" {
			image = strings.ReplaceAll(image, "{aact_version}", version)
		}
		out = append(out, outputBuild{PackageID: build.PackageID, Profile: build.Profile, BuildContext: filepath.ToSlash(context), ReleaseImage: image})
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(out)
}
