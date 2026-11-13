package app

import (
	"context"
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
)

func TestVaultSecretEnvInputsUseParentProfileNamespaceWithoutDockerSecrets(t *testing.T) {
	svc, _, _ := fixture(t)
	runtime := &providerTestRuntime{}
	svc.Options.Runtime = runtime
	pkg := catalog.Package{ID: "fixture", Dir: t.TempDir(), MCP: &catalog.MCP{
		Name: "fixture", Image: "fixture", Transport: "streamable-http", HostPortInput: "port", ContainerPort: 80,
		VaultSecretEnvInputs: map[string]string{"PROVIDER_TOKEN": "access_token"}, VaultAuthEnv: "PROVIDER_TOKEN",
	}}
	parent := state.Key{Source: "fixture-pack", Package: "fixture", Target: "workspace"}
	child := parent
	child.MCP = "fixture"
	profile := config.Profile{Ref: config.ProfileRef{PackID: parent.Source, CapabilityID: parent.Package, Name: parent.Target}}
	if err := svc.Store.SaveSecretInputs(parent, map[string]any{"access_token": "test-secret"}, nil); err != nil {
		t.Fatal(err)
	}
	_, err := svc.startConfigurationProfile(context.Background(), pkg, profile, child, map[string]any{"port": int64(8765), "access_token": "test-secret"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(runtime.specs) != 1 {
		t.Fatalf("runtime start count = %d", len(runtime.specs))
	}
	got := runtime.specs[0]
	if !got.VaultHandoffEnabled || got.VaultNamespaceID != parent.ID() {
		t.Fatalf("vault handoff namespace = enabled:%t namespace:%q, want parent %q", got.VaultHandoffEnabled, got.VaultNamespaceID, parent.ID())
	}
	if got.VaultSecretRevision == "" || got.VaultSecretRevision == "test-secret" {
		t.Fatalf("vault secret revision exposed or missing: %q", got.VaultSecretRevision)
	}
	if len(got.SecretEnv) != 0 {
		t.Fatalf("secret was put in Docker secret env: %#v", got.SecretEnv)
	}
	if got.Env["AACT_VAULT_SECRET_INPUT_PROVIDER_TOKEN"] != "access_token" || got.Env["AACT_VAULT_AUTH_ENV"] != "PROVIDER_TOKEN" {
		t.Fatalf("generic secret-name mapping = %#v", got.Env)
	}
	if got.Env["AACT_VAULT_SECRET_INPUT_PROVIDER_TOKEN"] == "test-secret" {
		t.Fatal("provider secret reached Docker environment")
	}
	firstRevision := got.VaultSecretRevision
	if err := svc.Store.SaveSecretInputs(parent, map[string]any{"access_token": "rotated-secret"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.startConfigurationProfile(context.Background(), pkg, profile, child, map[string]any{"port": int64(8765), "access_token": "rotated-secret"}, false); err != nil {
		t.Fatal(err)
	}
	rotated := runtime.specs[1]
	if rotated.VaultSecretRevision == firstRevision || rotated.VaultSecretRevision == "rotated-secret" {
		t.Fatalf("rotated revision = %q, previous=%q", rotated.VaultSecretRevision, firstRevision)
	}
}
