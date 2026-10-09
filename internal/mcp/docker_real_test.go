//go:build docker_integration

package mcp

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
)

func TestActualDockerRetriesReplacementAfterHealthFailure(t *testing.T) {
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := NewDockerRuntime(store)
	k := state.Key{Source: fmt.Sprintf("rollback-fixture-%d", time.Now().UnixNano()), Package: "fixture", Environment: "synthetic", Target: "test"}
	image := "aact/rollback-" + k.ID()[:16] + ":test"
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	t.Cleanup(func() {
		cleanup, c := context.WithTimeout(context.Background(), 10*time.Second)
		defer c()
		_ = r.Stop(cleanup, k)
		_, _ = r.run(cleanup, []string{"image", "rm", image})
	})
	build, err := filepath.Abs("../../testdata/mcp-fixture")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.run(ctx, []string{"build", "--quiet", "--tag", image, build}); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	mount := t.TempDir()
	if err := os.WriteFile(filepath.Join(mount, "profile.txt"), []byte("rollback-fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	good := RunSpec{Image: image, Host: "127.0.0.1", HostPort: port, ContainerPort: 8765, Transport: "streamable-http", EndpointPath: "/mcp", Mounts: []Mount{{Source: mount, Destination: "/fixture", ReadOnly: true}}}
	old, err := r.Start(ctx, k, good)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.run(ctx, []string{"stop", old.ID}); err != nil {
		t.Fatal(err)
	}
	bad := good
	bad.ContainerPort = 8766
	failureCtx, c := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	_, err = r.Start(failureCtx, k, bad)
	c()
	if err == nil {
		t.Fatal("bad endpoint accepted")
	}
	// Start returns the actual Docker container identity. The readable Docker
	// name may differ from the legacy key-derived name used by older runtimes.
	restored, err := r.inspect(ctx, old.ID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.ID != old.ID || restored.State.Running || !strings.HasPrefix(strings.TrimPrefix(restored.Name, "/"), "aact-previous-") {
		t.Fatalf("failed replacement should retain the old stopped container under its backup name: %+v", restored)
	}
	rows, err := store.Installations()
	if err != nil || len(rows) != 1 || rows[0].SourcePath != old.ID {
		t.Fatalf("prior ledger changed: %+v %v", rows, err)
	}
	items, err := r.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ownedCount := 0
	for _, item := range items {
		if item.Key == k {
			ownedCount++
		}
	}
	if ownedCount != 1 {
		t.Fatalf("failed replacement container remains: %+v", items)
	}
	replacement, err := r.Start(ctx, k, good)
	if err != nil {
		t.Fatal(err)
	}
	if replacement.ID == old.ID {
		t.Fatal("stopped old container reused instead of replacement")
	}
	if _, err = r.inspect(ctx, old.ID); err == nil {
		t.Fatal("old stopped container retained after healthy commit")
	}
	rows, err = store.Installations()
	if err != nil || len(rows) != 1 || rows[0].SourcePath != replacement.ID {
		t.Fatalf("successful retry did not retire the exact old container record: %+v %v", rows, err)
	}
}

func TestActualDockerKeepsNamedMCPChildrenIndependent(t *testing.T) {
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := NewDockerRuntime(store)
	parent := state.Key{Source: fmt.Sprintf("child-fixture-%d", time.Now().UnixNano()), Package: "fixture", Environment: "synthetic", Target: "test"}
	alpha := parent
	alpha.MCP = "alpha"
	beta := parent
	beta.MCP = "beta"
	image := "aact/child-fixture-" + parent.ID()[:16] + ":test"
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	t.Cleanup(func() {
		cleanup, c := context.WithTimeout(context.Background(), 15*time.Second)
		defer c()
		_ = r.Stop(cleanup, alpha)
		_ = r.Stop(cleanup, beta)
		_, _ = r.run(cleanup, []string{"image", "rm", image})
	})
	build, err := filepath.Abs("../../testdata/mcp-fixture")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.run(ctx, []string{"build", "--quiet", "--tag", image, build}); err != nil {
		t.Fatal(err)
	}
	nextPort := func() int {
		t.Helper()
		listener, listenErr := net.Listen("tcp", "127.0.0.1:0")
		if listenErr != nil {
			t.Fatal(listenErr)
		}
		port := listener.Addr().(*net.TCPAddr).Port
		if closeErr := listener.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
		return port
	}
	alphaPort, betaPort := nextPort(), nextPort()
	for alphaPort == betaPort {
		betaPort = nextPort()
	}
	mount := t.TempDir()
	if err := os.WriteFile(filepath.Join(mount, "profile.txt"), []byte("named-child-fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	spec := func(port int) RunSpec {
		return RunSpec{Image: image, Host: "127.0.0.1", HostPort: port, ContainerPort: 8765, Transport: "streamable-http", EndpointPath: "/mcp", Mounts: []Mount{{Source: mount, Destination: "/fixture", ReadOnly: true}}}
	}
	if _, err := r.Start(ctx, alpha, spec(alphaPort)); err != nil {
		t.Fatal(err)
	}
	betaRuntime, err := r.Start(ctx, beta, spec(betaPort))
	if err != nil {
		t.Fatal(err)
	}
	listed, err := r.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := map[state.Key]Instance{}
	for _, item := range listed {
		if item.Key == alpha || item.Key == beta {
			found[item.Key] = item
		}
	}
	if found[alpha].Key != alpha || found[beta].Key != beta || found[alpha].Ownership != "local" || found[beta].Ownership != "local" || found[alpha].Status != "running" || found[beta].Status != "running" {
		t.Fatalf("docker list did not reconstruct exact child identities: %+v", listed)
	}
	logs, err := r.Logs(ctx, alpha)
	if err != nil {
		t.Fatalf("logs for alpha child: %v", err)
	}
	if err := logs.Close(); err != nil {
		t.Fatal(err)
	}
	if err := r.Stop(ctx, alpha); err != nil {
		t.Fatal(err)
	}
	listed, err = r.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range listed {
		if item.Key == alpha && item.Status != "missing" {
			t.Fatalf("stopped child remains live: %+v", item)
		}
	}
	if _, err := r.inspect(ctx, betaRuntime.ID); err != nil {
		t.Fatalf("stopping alpha affected beta child: %v", err)
	}
	if betaAfter, err := r.List(ctx); err != nil {
		t.Fatal(err)
	} else {
		for _, item := range betaAfter {
			if item.Key == beta && (item.Status != "running" || item.Ownership != "local") {
				t.Fatalf("beta child changed after stopping alpha: %+v", item)
			}
		}
	}
}
