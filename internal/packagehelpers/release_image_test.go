package packagehelpers

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestBundledPrepareUsesDeclaredReleaseImageWithoutBuild(t *testing.T) {
	q := helperRequest(t, "cluster-inspector", map[string]any{"cluster": map[string]any{"api_server": "https://cluster.example.test"}})
	q.ImageSource = "release"
	q.ReleaseImage = "ghcr.io/paulharkink/another-agent-capability-toolkit/cluster-inspector-mcp:v0.3.0"
	f := &helperProcess{AuthJSON: `{"auth_required":false}`}
	result, err := (&Helper{Executor: f}).Run(context.Background(), "cluster-inspector", q)
	if err != nil {
		t.Fatal(err)
	}
	if result.Runtime == nil || result.Runtime.Image != q.ReleaseImage {
		t.Fatalf("prepare did not use declared release image: %+v", result.Runtime)
	}
	for _, call := range f.Calls {
		if len(call) > 1 && call[1] == "build" {
			t.Fatalf("release mode built an image: %v", f.Calls)
		}
	}
	if !strings.Contains(strings.Join(f.Calls[len(f.Calls)-1], " "), q.ReleaseImage) {
		t.Fatalf("docker run omitted release image: %v", f.Calls)
	}
}

func TestBundledPrepareLocalSourceStillBuilds(t *testing.T) {
	q := helperRequest(t, "cluster-inspector", map[string]any{"cluster": map[string]any{"api_server": "https://cluster.example.test"}})
	q.ImageSource = "local"
	q.ReleaseImage = "ghcr.io/unused:never"
	f := &helperProcess{AuthJSON: `{"auth_required":false}`}
	result, err := (&Helper{Executor: f}).Run(context.Background(), "cluster-inspector", q)
	if err != nil {
		t.Fatal(err)
	}
	if result.Runtime == nil || !strings.HasPrefix(result.Runtime.Image, "sha256:") {
		t.Fatalf("local mode did not use built image digest: %+v", result.Runtime)
	}
	built := false
	for _, call := range f.Calls {
		if len(call) > 1 && call[1] == "build" || len(call) > 2 && call[1] == "buildx" && call[2] == "build" {
			built = true
		}
	}
	if !built {
		t.Fatalf("local mode skipped build: %v", f.Calls)
	}
}

func TestBundledPrepareLocalSourceFallsBackWhenBuildxUnavailable(t *testing.T) {
	q := helperRequest(t, "cluster-inspector", map[string]any{"cluster": map[string]any{"api_server": "https://cluster.example.test"}})
	q.ImageSource = "local"
	f := &helperProcess{AuthJSON: `{"auth_required":false}`, BuildxError: errors.New("buildx unavailable")}
	if _, err := (&Helper{Executor: f}).Run(context.Background(), "cluster-inspector", q); err != nil {
		t.Fatal(err)
	}
	var build []string
	for _, call := range f.Calls {
		if len(call) > 1 && call[1] == "build" {
			build = call
		}
	}
	if len(build) == 0 || strings.Contains(strings.Join(build, " "), "--progress") {
		t.Fatalf("legacy fallback should build without BuildKit-only flags: %v", f.Calls)
	}
}
