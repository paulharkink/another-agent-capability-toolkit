package versioninfo

import (
	"fmt"
	"regexp"
	"strings"
)

// Version is the AACT application version, including a snapshot SemVer when
// this binary was built from a development archive.
var Version = "dev"

// ImageVersion is the published container image version. Snapshot builds pin
// release images to the latest stable tag instead of an unpublished snapshot.
var ImageVersion = "dev"

var releaseVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?$`)

func ResolveImage(reference string) (string, error) {
	if !strings.Contains(reference, "{aact_version}") {
		return reference, nil
	}
	if strings.Count(reference, "{aact_version}") != 1 || !releaseVersion.MatchString(ImageVersion) {
		return "", fmt.Errorf("cannot resolve release image %q: image version %q is not a release version", reference, ImageVersion)
	}
	return strings.Replace(reference, "{aact_version}", ImageVersion, 1), nil
}
