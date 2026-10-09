package versioninfo

import (
	"fmt"
	"regexp"
	"strings"
)

// Version is set by GoReleaser for release binaries. Development builds must
// not guess a mutable registry tag when the user requests a release image.
var Version = "dev"

var releaseVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?$`)

func ResolveImage(reference string) (string, error) {
	if !strings.Contains(reference, "{aact_version}") {
		return reference, nil
	}
	if strings.Count(reference, "{aact_version}") != 1 || !releaseVersion.MatchString(Version) {
		return "", fmt.Errorf("cannot resolve release image %q: AACT version %q is not a release version", reference, Version)
	}
	return strings.Replace(reference, "{aact_version}", Version, 1), nil
}
