package mcp

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/vault"
)

// This disposable integration fixture is opt-in and uses a generic node:alpine
// image, never a real AACT MCP image or existing container.
func TestDockerVaultHandoffTmpfsIntegration(t *testing.T) {
	if os.Getenv("AACT_TEST_DOCKER") != "1" {
		t.Skip("set AACT_TEST_DOCKER=1 to run the disposable Docker fixture")
	}
	helper := os.Getenv("AACT_VAULT_HANDOFF_HELPER")
	if helper == "" {
		t.Fatal("AACT_VAULT_HANDOFF_HELPER must point to a Linux helper binary")
	}
	if _, err := os.Stat(helper); err != nil {
		t.Fatal(err)
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker CLI is not installed")
	}
	r, err := NewDockerRuntimeForTest(t)
	if err != nil {
		t.Fatal(err)
	}
	portListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := portListener.Addr().(*net.TCPAddr).Port
	_ = portListener.Close()
	key := state.Key{Source: "fixture", Package: fmt.Sprintf("handoff-%d", time.Now().UnixNano()), Target: "integration"}
	r.VaultHandoffHelperPath = helper
	r.SkipHealth = true
	const bearer = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	_, err = r.Start(context.Background(), key, RunSpec{Image: "node:22-alpine", Args: []string{"sh", "-c", "sleep 300"}, Host: "127.0.0.1", HostPort: port, ContainerPort: 12345, VaultHandoff: &vault.ContainerHandoff{APIAddress: "https://host.docker.internal:32123", Certificate: "fixture-cert", Token: bearer}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Stop(context.Background(), key) }()
	content, err := r.Exec(context.Background(), key, []string{"cat", vault.ContainerHandoffPath}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), bearer) {
		t.Fatalf("tmpfs handoff file did not contain the supplied token")
	}
	mounts, err := r.Exec(context.Background(), key, []string{"sh", "-c", "grep ' /run/aact tmpfs ' /proc/mounts"}, nil, nil)
	if err != nil || !strings.Contains(string(mounts), "tmpfs") {
		t.Fatalf("handoff tmpfs mount missing: %q %v", mounts, err)
	}
}

func NewDockerRuntimeForTest(t *testing.T) (*Runtime, error) {
	t.Helper()
	store, err := state.Open(t.TempDir())
	if err != nil {
		return nil, err
	}
	return NewDockerRuntime(store), nil
}
