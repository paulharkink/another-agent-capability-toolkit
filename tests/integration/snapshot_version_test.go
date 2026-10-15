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
	if err := os.WriteFile(fakeGoReleaser, []byte("#!/bin/sh\nprintf '%s\\n' \"$AACT_SNAPSHOT_BRANCH\" \"$@\" > \"$CAPTURE\"\n"), 0700); err != nil {
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
	if string(got) != "feature-aact-test-demo\nrelease\n--snapshot\n--clean\n--skip=publish\n" {
		t.Fatalf("snapshot invocation = %q", got)
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
	if err := os.WriteFile(fakeGoReleaser, []byte("#!/bin/sh\nprintf '%s\\n' \"$AACT_SNAPSHOT_BRANCH\" > \"$CAPTURE\"\n"), 0700); err != nil {
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
	if got, err := os.ReadFile(capture); err != nil || string(got) != "feature-explicit-branch\n" {
		t.Fatalf("snapshot branch = %q, %v", got, err)
	}
}

func TestGoReleaserKeepsSnapshotAppVersionSeparateFromImagePin(t *testing.T) {
	config, err := os.ReadFile("../../.goreleaser.yaml")
	if err != nil {
		t.Fatal(err)
	}
	text := string(config)
	for _, want := range []string{
		"version_template: '{{ incpatch .Version }}-SNAPSHOT-{{ .Env.AACT_SNAPSHOT_BRANCH }}'",
		"internal/cli.Version={{ .Version }}",
		"internal/versioninfo.Version={{ if .IsSnapshot }}dev{{ else }}{{ .Version }}{{ end }}",
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
		"./scripts/snapshot-release.sh --print-branch",
		"AACT_SNAPSHOT_BRANCH: ${{ steps.snapshot-branch.outputs.slug }}",
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
	return repo
}
