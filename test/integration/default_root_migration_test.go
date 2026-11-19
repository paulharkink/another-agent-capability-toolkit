package integration

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/vault"
)

func TestDefaultRootMigrationPreservesStateAndSecretInputs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows already uses the AACT state directory name")
	}
	stateHome := t.TempDir()
	legacyRoot := filepath.Join(stateHome, "agent-skills")
	newRoot := filepath.Join(stateHome, "aact")
	setDefaultRootTestEnvironment(t, stateHome)

	legacyStore, err := state.Open(legacyRoot)
	if err != nil {
		t.Fatal(err)
	}
	installationID, err := legacyStore.InstallationID()
	if err != nil {
		t.Fatal(err)
	}
	key := state.Key{Source: "fixture-source", Package: "fixture-pack", Target: "fixture-instance"}
	installation := state.Installation{
		Key:         key,
		AgentID:     "fixture-agent",
		Component:   "mcp",
		Destination: "fixture destination",
		Mode:        "managed",
	}
	if err := legacyStore.Record(installation); err != nil {
		t.Fatal(err)
	}
	if err := legacyStore.SaveAnswers(key, map[string]any{"region": "eu-test"}); err != nil {
		t.Fatal(err)
	}
	legacyAuth := filepath.Join(legacyRoot, "profiles", "shared-auth")
	if err := legacyStore.AdoptAuth(key, legacyAuth); err != nil {
		t.Fatal(err)
	}
	if err := legacyStore.SaveSecretInputs(key, map[string]any{"fixture_token": "synthetic-migration-secret"}, nil); err != nil {
		t.Fatal(err)
	}

	migratedStore, err := state.Open("")
	if err != nil {
		t.Fatalf("open default root and migrate legacy state: %v", err)
	}
	if migratedStore.Root() != newRoot {
		t.Fatalf("default state root=%q, want %q", migratedStore.Root(), newRoot)
	}
	migratedID, err := migratedStore.InstallationID()
	if err != nil || migratedID != installationID {
		t.Fatalf("installation ID after migration=%q, err=%v; want %q", migratedID, err, installationID)
	}
	rows, err := migratedStore.Installations()
	if err != nil || !reflect.DeepEqual(rows, []state.Installation{installation}) {
		t.Fatalf("installations after migration=%#v, err=%v", rows, err)
	}
	answers, err := migratedStore.Answers(key)
	if err != nil || answers["region"] != "eu-test" {
		t.Fatalf("answers after migration=%#v, err=%v", answers, err)
	}
	newAuth := filepath.Join(newRoot, "profiles", "shared-auth")
	if got := migratedStore.AuthDir(key); got != newAuth {
		t.Fatalf("auth directory after migration=%q, want %q", got, newAuth)
	}
	if _, err := os.Stat(legacyRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy state root remains after migration: %v", err)
	}

	secretStore, err := vault.Open(filepath.Join(t.TempDir(), "credentials.kdbx"), []byte("isolated-migration-password"))
	if err != nil {
		t.Fatal(err)
	}
	migratedStore.SetSecretBackend(secretStore)
	if err := migratedStore.MigrateLegacySecretInputs(); err != nil {
		t.Fatalf("migrate legacy private inputs into isolated Vault: %v", err)
	}
	secrets, err := migratedStore.SecretInputs(key)
	if err != nil || secrets["fixture_token"] != "synthetic-migration-secret" {
		t.Fatalf("private input after Vault migration=%#v, err=%v", secrets, err)
	}
	if _, err := os.Stat(filepath.Join(newAuth, "aact-private-inputs.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("plaintext private-input file remains after Vault migration: %v", err)
	}
}

