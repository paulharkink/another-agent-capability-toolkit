package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/vault"
)

func TestInstanceInputSwitchFromRawTokenToFileOrEnvironmentUsesSubmittedSource(t *testing.T) {
	svc, _, _ := fixture(t)
	vaultStore, err := vault.Open(filepath.Join(t.TempDir(), "credentials.kdbx"), []byte("test-master-password"))
	if err != nil {
		t.Fatal(err)
	}
	svc.Store.SetSecretBackend(vaultStore)
	key := state.Key{Source: svc.Source.ID, Package: "fixture", Target: "work"}
	if err := svc.Store.SaveSecretInputs(key, map[string]any{"token": "old-raw-token"}, nil); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "provider-token")
	if err := os.WriteFile(file, []byte("file-token"), 0600); err != nil {
		t.Fatal(err)
	}
	pkg := catalog.Package{ID: key.Package, Inputs: []catalog.Input{
		{Name: "token", Type: "secret"}, {Name: "token_file", Type: "file"}, {Name: "token_env", Type: "string"},
	}, MCP: &catalog.MCP{Name: "provider", TokenInput: "token", TokenFileInput: "token_file", TokenEnvInput: "token_env", VaultSecretEnvInputs: map[string]string{"PROVIDER_TOKEN": "token"}, VaultAuthEnv: "PROVIDER_TOKEN"}}
	profile := config.Instance{Ref: config.InstanceRef{PackID: key.Source, CapabilityID: key.Package, Name: key.Target}}
	_, _, values, _, err := svc.instanceValuesForPreview(pkg, profile, key, InstanceRequest{Inputs: map[string]any{"token_file": file}})
	if err != nil {
		t.Fatal(err)
	}
	token, err := resolveProfileToken(*pkg.MCP, values)
	if err != nil || token != "file-token" {
		t.Fatalf("selected external token = %q err=%v, want file token", token, err)
	}
	t.Run("environment", func(t *testing.T) {
		envKey := key
		envKey.Target = "work-environment"
		if err := svc.Store.SaveSecretInputs(envKey, map[string]any{"token": "old-raw-token"}, nil); err != nil {
			t.Fatal(err)
		}
		t.Setenv("AACT_TEST_RAW_TO_ENV_TOKEN", "environment-token")
		profile := config.Instance{Ref: config.InstanceRef{PackID: envKey.Source, CapabilityID: envKey.Package, Name: envKey.Target}}
		_, _, values, _, err := svc.instanceValuesForPreview(pkg, profile, envKey, InstanceRequest{Inputs: map[string]any{"token_env": "AACT_TEST_RAW_TO_ENV_TOKEN"}})
		if err != nil {
			t.Fatal(err)
		}
		token, err := resolveProfileToken(*pkg.MCP, values)
		if err != nil || token != "environment-token" {
			t.Fatalf("selected environment token = %q err=%v, want environment token", token, err)
		}
	})
}

