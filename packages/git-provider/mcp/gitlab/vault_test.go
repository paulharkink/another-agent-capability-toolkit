package main

import (
	"encoding/json"
	"encoding/pem"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/vault"
)

func TestLoadVaultSecretsUsesSavedParentProfileInputsAndRejectsChildNamespace(t *testing.T) {
	vaultStore, err := vault.Open(filepath.Join(t.TempDir(), "credentials.kdbx"), []byte("test-master-password"))
	if err != nil {
		t.Fatal(err)
	}
	stateStore, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stateStore.SetSecretBackend(vaultStore)
	parent := state.Key{Source: "pack", Package: "git-provider", Target: "workspace"}
	child := parent
	child.MCP = "github"
	const tokenValue = "github-vault-token-fixture"
	if err := stateStore.SaveSecretInputs(parent, map[string]any{"github_token": tokenValue}, nil); err != nil {
		t.Fatal(err)
	}

	api := vault.NewAPI(vaultStore, nil)
	parentToken, err := api.IssuePrivateInputToken(parent.ID(), []string{"github_token"})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(api)
	defer server.Close()
	handoff := vault.ContainerHandoff{APIAddress: server.URL, Certificate: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})), Token: parentToken}
	handoffPath := filepath.Join(t.TempDir(), "vault.json")
	writeHandoff := func(value vault.ContainerHandoff) {
		t.Helper()
		data, _ := json.Marshal(value)
		if err := os.WriteFile(handoffPath, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeHandoff(handoff)
	secrets, err := loadVaultSecrets([]string{"AACT_VAULT_SECRET_INPUT_GITHUB_PERSONAL_ACCESS_TOKEN=github_token"}, handoffPath)
	if err != nil {
		t.Fatal(err)
	}
	if secrets["GITHUB_PERSONAL_ACCESS_TOKEN"] != tokenValue {
		t.Fatalf("loaded secret = %q, want saved parent input", secrets["GITHUB_PERSONAL_ACCESS_TOKEN"])
	}

	childToken, err := api.IssuePrivateInputToken(child.ID(), []string{"github_token"})
	if err != nil {
		t.Fatal(err)
	}
	if childToken == parentToken {
		t.Fatal("parent and child namespaces unexpectedly shared a token")
	}
	childHandoff := handoff
	childHandoff.Token = childToken
	writeHandoff(childHandoff)
	if _, err := loadVaultSecrets([]string{"AACT_VAULT_SECRET_INPUT_GITHUB_PERSONAL_ACCESS_TOKEN=github_token"}, handoffPath); err == nil {
		t.Fatal("child namespace unexpectedly read parent profile secret")
	}
}
