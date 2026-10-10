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
	manifestPublisher, err := os.ReadFile(filepath.Join(root, "tools", "publish-mcp-manifests.sh"))
	if err != nil {
		t.Fatal(err)
	}
	releasePipeline := text + "\n" + string(manifestPublisher)
	for _, required := range []string{"for dockerfile in packages/*/mcp/Dockerfile", `context="$(dirname "$dockerfile")"`, "type=image,name=", "--provenance=true", "--sbom=true", "packages: write", "visibility", "--skip=publish"} {
		if !strings.Contains(text, required) {
			t.Errorf("release workflow missing package-context/release contract %q", required)
		}
	}
	if strings.Contains(text, "docker buildx build .") || strings.Contains(text, "docker buildx build ..") || strings.Contains(text, "${package}-mcp:latest") {
		t.Fatal("release workflow uses a repository-wide context or mutable latest tag")
	}
	if !strings.Contains(text, "args: release --skip=publish --clean") || !strings.Contains(text, "args: release --clean") {
		t.Fatal("release workflow must preflight GoReleaser before image publication and run a full supported release afterward")
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
		if !strings.Contains(releasePipeline, "${package}-mcp") || !strings.Contains(releasePipeline, "${image}:v${version}") || !strings.Contains(releasePipeline, "org.opencontainers.image.source") {
			t.Errorf("workflow does not version and label the image for %s", pkg.ID)
		}
	}
	if !strings.Contains(text, "needs: prepare-release") || !strings.Contains(text, "needs: [prepare-release, build-mcp-amd64, build-mcp-arm64]") ||
		!strings.Contains(text, "needs: [prepare-release, publish-mcp-manifests, verify-existing-mcp-images]") ||
		strings.Index(text, "Require every GHCR package to be public") > strings.Index(text, "tools/publish-mcp-manifests.sh") ||
		strings.Index(text, "publish-release:") < strings.Index(text, "tools/publish-mcp-manifests.sh") {
		t.Fatal("version tags and GoReleaser publication must wait for both builds and the GHCR public visibility gate")
	}
}

func TestReleaseWorkflowUsesSupportedGoReleaserPublishPhase(t *testing.T) {
	workflow, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(workflow)
	publishStart := strings.Index(text, "  publish-release:")
	if publishStart < 0 {
		t.Fatal("release workflow must define its GoReleaser publication job")
	}
	publishJob := text[publishStart:]
	if !strings.Contains(publishJob, "args: release --clean") {
		t.Fatal("GoReleaser OSS must run its supported release command to build and publish release assets")
	}
	if strings.Contains(publishJob, "args: publish") {
		t.Fatal("GoReleaser OSS has no standalone publish command")
	}
	if strings.Contains(publishJob, "actions/download-artifact@v4") {
		t.Fatal("publish job must not download prebuilt GoReleaser artifacts that the OSS CLI cannot publish separately")
	}
	if !strings.Contains(publishJob, "always() && needs.prepare-release.result == 'success'") ||
		!strings.Contains(publishJob, "needs.publish-mcp-manifests.result == 'success'") ||
		!strings.Contains(publishJob, "needs.verify-existing-mcp-images.result == 'success'") {
		t.Fatal("GoReleaser must publish after preflight and either normal image publication or assets-only image verification succeeds")
	}
}