func TestVaultProviderSourcesMigrateAndRefreshRawFileAndEnvironmentValues(t *testing.T) {
	svc, _, _ := fixture(t)
	vaultStore, err := vault.Open(filepath.Join(t.TempDir(), "credentials.kdbx"), []byte("test-master-password"))
	if err != nil {
		t.Fatal(err)
	}
	svc.Store.SetSecretBackend(vaultStore)
	pkg := catalog.Package{ID: "fixture", Inputs: []catalog.Input{
		{Name: "token", Type: "secret"}, {Name: "token_file", Type: "file"}, {Name: "token_env", Type: "string"}, {Name: "port", Type: "integer"},
	}, MCP: &catalog.MCP{Name: "git", TokenInput: "token", TokenFileInput: "token_file", TokenEnvInput: "token_env", VaultSecretEnvInputs: map[string]string{"PROVIDER_TOKEN": "token"}, VaultAuthEnv: "PROVIDER_TOKEN"}}
	key := state.Key{Source: "fixture-pack", Package: "fixture", Target: "workspace"}
	file := filepath.Join(t.TempDir(), "provider-token")
	if err := os.WriteFile(file, []byte("file-token-one\n"), 0600); err != nil {
		t.Fatal(err)
	}
	values := map[string]any{"token_file": file, "port": int64(8765)}
	save := func(values map[string]any, reset []string) {
		t.Helper()
		if err := svc.saveAnswersWithActiveGroups(key, pkg, values, false, reset, nil); err != nil {
			t.Fatal(err)
		}
	}
	secret := func() string {
		t.Helper()
		values, err := svc.Store.SecretInputs(key)
		if err != nil {
			t.Fatal(err)
		}
		text, _ := values["token"].(string)
		return text
	}
	save(values, nil)
	if got := secret(); got != "file-token-one" {
		t.Fatalf("file source saved %q", got)
	}
	firstRevision, err := svc.Store.SecretInputsRevision(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("file-token-two\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runtime := &providerTestRuntime{}
	svc.Options.Runtime = runtime
	profile := config.Profile{Ref: config.ProfileRef{PackID: key.Source, CapabilityID: key.Package, Name: key.Target}}
	child := key
	child.MCP = "git"
	if _, err := svc.startConfigurationProfile(t.Context(), pkg, profile, child, values, false); err != nil {
		t.Fatal(err)
	}
	if got := secret(); got != "file-token-two" {
		t.Fatalf("rotated file source on start saved %q", got)
	}
	if len(runtime.specs) != 1 || runtime.specs[0].VaultSecretRevision == firstRevision {
		t.Fatalf("file rotation did not refresh runtime revision: %#v", runtime.specs)
	}
	if answers, err := svc.Store.Answers(key); err != nil || answers["token_file"] != file {
		t.Fatalf("file selection not retained: %#v, %v", answers, err)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.startConfigurationProfile(t.Context(), pkg, profile, child, values, false); err == nil {
		t.Fatal("missing external token file was accepted on start")
	}
	if got := secret(); got != "file-token-two" {
		t.Fatalf("missing file changed saved token to %q", got)
	}
	if err := os.WriteFile(file, []byte("file-token-two\n"), 0600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("AACT_TEST_PROVIDER_TOKEN", "env-token-one")
	envValues := map[string]any{"token_env": "AACT_TEST_PROVIDER_TOKEN", "port": int64(8765)}
	save(envValues, nil)
	if got := secret(); got != "env-token-one" {
		t.Fatalf("environment source saved %q", got)
	}
	envRevision := runtime.specs[0].VaultSecretRevision
	t.Setenv("AACT_TEST_PROVIDER_TOKEN", "env-token-two")
	if _, err := svc.startConfigurationProfile(t.Context(), pkg, profile, child, envValues, false); err != nil {
		t.Fatal(err)
	}
	if got := secret(); got != "env-token-two" {
		t.Fatalf("rotated environment source on start saved %q", got)
	}
	if runtime.specs[1].VaultSecretRevision == envRevision {
		t.Fatalf("environment rotation kept runtime revision %q", envRevision)
	}

	if err := svc.saveAnswersWithActiveGroups(key, pkg, map[string]any{"token": "raw-token", "token_file": file, "port": int64(8765)}, false, nil, nil); err == nil {
		t.Fatal("exclusive raw and file sources were accepted")
	}
	missing := map[string]any{"token_file": filepath.Join(t.TempDir(), "missing"), "port": int64(8765)}
	if err := svc.saveAnswersWithActiveGroups(key, pkg, missing, false, nil, nil); err == nil {
		t.Fatal("missing file source was accepted")
	}
	if got := secret(); got != "env-token-two" {
		t.Fatalf("failed source changed saved token to %q", got)
	}
	save(map[string]any{"port": int64(8765)}, []string{"token_env"})
	if got := secret(); got != "" {
		t.Fatalf("reset did not clear saved provider token: %q", got)
	}
}
