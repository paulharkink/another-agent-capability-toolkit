package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSnapshotBuildUsesNormalizedBranchSemVer(t *testing.T) {
	repo := initSnapshotTestRepository(t, "Feature/AACT_Test+Demo")
	fakeBin := t.TempDir()
	capture := filepath.Join(t.TempDir(), "invocation.txt")
	fakeGoReleaser := filepath.Join(fakeBin, "goreleaser")
	if err := os.WriteFile(fakeGoReleaser, []byte("#!/bin/sh\nprintf '%s\\n' \"$AACT_SNAPSHOT_BRANCH\" \"$AACT_RELEASE_IMAGE_VERSION\" \"$@\" > \"$CAPTURE\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs("../../scripts/snapshot-release.sh")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", script, "--clean", "--skip=publish")
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "PATH="+fakeBin+":"+os.Getenv("PATH"), "CAPTURE="+capture)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("snapshot wrapper failed: %s: %v", output, err)
	}
	got, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "feature-aact-test-demo\n0.5.0\nrelease\n--snapshot\n--clean\n--skip=publish\n" {
		t.Fatalf("snapshot invocation = %q", got)
	}
}

func TestSnapshotBuildUsesLatestFetchedStableTagEvenWhenNotMerged(t *testing.T) {
	repo := initSnapshotTestRepository(t, "feature/test")
	// Move the stable release tag to a commit outside the current branch's ancestry,
	// as happens when a feature branch was created before the release was fetched.
	createRelease := exec.Command("git", "checkout", "--quiet", "--orphan", "release-tag-fixture")
	createRelease.Dir = repo
	if output, err := createRelease.CombinedOutput(); err != nil {
		t.Fatalf("create unrelated release commit: %s: %v", output, err)
	}
	if err := os.WriteFile(filepath.Join(repo, "release"), []byte("release\n"), 0600); err != nil {
		t.Fatal(err)
	}
	addRelease := exec.Command("git", "add", "release")
	addRelease.Dir = repo
	if output, err := addRelease.CombinedOutput(); err != nil {
		t.Fatalf("stage release fixture: %s: %v", output, err)
	}
	commitRelease := exec.Command("git", "commit", "--quiet", "-m", "release fixture")
	commitRelease.Dir = repo
	if output, err := commitRelease.CombinedOutput(); err != nil {
		t.Fatalf("commit release fixture: %s: %v", output, err)
	}
	moveReleaseTag := exec.Command("git", "tag", "-f", "v0.5.0")
	moveReleaseTag.Dir = repo
	if output, err := moveReleaseTag.CombinedOutput(); err != nil {
		t.Fatalf("move stable tag: %s: %v", output, err)
	}
	checkoutFeature := exec.Command("git", "checkout", "--quiet", "feature/test")
	checkoutFeature.Dir = repo
	if output, err := checkoutFeature.CombinedOutput(); err != nil {
		t.Fatalf("return to feature branch: %s: %v", output, err)
	}
	script, err := filepath.Abs("../../scripts/snapshot-release.sh")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", script, "--print-image-version")
	cmd.Dir = repo
	output, err := cmd.CombinedOutput()
	if err != nil || string(output) != "0.5.0\n" {
		t.Fatalf("printed image version from non-ancestor stable tag = %q, %v", output, err)
	}
}

