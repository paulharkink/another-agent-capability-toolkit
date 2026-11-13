package state

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLegacyDefaultRootMigrationPreservesIdentityAndState(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows already uses the AACT state directory name")
	}
	stateHome := t.TempDir()
	legacyRoot := filepath.Join(stateHome, "agent-skills")
	newRoot := filepath.Join(stateHome, "aact")
	instance := Key{Source: "pack", Package: "capability", Environment: "prod", Target: "default", MCP: "server"}
	instanceDir := filepath.Join(legacyRoot, "instances", instance.ID())
	wants := map[string]string{
		filepath.Join(legacyRoot, "manager", "installation-id"):         "fixture-installation-id\n",
		filepath.Join(legacyRoot, "manager", "installations.json"):      `{"version":1,"installations":[]}`,
		filepath.Join(instanceDir, "answers.json"):                      `{"version":1,"values":{"region":"eu"}}`,
		filepath.Join(legacyRoot, "auth", instance.ID(), "secret.json"): `{"token":"fixture"}`,
	}
	for path, contents := range wants {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("AACT_STATE_DIR", "")

	store, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	if store.Root() != newRoot {
		t.Fatalf("state root = %q, want %q", store.Root(), newRoot)
	}
	if got := instance.ID(); got != filepath.Base(instanceDir) {
		t.Fatalf("instance ID changed during migration: %q != %q", got, filepath.Base(instanceDir))
	}
	for oldPath, want := range wants {
		newPath := filepath.Join(newRoot, oldPath[len(legacyRoot)+1:])
		got, err := os.ReadFile(newPath)
		if err != nil || string(got) != want {
			t.Fatalf("migrated %q = %q, %v; want %q", newPath, got, err, want)
		}
	}
	if _, err := os.Stat(legacyRoot); !os.IsNotExist(err) {
		t.Fatalf("legacy root remains after migration: err=%v", err)
	}
}

func TestLegacyDefaultRootMigrationRefusesTwoPopulatedRoots(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows already uses the AACT state directory name")
	}
	stateHome := t.TempDir()
	legacyRoot := filepath.Join(stateHome, "agent-skills")
	newRoot := filepath.Join(stateHome, "aact")
	for path, contents := range map[string]string{
		filepath.Join(legacyRoot, "legacy.txt"): "legacy",
		filepath.Join(newRoot, "new.txt"):       "new",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("AACT_STATE_DIR", "")

	if _, err := Open(""); err == nil {
		t.Fatal("expected migration conflict when both roots contain state")
	} else if !containsAll(err.Error(), legacyRoot, newRoot, "choose which directory to keep") {
		t.Fatalf("conflict error does not explain both paths and remediation: %v", err)
	}
	for path, want := range map[string]string{
		filepath.Join(legacyRoot, "legacy.txt"): "legacy",
		filepath.Join(newRoot, "new.txt"):       "new",
	} {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != want {
			t.Fatalf("conflict changed %q: got %q, %v", path, got, err)
		}
	}
}

func TestLegacyDefaultRootReadOnlyOpenDoesNotMoveState(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows already uses the AACT state directory name")
	}
	stateHome := t.TempDir()
	legacyRoot := filepath.Join(stateHome, "agent-skills")
	newRoot := filepath.Join(stateHome, "aact")
	if err := os.MkdirAll(legacyRoot, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(legacyRoot, "legacy-marker")
	if err := os.WriteFile(marker, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("AACT_STATE_DIR", "")

	store, err := OpenReadOnly("")
	if err != nil {
		t.Fatal(err)
	}
	if store.Root() != legacyRoot {
		t.Fatalf("read-only root = %q, want legacy %q", store.Root(), legacyRoot)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("read-only open changed legacy state: %v", err)
	}
	if _, err := os.Stat(newRoot); !os.IsNotExist(err) {
		t.Fatalf("read-only open created new state root: %v", err)
	}
}

func TestLegacyDefaultRootMigrationNoLegacyRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows already uses the AACT state directory name")
	}
	stateHome := t.TempDir()
	newRoot := filepath.Join(stateHome, "aact")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("AACT_STATE_DIR", "")

	store, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	if store.Root() != newRoot {
		t.Fatalf("default root = %q, want %q", store.Root(), newRoot)
	}
	if _, err := os.Stat(filepath.Join(newRoot, "manager")); err != nil {
		t.Fatalf("new root was not initialized: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stateHome, "agent-skills")); !os.IsNotExist(err) {
		t.Fatalf("unexpected legacy root created: %v", err)
	}
}

func TestExplicitStateRootDoesNotMigrateLegacyDefault(t *testing.T) {
	stateHome := t.TempDir()
	legacyRoot := filepath.Join(stateHome, "agent-skills")
	overrideRoot := filepath.Join(stateHome, "explicit")
	if err := os.MkdirAll(legacyRoot, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(legacyRoot, "legacy-marker")
	if err := os.WriteFile(marker, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("AACT_STATE_DIR", "")

	store, err := Open(overrideRoot)
	if err != nil {
		t.Fatal(err)
	}
	if store.Root() != overrideRoot {
		t.Fatalf("explicit root = %q, want %q", store.Root(), overrideRoot)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("explicit root open moved legacy state: %v", err)
	}
}

func TestAACTStateDirOverrideDoesNotMigrateLegacyDefault(t *testing.T) {
	stateHome := t.TempDir()
	legacyRoot := filepath.Join(stateHome, "agent-skills")
	overrideRoot := filepath.Join(stateHome, "override")
	if err := os.MkdirAll(legacyRoot, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(legacyRoot, "legacy-marker")
	if err := os.WriteFile(marker, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("AACT_STATE_DIR", overrideRoot)

	store, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	if store.Root() != overrideRoot {
		t.Fatalf("AACT_STATE_DIR root = %q, want %q", store.Root(), overrideRoot)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("AACT_STATE_DIR open moved legacy state: %v", err)
	}
}

func TestFailedLegacyRootRenamePreservesSource(t *testing.T) {
	stateHome := t.TempDir()
	legacyRoot := filepath.Join(stateHome, "agent-skills")
	newRoot := filepath.Join(stateHome, "aact")
	if err := os.MkdirAll(legacyRoot, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(legacyRoot, "legacy-marker")
	if err := os.WriteFile(marker, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	blocked := errors.New("rename blocked for test")
	_, err := resolveLegacyDefaultRootWithRename(newRoot, legacyRoot, false, func(string, string) error {
		return blocked
	})
	if !errors.Is(err, blocked) {
		t.Fatalf("migration error = %v, want injected rename error", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("failed migration lost legacy data: %v", err)
	}
	if _, err := os.Stat(newRoot); !os.IsNotExist(err) {
		t.Fatalf("failed migration created new root: %v", err)
	}
}

func containsAll(value string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(value, part) {
			return false
		}
	}
	return true
}
