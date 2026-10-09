package integration_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
)

func TestReleaseWorkflowDiscoversOnlyBundledMCPBuildContexts(t *testing.T) {
	root := filepath.Join("..", "..")
	workflow, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(workflow)
	for _, required := range []string{"for dockerfile in packages/*/mcp/Dockerfile", `context="$(dirname "$dockerfile")"`, `--push "$context"`, "--provenance=true", "--sbom=true", "packages: write", "visibility", "--skip=publish"} {
		if !strings.Contains(text, required) {
			t.Errorf("release workflow missing package-context/release contract %q", required)
		}
	}
	if strings.Contains(text, "docker buildx build .") || strings.Contains(text, "docker buildx build ..") || strings.Contains(text, "${package}-mcp:latest") {
		t.Fatal("release workflow uses a repository-wide context or mutable latest tag")
	}
	if strings.Contains(text, "args: release --clean") || !strings.Contains(text, "args: publish") {
		t.Fatal("release workflow must publish the prebuilt GoReleaser artifacts without rebuilding after image publication")
	}
	if !strings.Contains(text, "Could not inspect GHCR visibility") || !strings.Contains(text, "Set the package visibility to Public") {
		t.Fatal("release workflow must explain how to recover when GHCR visibility cannot be verified")
	}
	dockerfiles, err := filepath.Glob(filepath.Join(root, "packages", "*", "mcp", "Dockerfile"))
	if err != nil || len(dockerfiles) == 0 {
		t.Fatalf("expected bundled MCP build contexts, got %v (%v)", dockerfiles, err)
	}
	for _, dockerfile := range dockerfiles {
		packageDir := filepath.Dir(filepath.Dir(dockerfile))
		pkg, err := catalog.Load(packageDir)
		if err != nil {
			t.Fatal(err)
		}
		if pkg.MCP == nil || filepath.Clean(filepath.Join(packageDir, pkg.MCP.BuildContext)) != filepath.Dir(dockerfile) {
			t.Errorf("%s does not declare its own exact MCP build context", pkg.ID)
		}
		if !strings.Contains(pkg.MCP.ReleaseImage, ":v{aact_version}") {
			t.Errorf("%s release image is not version pinned: %q", pkg.ID, pkg.MCP.ReleaseImage)
		}
		if !strings.Contains(text, "${package}-mcp:v${version}") || !strings.Contains(text, "org.opencontainers.image.source") {
			t.Errorf("workflow does not version and label the image for %s", pkg.ID)
		}
	}
	if strings.Index(text, "args: release --skip=publish --clean") > strings.Index(text, "Build package-only MCP contexts") || strings.Index(text, "Require every GHCR package to be public") > strings.LastIndex(text, "args: publish") {
		t.Fatal("release publication is not gated by artifact preflight, image pushes, and public visibility")
	}
}

func TestTagReleaseDispatchesReleaseWorkflowOnCreatedTag(t *testing.T) {
	root := filepath.Join("..", "..")
	releaseWorkflow, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	tagWorkflow, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "tag-release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	releaseText := string(releaseWorkflow)
	tagText := string(tagWorkflow)
	if !strings.Contains(releaseText, "workflow_dispatch:") {
		t.Fatal("release workflow must accept workflow_dispatch so the tag workflow can start it with GITHUB_TOKEN")
	}
	if !strings.Contains(releaseText, "tags: ['v*']") {
		t.Fatal("release workflow must continue to run automatically for version tags")
	}
	if !strings.Contains(releaseText, "if: github.ref_type == 'tag' && startsWith(github.ref_name, 'v')") {
		t.Fatal("release workflow must only build and publish artifacts when dispatched for a version tag")
	}
	if !strings.Contains(tagText, `gh workflow run release.yml --repo paulharkink/another-agent-capability-toolkit --ref "$tag"`) {
		t.Fatal("tag workflow must dispatch the release workflow on the exact tag it just created")
	}
}

func TestReleaseWorkflowAuthenticatesDockerHubBeforeSettingUpQEMU(t *testing.T) {
	workflow, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(workflow)
	preflight := strings.Index(text, `name: Require Docker Hub credentials`)
	login := strings.Index(text, `name: Login to Docker Hub`)
	qemu := strings.Index(text, "uses: docker/setup-qemu-action@v3")
	if preflight < 0 || login < 0 || qemu < 0 || preflight >= login || login >= qemu {
		t.Fatal("release workflow must check Docker Hub credentials, log in, then set up QEMU")
	}
	if !strings.Contains(text[preflight:login], "DOCKERHUB_USERNAME: ${{ secrets.DOCKERHUB_USERNAME }}") ||
		!strings.Contains(text[preflight:login], "DOCKERHUB_TOKEN: ${{ secrets.DOCKERHUB_TOKEN }}") ||
		!strings.Contains(text[preflight:login], "Set DOCKERHUB_USERNAME and DOCKERHUB_TOKEN repository Actions secrets") {
		t.Fatal("release workflow must fail clearly when Docker Hub repository secrets are missing")
	}
	if !strings.Contains(text[login:qemu], "uses: docker/login-action@v3") ||
		!strings.Contains(text[login:qemu], "username: ${{ secrets.DOCKERHUB_USERNAME }}") ||
		!strings.Contains(text[login:qemu], "password: ${{ secrets.DOCKERHUB_TOKEN }}") {
		t.Fatal("Docker Hub login must use the DOCKERHUB_USERNAME and DOCKERHUB_TOKEN repository secrets")
	}
}

func TestCIWorkflowDoesNotDuplicateFeatureBranchPullRequestRuns(t *testing.T) {
	workflow, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(workflow)
	pushStart := strings.Index(text, "  push:")
	pullRequestStart := strings.Index(text, "  pull_request:")
	if pushStart < 0 || pullRequestStart < 0 || pushStart >= pullRequestStart {
		t.Fatal("CI workflow must validate pull requests and main pushes")
	}
	pushTriggers := text[pushStart:pullRequestStart]
	if !strings.Contains(pushTriggers, "branches: [main]") || strings.Contains(pushTriggers, "feature/**") {
		t.Fatal("CI workflow must validate main pushes without duplicating pull_request runs on feature branches")
	}
}