func TestManualReleaseDispatchUsesMainWorkflowAndSelectedTagSource(t *testing.T) {
	root := filepath.Join("..", "..")
	workflow, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	publisher, err := os.ReadFile(filepath.Join(root, "tools", "publish-mcp-manifests.sh"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(workflow)
	for _, required := range []string{
		"release_tag:",
		"github.event_name == 'workflow_dispatch'",
		"github.ref_name == 'main'",
		"inputs.release_tag",
		"tag: ${{ steps.resolve-release.outputs.tag }}",
		"source_commit: ${{ steps.resolve-release.outputs.source_commit }}",
		"ref: ${{ needs.prepare-release.outputs.source_commit }}",
		"AACT_RELEASE_TAG: ${{ needs.prepare-release.outputs.tag }}",
		"org.opencontainers.image.revision=${{ needs.prepare-release.outputs.source_commit }}",
		"ref: ${{ github.workflow_sha }}",
		"git checkout --detach \"$source_commit\"",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("release workflow does not support dispatching the main workflow against a selected tag: missing %q", required)
		}
	}
	validateTag := strings.Index(text, `if [[ ! "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]`)
	fetchTag := strings.Index(text, `git fetch --force origin "refs/tags/${tag}:refs/tags/${tag}"`)
	if validateTag < 0 || fetchTag < 0 || validateTag >= fetchTag {
		t.Fatal("release_tag must be validated before it is used in the tag fetch")
	}
	if strings.Contains(text, "ref: ${{ inputs.release_tag || github.ref }}") {
		t.Fatal("unvalidated workflow_dispatch input must not be used as a checkout ref")
	}
	if !strings.Contains(string(publisher), `release_tag="${AACT_RELEASE_TAG:-$GITHUB_REF_NAME}"`) {
		t.Fatal("manifest publisher must use the explicitly selected release tag when workflow_dispatch runs from main")
	}
	manifestJob := text[strings.Index(text, "  publish-mcp-manifests:"):]
	if !strings.Contains(manifestJob, "needs: [prepare-release, build-mcp-amd64, build-mcp-arm64]") {
		t.Fatal("manifest job must directly depend on prepare-release to receive its tag and source commit outputs")
	}
}

func TestArm64BuildReceivesResolvedReleaseOutputs(t *testing.T) {
	workflow, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(workflow)
	start := strings.Index(text, "  build-mcp-arm64:")
	if start < 0 {
		t.Fatal("release workflow must define arm64 build job")
	}
	end := strings.Index(text[start:], "  publish-mcp-manifests:")
	if end < 0 {
		t.Fatal("release workflow must define arm64 build and manifest jobs")
	}
	arm64Job := text[start : start+end]
	for _, required := range []string{
		"needs: [prepare-release, build-mcp-amd64]",
		"needs.prepare-release.outputs.version",
		"needs.prepare-release.outputs.source_commit",
		"name: mcp-image-digests-arm64-${{ needs.prepare-release.outputs.tag }}",
	} {
		if !strings.Contains(arm64Job, required) {
			t.Errorf("arm64 build must receive and use resolved release outputs; missing %q", required)
		}
	}
}

func TestAssetsOnlyRecoveryValidatesExistingImagesAndSkipsImagePublishing(t *testing.T) {
	root := filepath.Join("..", "..")
	workflow, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := os.ReadFile(filepath.Join(root, "tools", "verify-mcp-release-images.sh"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(workflow)
	for _, required := range []string{
		"publish_assets_only:",
		"type: boolean",
		"default: false",
		"if: inputs.publish_assets_only != true",
		"verify-existing-mcp-images:",
		"if: inputs.publish_assets_only == true",
		"run: bash tools/verify-mcp-release-images.sh",
		"needs: [prepare-release, publish-mcp-manifests, verify-existing-mcp-images]",
		"always() && needs.prepare-release.result == 'success'",
		"needs.verify-existing-mcp-images.result == 'success'",
		"needs.publish-mcp-manifests.result == 'success'",
		"args: release --clean",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("assets-only recovery workflow is missing %q", required)
		}
	}
	verifyStart := strings.Index(text, "  verify-existing-mcp-images:")
	publishStart := strings.Index(text, "  publish-release:")
	if verifyStart < 0 || publishStart < 0 || verifyStart >= publishStart {
		t.Fatal("assets-only verification job must precede the release publication job")
	}
	if strings.Contains(text[verifyStart:publishStart], "imagetools create") || strings.Contains(text[verifyStart:publishStart], "actions/download-artifact@v4") {
		t.Fatal("assets-only verification job must not create or push image manifests")
	}
	buildStart := strings.Index(text, "  build-mcp-amd64:")
	armStart := strings.Index(text, "  build-mcp-arm64:")
	manifestStart := strings.Index(text, "  publish-mcp-manifests:")
	if buildStart < 0 || armStart < 0 || manifestStart < 0 ||
		!strings.HasPrefix(text[buildStart:], "  build-mcp-amd64:\n    if: inputs.publish_assets_only != true") ||
		!strings.HasPrefix(text[armStart:], "  build-mcp-arm64:\n    if: inputs.publish_assets_only != true") ||
		!strings.HasPrefix(text[manifestStart:], "  publish-mcp-manifests:\n    if: inputs.publish_assets_only != true") {
		t.Fatal("assets-only recovery must skip both image builds and the image manifest publisher")
	}
	verifyText := string(verifier)
	for _, required := range []string{
		"azure-inspector cluster-inspector grafana-inspector",
		"AACT_RELEASE_TAG",
		"gh api",
		"visibility",
		"public",
		"docker buildx imagetools inspect",
		"linux",
		"amd64",
		"arm64",
	} {
		if !strings.Contains(verifyText, required) {
			t.Errorf("assets-only verification helper is missing %q", required)
		}
	}
	if strings.Contains(verifyText, "imagetools create") || strings.Contains(verifyText, "--push") {
		t.Fatal("assets-only verification helper must only inspect images and visibility")
	}
	goreleaserConfig, err := os.ReadFile(filepath.Join(root, ".goreleaser.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(goreleaserConfig), "replace_existing_artifacts: true") {
		t.Fatal("GoReleaser must support replacing the incomplete v0.4.3 assets during a safe retry")
	}
}

func TestManifestPublisherPreservesExistingImagesAndRetriesRegistryInspection(t *testing.T) {
	root := filepath.Join("..", "..")
	workflow, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	publisher, err := os.ReadFile(filepath.Join(root, "tools", "publish-mcp-manifests.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(workflow), "tools/publish-mcp-manifests.sh") {
		t.Fatal("release workflow must use the tested MCP manifest publisher")
	}
	text := string(publisher)
	for _, required := range []string{
		"imagetools inspect",
		"imagetools create",
		"gh api --paginate",
		"refusing to assume",
		"Skipping existing version-pinned image",
		"existing image does not match the architecture digests from this run",
		"Failed to verify the published manifest",
		"for attempt in 1 2 3 4 5",
		"sleep",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("MCP manifest publisher must preserve existing tags and report/retry registry operations; missing %q", required)
		}
	}
	if strings.Index(text, "Skipping existing version-pinned image") > strings.Index(text, "imagetools create") {
		t.Fatal("publisher must verify and preserve an existing version tag before creating a tag")
	}
}

func TestBundledDockerfilesDoNotDependOnDockerHub(t *testing.T) {
	root := filepath.Join("..", "..")
	var dockerfiles []string
	err := filepath.WalkDir(filepath.Join(root, "packages"), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), "Dockerfile") {
			dockerfiles = append(dockerfiles, path)
		}
		return nil
	})
	if err != nil || len(dockerfiles) == 0 {
		t.Fatalf("expected package Dockerfiles, got %v (%v)", dockerfiles, err)
	}
	dockerfiles = append(dockerfiles, filepath.Join(root, "testdata", "mcp-fixture", "Dockerfile"))
	for _, path := range dockerfiles {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "FROM ") && (strings.Contains(line, "docker.io/") || strings.HasPrefix(line, "FROM python:")) {
				t.Errorf("%s has a Docker Hub base image: %s", path, line)
			}
		}
	}
	workflowDir := filepath.Join("..", "..", ".github", "workflows")
	workflowEntries, err := os.ReadDir(workflowDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range workflowEntries {
		if entry.IsDir() || !(strings.HasSuffix(entry.Name(), ".yml") || strings.HasSuffix(entry.Name(), ".yaml")) {
			continue
		}
		workflow, err := os.ReadFile(filepath.Join(workflowDir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		text := string(workflow)
		for _, forbidden := range []string{"DOCKERHUB_USERNAME", "DOCKERHUB_TOKEN", "docker.io/", "docker/setup-qemu-action", "docker/setup-buildx-action", "docker/build-push-action"} {
			if strings.Contains(text, forbidden) {
				t.Errorf("%s still depends on Docker Hub credentials, image refs, or container actions: %s", entry.Name(), forbidden)
			}
		}
	}
	workflow, err := os.ReadFile(filepath.Join(workflowDir, "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	publisher, err := os.ReadFile(filepath.Join(root, "tools", "publish-mcp-manifests.sh"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(workflow) + "\n" + string(publisher)
	if !strings.Contains(text, "runs-on: ubuntu-24.04-arm") || !strings.Contains(text, "runs-on: ubuntu-latest") ||
		!strings.Contains(text, "--platform linux/amd64") || !strings.Contains(text, "--platform linux/arm64") ||
		!strings.Contains(text, "docker buildx imagetools create") || !strings.Contains(text, "containerd-snapshotter") {
		t.Fatal("release workflow must build on native amd64 and arm64 runners, then assemble a multi-platform tag")
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
	if !strings.Contains(releaseText, "github.event_name == 'push' && github.ref_type == 'tag' && startsWith(github.ref_name, 'v')") {
		t.Fatal("release workflow must continue to build and publish automatically for pushed version tags")
	}
	if !strings.Contains(releaseText, "github.event_name == 'workflow_dispatch'") || !strings.Contains(releaseText, "github.ref_name == 'main'") {
		t.Fatal("manual release dispatch must use the current main workflow, not the old workflow embedded in a tag")
	}
	if !strings.Contains(tagText, `gh workflow run release.yml --repo paulharkink/another-agent-capability-toolkit --ref "$tag"`) {
		t.Fatal("tag workflow must dispatch the release workflow on the exact tag it just created")
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

func TestCIWorkflowDoesNotRequireDockerHubSecrets(t *testing.T) {
	workflow, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(workflow)
	dockerJob := strings.Index(text, "  docker:")
	if dockerJob < 0 {
		t.Fatal("CI workflow must define the Docker validation job")
	}
	dockerText := text[dockerJob:]
	trustCondition := "if: github.event_name != 'pull_request' || github.event.pull_request.head.repo.full_name == github.repository"
	if !strings.Contains(dockerText, trustCondition) {
		t.Fatal("Docker job must skip fork pull requests so their code cannot access repository secrets")
	}
	if strings.Contains(dockerText, "DOCKERHUB_USERNAME") || strings.Contains(dockerText, "DOCKERHUB_TOKEN") || strings.Contains(dockerText, "docker/login-action@v3") {
		t.Fatal("Docker CI must not require Docker Hub credentials")
	}
	if strings.Contains(dockerText, "docker.io/") || !strings.Contains(dockerText, "tools/prefetch-python-bases.sh") {
		t.Fatal("Docker CI must prefetch digest-pinned Python bases from ECR Public without Docker Hub")
	}
}
