package catalog

import (
	"strings"
	"testing"
)

func TestBundledMCPReleaseImagesArePinnedToAACTVersionTemplate(t *testing.T) {
	for _, id := range []string{"cluster-inspector", "grafana-inspector", "azure-inspector"} {
		pkg, err := Load("../../packages/" + id)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(pkg.MCP.ReleaseImage, ":v{aact_version}") {
			t.Errorf("%s release image is not pinned to AACT version: %q", id, pkg.MCP.ReleaseImage)
		}
	}
}
