//go:build docker_integration

package sessionsearch

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSearchSyntheticHistoryInActualContainer(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".codex", "sessions", "2026", "09", "30")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "rollout-2026-09-30T12-00-00-fx1.jsonl")
	content := `{"type":"session_meta","payload":{"id":"fx1","cwd":"/fixture/synthetic-project","git":{"branch":"feature/fixture"},"timestamp":"2026-09-30T12:00:00Z"}}
{"type":"response_item","payload":{"role":"user","content":[{"type":"input_text","text":"We discussed postgres fixture evidence"}]}}
`
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	packageDir, err := filepath.Abs(filepath.Join("..", "..", "packages", "find-session"))
	if err != nil {
		t.Fatal(err)
	}
	packageDir = packageWithPrefetchedPythonBase(t, packageDir)
	var stdout, stderr bytes.Buffer
	s := Search{PackageDir: packageDir, Stdout: &stdout, Stderr: &stderr}
	if err = s.Run(context.Background(), []string{"-g", "postgres"}, home, false); err != nil {
		t.Fatalf("%v\n%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "postgres") || !strings.Contains(stdout.String(), "fx1") {
		t.Fatalf("search result missing: %s", stdout.String())
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != content {
		t.Fatal("history changed")
	}
}

func packageWithPrefetchedPythonBase(t *testing.T, source string) string {
	t.Helper()
	const ecr = "public.ecr.aws/docker/library/python:3.12-slim@sha256:a6e34c598f2467ed0e9a8d349809fcd8b5c603269512df273a0bb1784edc11b1"
	const hub = "docker.io/library/python:3.12-slim@sha256:a6e34c598f2467ed0e9a8d349809fcd8b5c603269512df273a0bb1784edc11b1"
	selected := os.Getenv("AACT_PYTHON_BASE_IMAGE_3_12_SLIM")
	if selected == "" {
		selected = ecr
	}
	if selected != ecr && selected != hub {
		t.Fatalf("refusing unverified Find Session test base %q", selected)
	}
	packageDir := t.TempDir()
	if err := os.CopyFS(packageDir, os.DirFS(source)); err != nil {
		t.Fatal(err)
	}
	dockerfile := filepath.Join(packageDir, "container", "Dockerfile")
	data, err := os.ReadFile(dockerfile)
	if err != nil {
		t.Fatal(err)
	}
	defaultLine := "ARG PYTHON_BASE_IMAGE=" + ecr
	if !strings.Contains(string(data), defaultLine) {
		t.Fatal("Find Session Dockerfile lost its pinned ECR default")
	}
	if err := os.WriteFile(dockerfile, []byte(strings.Replace(string(data), defaultLine, "ARG PYTHON_BASE_IMAGE="+selected, 1)), 0600); err != nil {
		t.Fatal(err)
	}
	return packageDir
}
