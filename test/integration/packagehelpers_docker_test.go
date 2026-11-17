//go:build docker_integration

package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/mcp"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/packagehelpers"
)

func TestPrepareMissingAuthInActualContainers(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  map[string]any
	}{
		{"cluster-inspector", map[string]any{"cluster": map[string]any{"api_server": "https://unreachable.example.test"}}},
		{"grafana-inspector", map[string]any{"grafana": map[string]any{"url": "https://unreachable.example.test", "auth_mode": "api_token"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := dockerHelperRequest(t, tc.name, tc.raw)
			q.PackageDir = packagehelpersWithPrefetchedPythonBase(t, q.PackageDir)
			result, err := (&packagehelpers.Helper{}).Run(context.Background(), tc.name, q)
			if err != nil {
				t.Fatal(err)
			}
			if !result.AuthRequired || result.Runtime != nil {
				t.Fatal(result)
			}
		})
	}
}

func dockerHelperRequest(t *testing.T, name string, raw map[string]any) mcp.ActionRequest {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "packages", name))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	target := filepath.Join(root, "target.toml")
	if err := os.WriteFile(target, []byte("# source remains untouched\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return mcp.ActionRequest{ProtocolVersion: 1, Action: "prepare", PackageDir: dir, StateDir: filepath.Join(root, "state"), Target: config.Target{Environment: "lab", Name: "fixture", Path: target, Raw: raw}, Inputs: map[string]any{}}
}

func packagehelpersWithPrefetchedPythonBase(t *testing.T, source string) string {
	t.Helper()
	const ecr = "public.ecr.aws/docker/library/python:3.13-slim@sha256:70729b46c69b4f1e97c4822c1af3df53a1476cf5ddc6c087c0c10bc3a5678c2f"
	const hub = "docker.io/library/python:3.13-slim@sha256:70729b46c69b4f1e97c4822c1af3df53a1476cf5ddc6c087c0c10bc3a5678c2f"
	selected := os.Getenv("AACT_PYTHON_BASE_IMAGE_3_13_SLIM")
	if selected == "" {
		selected = ecr
	}
	if selected != ecr && selected != hub {
		t.Fatalf("refusing unverified inspector test base %q", selected)
	}
	packageDir := t.TempDir()
	if err := os.CopyFS(packageDir, os.DirFS(source)); err != nil {
		t.Fatal(err)
	}
	dockerfile := filepath.Join(packageDir, "mcp", "Dockerfile")
	data, err := os.ReadFile(dockerfile)
	if err != nil {
		t.Fatal(err)
	}
	defaultLine := "ARG PYTHON_BASE_IMAGE=" + ecr
	if !strings.Contains(string(data), defaultLine) {
		t.Fatal("inspector Dockerfile lost its pinned ECR default")
	}
	if err := os.WriteFile(dockerfile, []byte(strings.Replace(string(data), defaultLine, "ARG PYTHON_BASE_IMAGE="+selected, 1)), 0600); err != nil {
		t.Fatal(err)
	}
	return packageDir
}
