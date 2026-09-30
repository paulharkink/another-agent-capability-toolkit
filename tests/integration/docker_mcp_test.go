//go:build integration

package integration

import (
	"context"
	"fmt"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/mcp"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

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
	for _, i := range all {
		if i.Key == k {
			t.Fatal("stopped container remains")
		}
	}
}
