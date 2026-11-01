package integration_test

import (
	"os"
	"strings"
	"testing"
)

func TestFindSessionReleaseImageContract(t *testing.T) {
	workflow, err := os.ReadFile("../../.github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	workflowText := string(workflow)
	for _, test := range []struct {
		job      string
		nextJob  string
		arch     string
		artifact string
	}{
		{job: "build-mcp-amd64", nextJob: "build-mcp-arm64", arch: "amd64", artifact: "image-digests/amd64/find-session.txt"},
		{job: "build-mcp-arm64", nextJob: "publish-mcp-manifests", arch: "arm64", artifact: "image-digests/arm64/find-session.txt"},
	} {
		start := strings.Index(workflowText, "  "+test.job+":")
		if start < 0 {
			t.Fatalf("release workflow missing job %q", test.job)
		}
		end := strings.Index(workflowText[start+1:], "\n  "+test.nextJob+":")
		if end < 0 {
			end = len(workflowText)
		} else {
			end += start + 1
		}
		job := workflowText[start:end]
		for _, want := range []string{
			"packages/find-session/container/Dockerfile",
			"ghcr.io/${GITHUB_REPOSITORY,,}/aact-find-session",
			"linux/" + test.arch,
			test.artifact,
			`--output "type=image,name=${staging},push=true"`,
		} {
			if !strings.Contains(job, want) {
				t.Errorf("release workflow job %q missing find-session publication detail %q", test.job, want)
			}
		}
	}
	if !strings.Contains(workflowText, "mcp-image-digests-${{ needs.prepare-release.outputs.tag }}") ||
		!strings.Contains(workflowText, "AACT_IMAGE_OWNER: ${{ github.repository_owner }}") {
		t.Fatal("release workflow must record find-session image digests with the MCP release artifact and inject its repository owner into release binaries")
	}

	verifier, err := os.ReadFile("../../tools/verify-mcp-release-images.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"ghcr.io/${repository_lower}/${repository_name}/aact-find-session:${release_tag}",
		"${repository_name}%2Faact-find-session",
		"aact-find-session",
		"linux/amd64",
		"linux/arm64",
	} {
		if !strings.Contains(string(verifier), required) {
			t.Errorf("assets-only recovery must verify the existing find-session image contract; missing %q", required)
		}
	}
	archiveWorkflow, err := os.ReadFile("../../.github/workflows/artifacts.yml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(archiveWorkflow), "AACT_IMAGE_OWNER: ${{ github.repository_owner }}") {
		t.Fatal("development archive builds must inject the repository owner for find-session image metadata")
	}

	goreleaser, err := os.ReadFile("../../.goreleaser.yaml")
	if err != nil {
		t.Fatal(err)
	}
	config := strings.ReplaceAll(string(goreleaser), "\r\n", "\n")
	for _, buildID := range []string{"find-session", "find-session-helper"} {
		start := strings.Index(config, "- id: "+buildID+"\n")
		if start < 0 {
			t.Fatalf("GoReleaser config missing build %q", buildID)
		}
		end := strings.Index(config[start+1:], "\n  - id: ")
		if end < 0 {
			end = len(config)
		} else {
			end += start + 1
		}
		block := config[start:end]
		for _, required := range []string{"internal/versioninfo.ImageVersion={{ if .IsSnapshot }}{{ .Env.AACT_RELEASE_IMAGE_VERSION }}{{ else }}{{ .Version }}{{ end }}", "internal/versioninfo.ImageOwner={{ if .Env.AACT_IMAGE_OWNER }}{{ .Env.AACT_IMAGE_OWNER }}{{ else }}paulharkink{{ end }}"} {
			if !strings.Contains(block, required) {
				t.Errorf("GoReleaser build %q is missing release image setting %q", buildID, required)
			}
		}
	}
}
