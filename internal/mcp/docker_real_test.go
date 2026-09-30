//go:build docker_integration

package mcp

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
)

func TestActualDockerRestoresStoppedRuntimeAfterHealthFailure(t *testing.T) {
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
	restored, err := r.inspect(ctx, containerName(k))
	if err != nil {
		t.Fatal(err)
	}
	if restored.ID != old.ID || restored.State.Running {
		t.Fatalf("prior stopped container not restored: %+v", restored)
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
}
