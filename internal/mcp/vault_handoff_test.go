package mcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/vault"
)

func TestGenericRuntimeDeliversVaultHandoffThroughStdinIntoTmpfs(t *testing.T) {
	r, executor, key := testRuntime(t)
	executor.f = absent
	helper := filepath.Join(t.TempDir(), "aact-vault-handoff")
	if err := os.WriteFile(helper, []byte("fixture-helper"), 0700); err != nil {
		t.Fatal(err)
	}
	r.VaultHandoffHelperPath = helper
	handoff := &vault.ContainerHandoff{
		APIAddress:  "https://host.docker.internal:32123",
		Certificate: "public-certificate-fixture",
		Token:       "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
	}
	_, err := r.Start(context.Background(), key, RunSpec{Image: "fixture", HostPort: 8765, ContainerPort: 80, VaultHandoff: handoff})
	if err != nil {
		t.Fatal(err)
	}
	var runFound, execFound bool
	for index, args := range executor.calls {
		if len(args) > 1 && args[1] == "run" {
			runFound = containsArgPair(args, "--tmpfs", vault.ContainerHandoffTmpfs)
			joined := strings.Join(args, " ")
			runFound = runFound && containsArgPair(args, "--mount", "type=bind,src="+helper+",dst=/run/aact-vault-handoff,readonly")
			if strings.Contains(joined, handoff.Token) || strings.Contains(joined, handoff.Certificate) {
				t.Fatalf("handoff leaked in docker argv: %v", args)
			}
		}
		if len(args) > 1 && args[1] == "exec" {
			execFound = strings.Contains(strings.Join(args, " "), "/run/aact-vault-handoff") && index < len(executor.inputs) && strings.Contains(string(executor.inputs[index]), handoff.Token)
			if index < len(executor.inputs) && strings.Contains(string(executor.inputs[index]), handoff.Token) && strings.Contains(strings.Join(args, " "), handoff.Token) {
				t.Fatal("token appeared in exec argv")
			}
		}
	}
	if !runFound {
		t.Fatal("container was not given the AACT tmpfs mount")
	}
	if got := vaultHandoffHostGatewayArgs("linux"); len(got) != 2 || got[0] != "--add-host" || got[1] != "host.docker.internal:host-gateway" {
		t.Fatalf("Linux host gateway args = %#v", got)
	}
	if got := vaultHandoffHostGatewayArgs("darwin"); len(got) != 0 {
		t.Fatalf("non-Linux host gateway args = %#v, want none", got)
	}
	if !execFound {
		t.Fatal("handoff was not delivered over docker exec stdin")
	}
	for _, args := range executor.calls {
		for _, arg := range args {
			if strings.Contains(arg, handoff.Token) {
				t.Fatalf("bearer leaked to docker argument/config: %q", arg)
			}
		}
	}
}

func TestKnownContainerHandoffRevocationIsContainerScoped(t *testing.T) {
	r := &Runtime{vaultRevokers: &sync.Map{}}
	oldRevoked, newRevoked := false, false
	r.vaultRevokers.Store("old-container", func() error { oldRevoked = true; return nil })
	r.vaultRevokers.Store("new-container", func() error { newRevoked = true; return nil })
	if err := r.revokeKnownVaultHandoff("old-container", strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	if !oldRevoked || newRevoked {
		t.Fatalf("old=%v new=%v", oldRevoked, newRevoked)
	}
}

func containsArgPair(args []string, key, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == key && args[i+1] == value {
			return true
		}
	}
	return false
}

type fixtureVaultIssuer struct {
	issuedNamespace  string
	revokedNamespace string
	revoked          bool
	boundContainer   string
	revokedContainer string
}

func (issuer *fixtureVaultIssuer) IssueContainerHandoff(namespace string) (vault.ContainerHandoff, func() error, error) {
	issuer.issuedNamespace = namespace
	handoff := vault.ContainerHandoff{APIAddress: "https://host.docker.internal:32123", Certificate: "fixture-cert", Token: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}
	return handoff, func() error { issuer.revoked = true; return nil }, nil
}
func (issuer *fixtureVaultIssuer) RevokeContainerHandoffs(namespace string) error {
	issuer.revokedNamespace = namespace
	return nil
}
func (issuer *fixtureVaultIssuer) BindContainerHandoff(containerID, _ string) error {
	issuer.boundContainer = containerID
	return nil
}
func (issuer *fixtureVaultIssuer) RevokeContainerHandoff(namespace, containerID string) error {
	issuer.revokedNamespace, issuer.revokedContainer = namespace, containerID
	issuer.revoked = true
	return nil
}

func TestVaultHandoffIsExplicitOptInAndUsesExistingInstanceID(t *testing.T) {
	r, executor, key := testRuntime(t)
	executor.f = absent
	issuer := &fixtureVaultIssuer{}
	r.VaultTokenIssuer = issuer
	if _, err := r.Start(context.Background(), key, RunSpec{Image: "fixture", HostPort: 8765, ContainerPort: 80}); err != nil {
		t.Fatal(err)
	}
	if issuer.issuedNamespace != "" {
		t.Fatal("vault handoff changed ordinary MCP startup without opt-in")
	}
	r, executor, key = testRuntime(t)
	executor.f = absent
	r.VaultTokenIssuer = issuer
	helper := filepath.Join(t.TempDir(), "helper")
	if err := os.WriteFile(helper, []byte("helper fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	r.VaultHandoffHelperPath = helper
	if _, err := r.Start(context.Background(), key, RunSpec{Image: "fixture", HostPort: 8766, ContainerPort: 80, VaultHandoffEnabled: true}); err != nil {
		t.Fatal(err)
	}
	if issuer.issuedNamespace != key.ID() {
		t.Fatalf("issuer namespace=%q, want existing Key.ID() %q", issuer.issuedNamespace, key.ID())
	}
	if err := r.revokeVaultHandoff("cid", key.ID()); err != nil {
		t.Fatal(err)
	}
	if !issuer.revoked || issuer.revokedNamespace != key.ID() || issuer.revokedContainer != "cid" {
		t.Fatalf("revoke=%v namespace=%q container=%q", issuer.revoked, issuer.revokedNamespace, issuer.revokedContainer)
	}
}
