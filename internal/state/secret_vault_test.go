package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

type testSecretBackend struct {
	values map[string][]byte
	putErr error
}

func (b *testSecretBackend) Get(namespace, name string) ([]byte, bool, error) {
	v, ok := b.values[namespace+"/"+name]
	return append([]byte(nil), v...), ok, nil
}
func (b *testSecretBackend) Put(namespace, name string, value []byte) error {
	if b.putErr != nil {
		return b.putErr
	}
	b.values[namespace+"/"+name] = append([]byte(nil), value...)
	return nil
}
func (b *testSecretBackend) Delete(namespace, name string) error {
	delete(b.values, namespace+"/"+name)
	return nil
}

func TestSecretInputsMigratesLegacyJSONOnlyAfterVaultWrite(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	key := Key{Source: "source", Package: "pack", Target: "target", MCPDefinition: "mcp"}
	legacy := filepath.Join(store.AuthDir(key), "aact-private-inputs.json")
	if err := os.MkdirAll(filepath.Dir(legacy), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte(`{"version":1,"values":{"token":"old-secret","count":3}}`), 0600); err != nil {
		t.Fatal(err)
	}
	backend := &testSecretBackend{values: map[string][]byte{}}
	store.SetSecretBackend(backend)
	values, err := store.SecretInputs(key)
	if err != nil {
		t.Fatal(err)
	}
	if values["token"] != "old-secret" || fmt.Sprint(values["count"]) != "3" {
		t.Fatalf("migrated values=%#v", values)
	}
	if _, err := os.Stat(legacy); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy plaintext file remains: %v", err)
	}
	stored, found, err := backend.Get(key.ID(), secretInputsVaultKey)
	if err != nil || !found {
		t.Fatalf("vault value found=%v err=%v", found, err)
	}
	var record secretInputRecord
	if err := json.Unmarshal(stored, &record); err != nil || record.Values["token"] != "old-secret" {
		t.Fatalf("vault record=%s err=%v", stored, err)
	}
}

func TestSecretInputsRetainsLegacyJSONWhenVaultMigrationFails(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	key := Key{Source: "source", Package: "pack", Target: "target", MCPDefinition: "mcp"}
	legacy := filepath.Join(store.AuthDir(key), "aact-private-inputs.json")
	if err := os.MkdirAll(filepath.Dir(legacy), 0700); err != nil {
		t.Fatal(err)
	}
	original := []byte(`{"version":1,"values":{"token":"old-secret"}}`)
	if err := os.WriteFile(legacy, original, 0600); err != nil {
		t.Fatal(err)
	}
	store.SetSecretBackend(&testSecretBackend{values: map[string][]byte{}, putErr: errors.New("simulated vault write error")})
	if _, err := store.SecretInputs(key); err == nil {
		t.Fatal("expected migration failure")
	}
	if got, err := os.ReadFile(legacy); err != nil || string(got) != string(original) {
		t.Fatalf("legacy source changed: %q err=%v", got, err)
	}
}

func TestMigrateLegacySecretInputsAtVaultUnlock(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	key := Key{Source: "source", Package: "pack", Target: "target", MCPDefinition: "mcp"}
	path := filepath.Join(store.AuthDir(key), "aact-private-inputs.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"version":1,"values":{"token":"old-secret"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	backend := &testSecretBackend{values: map[string][]byte{}}
	store.SetSecretBackend(backend)
	if err := store.MigrateLegacySecretInputs(); err != nil {
		t.Fatal(err)
	}
	if _, found, err := backend.Get(key.ID(), secretInputsVaultKey); err != nil || !found {
		t.Fatalf("vault migrated=%v err=%v", found, err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("plaintext remains: %v", err)
	}
}

func TestMigrateLegacySecretInputsPreservesExistingVaultValue(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	key := Key{Source: "source", Package: "pack", Target: "target", MCPDefinition: "mcp"}
	path := filepath.Join(store.AuthDir(key), "aact-private-inputs.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"version":1,"values":{"legacy":"stale"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	newer := []byte(`{"version":1,"values":{"vault":"new"}}`)
	backend := &testSecretBackend{values: map[string][]byte{key.ID() + "/" + secretInputsVaultKey: newer}}
	store.SetSecretBackend(backend)
	if err := store.MigrateLegacySecretInputs(); err != nil {
		t.Fatal(err)
	}
	got, found, err := backend.Get(key.ID(), secretInputsVaultKey)
	if err != nil || !found {
		t.Fatalf("Get() found=%v err=%v", found, err)
	}
	if string(got) != string(newer) {
		t.Fatalf("newer vault record overwritten: %s", got)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy plaintext remains: %v", err)
	}
}

func TestSecretInputRevisionChangesOnlyWhenSavedValuesChange(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	key := Key{Source: "source", Package: "pack", Target: "target", MCPDefinition: "mcp"}
	store.SetSecretBackend(&testSecretBackend{values: map[string][]byte{}})
	if err := store.SaveSecretInputs(key, map[string]any{"token": "first"}, nil); err != nil {
		t.Fatal(err)
	}
	first, err := store.SecretInputsRevision(key)
	if err != nil || first == "" {
		t.Fatalf("initial revision=%q err=%v", first, err)
	}
	if first == "first" || first == "second" {
		t.Fatalf("revision exposed credential value: %q", first)
	}
	if err := store.SaveSecretInputs(key, map[string]any{"token": "first"}, nil); err != nil {
		t.Fatal(err)
	}
	unchanged, err := store.SecretInputsRevision(key)
	if err != nil || unchanged != first {
		t.Fatalf("unchanged revision=%q err=%v, want %q", unchanged, err, first)
	}
	if err := store.SaveSecretInputs(key, map[string]any{"token": "second"}, nil); err != nil {
		t.Fatal(err)
	}
	rotated, err := store.SecretInputsRevision(key)
	if err != nil || rotated == "" || rotated == first || rotated == "second" {
		t.Fatalf("rotated revision=%q err=%v, previous=%q", rotated, err, first)
	}
}