func TestDefaultRootConflictPreservesBothRoots(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows already uses the AACT state directory name")
	}
	stateHome := t.TempDir()
	legacyRoot := filepath.Join(stateHome, "agent-skills")
	newRoot := filepath.Join(stateHome, "aact")
	setDefaultRootTestEnvironment(t, stateHome)
	writeDefaultRootFixture(t, filepath.Join(legacyRoot, "legacy-marker"), "legacy-state")
	writeDefaultRootFixture(t, filepath.Join(newRoot, "aact-marker"), "aact-state")

	_, err := state.Open("")
	if err == nil {
		t.Fatal("opening populated legacy and AACT roots unexpectedly succeeded")
	}
	for _, expected := range []string{legacyRoot, newRoot, "choose which directory to keep"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("conflict error %q does not contain %q", err, expected)
		}
	}
	for path, want := range map[string]string{
		filepath.Join(legacyRoot, "legacy-marker"): "legacy-state",
		filepath.Join(newRoot, "aact-marker"):      "aact-state",
	} {
		got, readErr := os.ReadFile(path)
		if readErr != nil || string(got) != want {
			t.Errorf("conflict changed %q: got %q, err=%v", path, got, readErr)
		}
	}
}

func TestReadOnlyDefaultRootOpenDoesNotMigrateOrCreateRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows already uses the AACT state directory name")
	}
	stateHome := t.TempDir()
	legacyRoot := filepath.Join(stateHome, "agent-skills")
	newRoot := filepath.Join(stateHome, "aact")
	setDefaultRootTestEnvironment(t, stateHome)
	marker := filepath.Join(legacyRoot, "legacy-marker")
	writeDefaultRootFixture(t, marker, "preserve")

	store, err := state.OpenReadOnly("")
	if err != nil {
		t.Fatal(err)
	}
	if store.Root() != legacyRoot {
		t.Fatalf("read-only root=%q, want legacy root %q", store.Root(), legacyRoot)
	}
	if got, err := os.ReadFile(marker); err != nil || string(got) != "preserve" {
		t.Fatalf("read-only open changed legacy state: got %q, err=%v", got, err)
	}
	if _, err := os.Stat(newRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read-only open created AACT root: %v", err)
	}
}

func TestDefaultRootInitializationAndOverrides(t *testing.T) {
	t.Run("fresh default", func(t *testing.T) {
		stateHome := t.TempDir()
		setDefaultRootTestEnvironment(t, stateHome)
		store, err := state.Open("")
		if err != nil {
			t.Fatal(err)
		}
		want := filepath.Join(stateHome, "aact")
		if store.Root() != want {
			t.Fatalf("default state root=%q, want %q", store.Root(), want)
		}
		if _, err := os.Stat(filepath.Join(want, "manager")); err != nil {
			t.Fatalf("default root was not initialized: %v", err)
		}
		if _, err := os.Stat(filepath.Join(stateHome, "agent-skills")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("unexpected legacy root: %v", err)
		}
	})

	for _, tc := range []struct {
		name     string
		openRoot func(string) string
		stateEnv bool
	}{
		{name: "explicit state root", openRoot: func(override string) string { return override }},
		{name: "AACT_STATE_DIR", openRoot: func(string) string { return "" }, stateEnv: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stateHome := t.TempDir()
			setDefaultRootTestEnvironment(t, stateHome)
			legacyRoot := filepath.Join(stateHome, "agent-skills")
			marker := filepath.Join(legacyRoot, "legacy-marker")
			writeDefaultRootFixture(t, marker, "preserve")
			override := filepath.Join(stateHome, "override")
			if tc.stateEnv {
				t.Setenv("AACT_STATE_DIR", override)
			}
			store, err := state.Open(tc.openRoot(override))
			if err != nil {
				t.Fatal(err)
			}
			if store.Root() != override {
				t.Fatalf("override state root=%q, want %q", store.Root(), override)
			}
			if got, err := os.ReadFile(marker); err != nil || string(got) != "preserve" {
				t.Fatalf("override moved legacy state: got %q, err=%v", got, err)
			}
		})
	}
}

func setDefaultRootTestEnvironment(t *testing.T, stateHome string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("AACT_STATE_DIR", "")
}

func writeDefaultRootFixture(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
}
