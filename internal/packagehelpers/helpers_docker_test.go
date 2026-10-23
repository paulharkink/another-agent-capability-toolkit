//go:build docker_integration

package packagehelpers

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
			q := helperRequest(t, tc.name, tc.raw)
			q.PackageDir = packageWithPrefetchedPythonBase(t, q.PackageDir)
			result, err := (&Helper{}).Run(context.Background(), tc.name, q)
			if err != nil {
				t.Fatal(err)
			}
			if !result.AuthRequired || result.Runtime != nil {
				t.Fatal(result)
			}
		})
	}
}

func packageWithPrefetchedPythonBase(t *testing.T, source string) string {
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