func TestSnapshotBuildFetchesReleaseTagsFromOrigin(t *testing.T) {
	repo := initSnapshotTestRepository(t, "feature/test")
	deleteLocalTag := exec.Command("git", "tag", "--delete", "v0.5.0")
	deleteLocalTag.Dir = repo
	if output, err := deleteLocalTag.CombinedOutput(); err != nil {
		t.Fatalf("delete local release tag: %s: %v", output, err)
	}
	remote := t.TempDir()
	initRemote := exec.Command("git", "init", "--bare", "--quiet", remote)
	if output, err := initRemote.CombinedOutput(); err != nil {
		t.Fatalf("initialize bare origin: %s: %v", output, err)
	}
	seed := t.TempDir()
	runGit := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s: %v", args, output, err)
		}
	}
	runGit(seed, "init", "--quiet")
	runGit(seed, "config", "user.email", "snapshot-test@example.invalid")
	runGit(seed, "config", "user.name", "Snapshot Test")
	if err := os.WriteFile(filepath.Join(seed, "release"), []byte("release\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runGit(seed, "add", "release")
	runGit(seed, "commit", "--quiet", "-m", "release fixture")
	runGit(seed, "tag", "v0.5.0")
	runGit(seed, "remote", "add", "origin", remote)
	runGit(seed, "push", "--quiet", "origin", "refs/tags/v0.5.0")
	runGit(repo, "remote", "add", "origin", remote)
	script, err := filepath.Abs("../../scripts/snapshot-release.sh")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", script, "--print-image-version")
	cmd.Dir = repo
	output, err := cmd.CombinedOutput()
	if err != nil || string(output) != "0.5.0\n" {
		t.Fatalf("printed release image version after origin fetch = %q, %v", output, err)
	}
	if got, err := exec.Command("git", "-C", repo, "show-ref", "--verify", "--quiet", "refs/tags/v0.5.0").CombinedOutput(); err != nil {
		t.Fatalf("origin stable tag was not fetched: %s: %v", got, err)
	}
}

func TestSnapshotBuildRejectsDetachedHeadWithoutHashFallback(t *testing.T) {
	repo := initSnapshotTestRepository(t, "feature/test")
	detach := exec.Command("git", "checkout", "--quiet", "--detach")
	detach.Dir = repo
	if output, err := detach.CombinedOutput(); err != nil {
		t.Fatalf("detach test repository: %s: %v", output, err)
	}
	fakeBin := t.TempDir()
	capture := filepath.Join(t.TempDir(), "invocation.txt")
	fakeGoReleaser := filepath.Join(fakeBin, "goreleaser")
	if err := os.WriteFile(fakeGoReleaser, []byte("#!/bin/sh\ntouch \"$CAPTURE\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	script, pathErr := filepath.Abs("../../scripts/snapshot-release.sh")
	if pathErr != nil {
		t.Fatal(pathErr)
	}
	cmd := exec.Command("/bin/sh", script)
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "PATH="+fakeBin+":"+os.Getenv("PATH"), "CAPTURE="+capture)
	output, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "requires an attached branch") {
		t.Fatalf("detached snapshot was not rejected clearly: %q, %v", output, err)
	}
	if _, err := os.Stat(capture); !os.IsNotExist(err) {
		t.Fatalf("GoReleaser invoked without a branch: %v", err)
	}
}

func TestSnapshotBuildUsesExplicitBranchForDetachedBuild(t *testing.T) {
	repo := initSnapshotTestRepository(t, "feature/test")
	detach := exec.Command("git", "checkout", "--quiet", "--detach")
	detach.Dir = repo
	if output, err := detach.CombinedOutput(); err != nil {
		t.Fatalf("detach test repository: %s: %v", output, err)
	}
	fakeBin := t.TempDir()
	capture := filepath.Join(t.TempDir(), "invocation.txt")
	fakeGoReleaser := filepath.Join(fakeBin, "goreleaser")
	if err := os.WriteFile(fakeGoReleaser, []byte("#!/bin/sh\nprintf '%s\\n' \"$AACT_SNAPSHOT_BRANCH\" \"$AACT_RELEASE_IMAGE_VERSION\" > \"$CAPTURE\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs("../../scripts/snapshot-release.sh")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", script)
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "PATH="+fakeBin+":"+os.Getenv("PATH"), "CAPTURE="+capture, "AACT_SNAPSHOT_SOURCE_BRANCH=Feature/Explicit_Branch")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("explicit-branch snapshot failed: %s: %v", output, err)
	}
	if got, err := os.ReadFile(capture); err != nil || string(got) != "feature-explicit-branch\n0.5.0\n" {
		t.Fatalf("snapshot branch = %q, %v", got, err)
	}
}

