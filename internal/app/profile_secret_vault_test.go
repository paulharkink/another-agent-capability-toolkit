package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/vault"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

func TestApplyProfilePersistsSecretInputInKeePassAndReloadsIt(t *testing.T) {
	s, _, q := profileApplyFixture(t)
	p := &s.Source.Catalog[0]
	if err := os.WriteFile(filepath.Join(s.Source.ProfileRoot, "demo", "ota.toml"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	p.MCPs = nil
	p.Inputs = []catalog.Input{{Name: "credential", Type: "secret"}}
	p.Sets = []catalog.ComponentSet{{Name: "core", Skills: []string{"one"}}}
	q.ItemIDs = []string{"set:core"}
	q.Inputs = map[string]any{"credential": "fixture-secret-value"}

	key, err := s.Store.ResolveProfileKey(q.Ref.PackID, q.Ref.CapabilityID, q.Ref.Name)
	if err != nil {
		t.Fatal(err)
	}
	vaultPath := filepath.Join(t.TempDir(), "vault.kdbx")
	password := []byte("fixture-master-password")
	firstVault, err := vault.Open(vaultPath, password)
	if err != nil {
		t.Fatal(err)
	}
	s.Store.SetSecretBackend(firstVault)

	uiRequest := viewmodel.SetupInstallRequest{Ref: q.Ref, Inputs: q.Inputs, ItemIDs: q.ItemIDs, DestinationIDs: q.DestinationIDs}
	out, err := s.UIInstall(context.Background(), uiRequest)
	if err != nil {
		t.Fatalf("apply profile: out=%+v err=%v", out, err)
	}
	answers, err := s.Store.Answers(key)
	if err != nil {
		t.Fatal(err)
	}
	if _, leaked := answers["credential"]; leaked {
		t.Fatalf("secret appeared in ordinary answers: %#v", answers)
	}

	// Reopen both the application state and KeePass database as a fresh service
	// would after a restart. The profile resolution path must restore the value.
	freshVault, err := vault.Open(vaultPath, password)
	if err != nil {
		t.Fatal(err)
	}
	freshState, err := state.Open(s.Store.Root())
	if err != nil {
		t.Fatal(err)
	}
	freshState.SetSecretBackend(freshVault)
	freshService := New(s.Source, freshState, s.Options)
	freshPackage, freshProfile, freshKey, err := freshService.loadProfile(q.Ref)
	if err != nil {
		t.Fatal(err)
	}
	_, _, values, _, err := freshService.profileValues(freshPackage, freshProfile, freshKey, ProfileRequest{Ref: q.Ref})
	if err != nil {
		t.Fatalf("reload profile values: %v", err)
	}
	if values["credential"] != "fixture-secret-value" {
		t.Fatalf("reopened profile credential = %#v, want restored secret", values["credential"])
	}
	secretValues, err := freshState.SecretInputs(key)
	if err != nil {
		t.Fatal(err)
	}
	if secretValues["credential"] != "fixture-secret-value" {
		t.Fatalf("KeePass secret = %#v, want submitted value", secretValues["credential"])
	}
	// A blank UI field means keep the existing credential for this apply.
	uiRequest.Inputs = map[string]any{"credential": ""}
	if _, err := freshService.UIInstall(context.Background(), uiRequest); err != nil {
		t.Fatalf("reapply with blank credential field: %v", err)
	}
	secretValues, err = freshState.SecretInputs(key)
	if err != nil || secretValues["credential"] != "fixture-secret-value" {
		t.Fatalf("blank UI secret was not preserved: values=%#v err=%v", secretValues, err)
	}
	uiRequest.Inputs = nil
	if _, err := freshService.UIInstall(context.Background(), uiRequest); err != nil {
		t.Fatalf("reapply with omitted credential: %v", err)
	}
	uiRequest.ResetInputs = []string{"credential"}
	if _, err := freshService.UIInstall(context.Background(), uiRequest); err != nil {
		t.Fatalf("reset credential through UI: %v", err)
	}
	secretValues, err = freshState.SecretInputs(key)
	if err != nil || len(secretValues) != 0 {
		t.Fatalf("UI reset did not remove vault credential: values=%#v err=%v", secretValues, err)
	}
	if _, err := os.Stat(filepath.Join(freshState.AuthDir(key), "aact-private-inputs.json")); !os.IsNotExist(err) {
		t.Fatalf("private plaintext file still exists: err=%v", err)
	}
}

type failingAppSecretBackend struct{ err error }

func (b failingAppSecretBackend) Get(string, string) ([]byte, bool, error) { return nil, false, nil }
func (b failingAppSecretBackend) Put(string, string, []byte) error         { return b.err }
func (b failingAppSecretBackend) Delete(string, string) error              { return b.err }

type profileRecordFaultBackend struct {
	values      map[string][]byte
	profilePath string
	triggered   bool
}

func (b *profileRecordFaultBackend) Get(namespace, name string) ([]byte, bool, error) {
	value, ok := b.values[namespace+"/"+name]
	return append([]byte(nil), value...), ok, nil
}
func (b *profileRecordFaultBackend) Put(namespace, name string, value []byte) error {
	b.values[namespace+"/"+name] = append([]byte(nil), value...)
	if !b.triggered {
		b.triggered = true
		return os.MkdirAll(b.profilePath, 0700)
	}
	return nil
}
func (b *profileRecordFaultBackend) Delete(namespace, name string) error {
	delete(b.values, namespace+"/"+name)
	return nil
}

func TestApplyProfileFailsClosedWithoutVault(t *testing.T) {
	s, _, q := profileApplyFixture(t)
	if err := os.WriteFile(filepath.Join(s.Source.ProfileRoot, "demo", "ota.toml"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	p := &s.Source.Catalog[0]
	p.Inputs = []catalog.Input{{Name: "credential", Type: "secret"}}
	p.Sets = []catalog.ComponentSet{{Name: "core", Skills: []string{"one"}}}
	q.ItemIDs = []string{"set:core"}
	q.Inputs = map[string]any{"credential": "fixture-secret-value"}
	s.Store.SetSecretBackend(nil)
	if _, err := s.ApplyProfile(context.Background(), q); err == nil || !strings.Contains(err.Error(), "KeePass vault") {
		t.Fatalf("secret submission without KeePass backend was not rejected: %v", err)
	}
}

func TestApplyProfileVaultFailureLeavesOrdinaryAnswersUntouched(t *testing.T) {
	s, _, q := profileApplyFixture(t)
	p := &s.Source.Catalog[0]
	p.MCP = nil
	p.MCPs = nil
	p.Inputs = []catalog.Input{{Name: "credential", Type: "secret"}, {Name: "label", Type: "string"}}
	p.Sets = []catalog.ComponentSet{{Name: "core", Skills: []string{"one"}}}
	q.ItemIDs = []string{"set:core"}
	q.Inputs = map[string]any{"credential": "fixture-secret-value", "label": "new-label"}
	key, err := s.Store.ResolveProfileKey(q.Ref.PackID, q.Ref.CapabilityID, q.Ref.Name)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Store.SaveAnswers(key, map[string]any{"label": "previous-label"}); err != nil {
		t.Fatal(err)
	}
	s.Store.SetSecretBackend(failingAppSecretBackend{err: errors.New("vault write failed")})
	if _, err := s.ApplyProfile(context.Background(), q); err == nil || !strings.Contains(err.Error(), "vault write failed") {
		t.Fatalf("vault write failure was not returned: %v", err)
	}
	answers, err := s.Store.Answers(key)
	if err != nil || answers["label"] != "previous-label" {
		t.Fatalf("ordinary answers changed after vault write failure: %#v err=%v", answers, err)
	}
}

func TestApplyProfileRecordFailureRollsBackAnswersAndVault(t *testing.T) {
	s, _, q := profileApplyFixture(t)
	if err := os.WriteFile(filepath.Join(s.Source.ProfileRoot, "demo", "ota.toml"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	p := &s.Source.Catalog[0]
	p.MCP = nil
	p.MCPs = nil
	p.Inputs = []catalog.Input{{Name: "credential", Type: "secret"}, {Name: "label", Type: "string"}}
	p.Sets = []catalog.ComponentSet{{Name: "core", Skills: []string{"one"}}}
	q.ItemIDs = []string{"set:core"}
	q.Inputs = map[string]any{"credential": "fixture-secret-value", "label": "new-label"}
	key, err := s.Store.ResolveProfileKey(q.Ref.PackID, q.Ref.CapabilityID, q.Ref.Name)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Store.SaveAnswers(key, map[string]any{"label": "previous-label"}); err != nil {
		t.Fatal(err)
	}
	backend := &profileRecordFaultBackend{values: map[string][]byte{}, profilePath: filepath.Join(s.Store.Root(), "manager", "profiles.json")}
	s.Store.SetSecretBackend(backend)
	if _, err := s.ApplyProfile(context.Background(), q); err == nil {
		t.Fatal("expected profile record failure")
	}
	answers, err := s.Store.Answers(key)
	if err != nil || answers["label"] != "previous-label" {
		t.Fatalf("ordinary answers changed after profile record failure: %#v err=%v", answers, err)
	}
	secrets, err := s.Store.SecretInputs(key)
	if err != nil || len(secrets) != 0 {
		t.Fatalf("vault secret remained after profile record failure: %#v err=%v", secrets, err)
	}
}
