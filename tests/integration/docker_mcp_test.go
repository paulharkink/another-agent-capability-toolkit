//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/mcp"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestConditionalCallbackPortAndMCPReadinessAreIndependent(t *testing.T) {
	pkg, err := catalog.Load(filepath.Join("..", "..", "packages", "argocd"))
	if err != nil {
		t.Fatal(err)
	}
	if pkg.MCP == nil || len(pkg.MCP.AdditionalPorts) != 1 {
		t.Fatalf("expected one declared SSO callback mapping, got %+v", pkg.MCP)
	}
	callback := pkg.MCP.AdditionalPorts[0]
	if callback.HostPortInput != "callback_port" || callback.ContainerPortInput != "callback_port" || callback.When["auth_mode"] != "sso" {
		t.Fatalf("callback mapping is not restricted to the requested SSO callback port: %+v", callback)
	}

	var authenticated atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/mcp":
			if r.Method != http.MethodPost {
				http.Error(w, "expected MCP initialize", http.StatusMethodNotAllowed)
				return
			}
			_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05"}}`)
		case "/health":
			_ = json.NewEncoder(w).Encode(map[string]bool{"authenticated": authenticated.Load()})
		case "/oauth/callback":
			authenticated.Store(true)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	if err := mcp.Health(context.Background(), server.URL+"/mcp", "streamable-http"); err != nil {
		t.Fatalf("MCP readiness should succeed before authentication: %v", err)
	}
	checkAuthentication := func(want bool) {
		t.Helper()
		response, err := http.Get(server.URL + "/health")
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var got struct {
			Authenticated bool `json:"authenticated"`
		}
		if err := json.NewDecoder(response.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		if got.Authenticated != want {
			t.Fatalf("authenticated = %v, want %v", got.Authenticated, want)
		}
	}
	checkAuthentication(false)
	callbackResponse, err := http.Get(server.URL + "/oauth/callback")
	if err != nil {
		t.Fatal(err)
	}
	callbackResponse.Body.Close()
	if callbackResponse.StatusCode != http.StatusNoContent {
		t.Fatalf("synthetic callback status = %d", callbackResponse.StatusCode)
	}
	checkAuthentication(true)
}

func TestDockerMCP(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	s, e := state.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	r := mcp.NewDockerRuntime(s)
	k := state.Key{Source: fmt.Sprintf("integration-%d", time.Now().UnixNano()), Package: "fixture", Environment: "synthetic", Target: "unit"}
	t.Cleanup(func() {
		cleanup, c := context.WithTimeout(context.Background(), 10*time.Second)
		defer c()
		_ = r.Stop(cleanup, k)
	})
	mount := t.TempDir()
	if e = os.WriteFile(filepath.Join(mount, "profile.txt"), []byte("aact-fixture"), 0600); e != nil {
		t.Fatal(e)
	}
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	build, e := filepath.Abs("../../testdata/mcp-fixture")
	if e != nil {
		t.Fatal(e)
	}
	build = fixtureWithPrefetchedPythonBase(t, build)
	instance, e := r.Start(ctx, k, mcp.RunSpec{BuildContext: build, Host: "127.0.0.1", HostPort: port, ContainerPort: 8765, Transport: "streamable-http", EndpointPath: "/mcp", Mounts: []mcp.Mount{{Source: mount, Destination: "/fixture", ReadOnly: true}}})
	if e != nil {
		t.Fatal(e)
	}
	if instance.Status != "running" {
		t.Fatal(instance)
	}
	if e = mcp.Health(ctx, instance.URL, "streamable-http"); e != nil {
		t.Fatal(e)
	}
	sse := fmt.Sprintf("http://127.0.0.1:%d/sse", port)
	if e = mcp.Health(ctx, sse, "sse"); e != nil {
		t.Fatal(e)
	}
	all, e := r.List(ctx)
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, i := range all {
		if i.Key == k {
			found = true
		}
	}
	if !found {
		t.Fatal("started container missing from inventory")
	}
	if e = r.Stop(ctx, k); e != nil {
		t.Fatal(e)
	}
	all, e = r.List(ctx)
	if e != nil {
		t.Fatal(e)
	}
	stoppedRuntimePresent := false
	for _, i := range all {
		if i.Key == k || i.ID == instance.ID {
			stoppedRuntimePresent = true
		}
	}
	if stoppedRuntimePresent {
		t.Fatalf("successful Stop left the stopped runtime in fresh inventory: %+v", all)
	}

	replacement, e := r.Start(ctx, k, mcp.RunSpec{BuildContext: build, Host: "127.0.0.1", HostPort: port, ContainerPort: 8765, Transport: "streamable-http", EndpointPath: "/mcp", Mounts: []mcp.Mount{{Source: mount, Destination: "/fixture", ReadOnly: true}}})
	if e != nil {
		t.Fatal(e)
	}
	if replacement.ID == instance.ID || replacement.Status != "running" {
		t.Fatalf("replacement runtime is not a new running container: old=%+v new=%+v", instance, replacement)
	}
	all, e = r.List(ctx)
	if e != nil {
		t.Fatal(e)
	}
	matching := 0
	for _, i := range all {
		if i.Key == k {
			matching++
			if i.ID != replacement.ID || i.Status != "running" {
				t.Fatalf("fresh inventory contains stale or unexpected runtime after restart: %+v", i)
			}
		}
	}
	if matching != 1 {
		t.Fatalf("fresh inventory has %d runtimes after restart; want exactly one: %+v", matching, all)
	}
}

func fixtureWithPrefetchedPythonBase(t *testing.T, source string) string {
	t.Helper()
	const ecr = "public.ecr.aws/docker/library/python:3.14-alpine@sha256:f6a589d43c42b9e7f7dc67a12d37132491f362859a5d750607710cc56da3bc72"
	const hub = "docker.io/library/python:3.14-alpine@sha256:f6a589d43c42b9e7f7dc67a12d37132491f362859a5d750607710cc56da3bc72"
	selected := os.Getenv("AACT_PYTHON_BASE_IMAGE_3_14_ALPINE")
	if selected == "" {
		selected = ecr
	}
	if selected != ecr && selected != hub {
		t.Fatalf("refusing unverified integration fixture Python base %q", selected)
	}
	data, err := os.ReadFile(filepath.Join(source, "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	defaultLine := "ARG PYTHON_BASE_IMAGE=" + ecr
	if !strings.Contains(string(data), defaultLine) {
		t.Fatalf("integration fixture Dockerfile lost its pinned ECR default")
	}
	context := t.TempDir()
	if err := os.WriteFile(filepath.Join(context, "Dockerfile"), []byte(strings.Replace(string(data), defaultLine, "ARG PYTHON_BASE_IMAGE="+selected, 1)), 0600); err != nil {
		t.Fatal(err)
	}
	server, err := os.ReadFile(filepath.Join(source, "server.py"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(context, "server.py"), server, 0600); err != nil {
		t.Fatal(err)
	}
	return context
}