func TestSnapshotBuildPrintsLatestStableReleaseImageVersion(t *testing.T) {
	repo := initSnapshotTestRepository(t, "feature/test")
	addTag := exec.Command("git", "tag", "v0.6.0-rc.1")
	addTag.Dir = repo
	if output, err := addTag.CombinedOutput(); err != nil {
		t.Fatalf("add prerelease tag: %s: %v", output, err)
	}
	script, err := filepath.Abs("../../scripts/snapshot-release.sh")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", script, "--print-image-version")
	cmd.Dir = repo
	output, err := cmd.CombinedOutput()
	if err != nil || string(output) != "0.5.0\n" {
		t.Fatalf("printed image version = %q, %v", output, err)
	}
}

func TestSnapshotBuildRequiresStableTagForReleaseImagePin(t *testing.T) {
	repo := initSnapshotTestRepository(t, "feature/test")
	removeTag := exec.Command("git", "tag", "--delete", "v0.5.0")
	removeTag.Dir = repo
	if output, err := removeTag.CombinedOutput(); err != nil {
		t.Fatalf("remove stable tag: %s: %v", output, err)
	}
	script, err := filepath.Abs("../../scripts/snapshot-release.sh")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", script, "--print-image-version")
	cmd.Dir = repo
	output, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "requires a stable vMAJOR.MINOR.PATCH tag") {
		t.Fatalf("missing stable tag was not rejected clearly: %q, %v", output, err)
	}
}

func TestGoReleaserKeepsSnapshotAppVersionSeparateFromPublishedImagePin(t *testing.T) {
	config, err := os.ReadFile("../../.goreleaser.yaml")
	if err != nil {
		t.Fatal(err)
	}
	text := string(config)
	for _, want := range []string{
		"version_template: '{{ incpatch .Env.AACT_RELEASE_IMAGE_VERSION }}-SNAPSHOT-{{ .Env.AACT_SNAPSHOT_BRANCH }}'",
		"internal/cli.Version={{ .Version }}",
		"internal/versioninfo.Version={{ .Version }}",
		"internal/versioninfo.ImageVersion={{ if .IsSnapshot }}{{ .Env.AACT_RELEASE_IMAGE_VERSION }}{{ else }}{{ .Version }}{{ end }}",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("GoReleaser config missing %q", want)
		}
	}
}

func TestSnapshotWorkflowFetchesTagsAndPassesNormalizedBranch(t *testing.T) {
	workflow, err := os.ReadFile("../../.github/workflows/artifacts.yml")
	if err != nil {
		t.Fatal(err)
	}
	text := string(workflow)
	for _, want := range []string{
		"fetch-depth: 0",
		"fetch-tags: true",
		"./scripts/snapshot-release.sh --print-branch",
		"AACT_SNAPSHOT_BRANCH: ${{ steps.snapshot-branch.outputs.slug }}",
		"./scripts/snapshot-release.sh --print-image-version",
		"AACT_RELEASE_IMAGE_VERSION: ${{ steps.snapshot-branch.outputs.image-version }}",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("snapshot workflow missing %q", want)
		}
	}
}

func initSnapshotTestRepository(t *testing.T, branch string) string {
	t.Helper()
	repo := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s: %v", args, output, err)
		}
	}
	run("init", "--quiet")
	run("config", "user.email", "snapshot-test@example.invalid")
	run("config", "user.name", "Snapshot Test")
	if err := os.WriteFile(filepath.Join(repo, "tracked"), []byte("fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	run("add", "tracked")
	run("commit", "--quiet", "-m", "fixture")
	run("branch", "-m", branch)
	run("tag", "v0.5.0")
	return repo
}
