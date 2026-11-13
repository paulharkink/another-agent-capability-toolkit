package tui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/vault"
)

func TestVaultUnlockModelOpensExistingKeePassAndRetriesWrongPassword(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.kdbx")
	if _, err := vault.Open(path, []byte("correct fixture password")); err != nil {
		t.Fatal(err)
	}
	attempts := 0
	model := newVaultUnlockModel(false, func(password []byte) error {
		attempts++
		_, err := vault.Open(path, password)
		return err
	})

	model = typePassword(model, "wrong fixture password")
	if strings.Contains(model.View().Content, "wrong fixture password") || !strings.Contains(model.View().Content, "••••••••••••••••••••") {
		t.Fatalf("password was not masked: %s", model.View().Content)
	}
	model, result := submitVaultPassword(model)
	if result == nil {
		t.Fatal("existing vault submission did not start unlock")
	}
	model = applyVaultUnlockResult(model, result())
	if model.done || model.message == "" || string(model.password) != "" {
		t.Fatalf("wrong password did not return to a cleared retry state: %+v", model)
	}
	if strings.Contains(model.View().Content, "wrong fixture password") {
		t.Fatalf("failed password was displayed: %s", model.View().Content)
	}

	model = typePassword(model, "correct fixture password")
	model, result = submitVaultPassword(model)
	model = applyVaultUnlockResult(model, result())
	if !model.done || attempts != 2 {
		t.Fatalf("correct retry did not unlock existing vault: done=%v attempts=%d message=%q", model.done, attempts, model.message)
	}
}

func TestVaultUnlockModelCreatesKeePassAfterMatchingConfirmation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.kdbx")
	model := newVaultUnlockModel(true, func(password []byte) error {
		_, err := vault.Open(path, password)
		return err
	})
	model = typePassword(model, "new fixture password")
	model, result := submitVaultPassword(model)
	if result != nil || !model.confirming {
		t.Fatalf("new vault skipped confirmation: confirming=%v cmd=%v", model.confirming, result)
	}
	model = typePassword(model, "different fixture password")
	model, result = submitVaultPassword(model)
	if result != nil || model.confirming || model.message == "" {
		t.Fatalf("mismatched confirmation did not return to password entry: confirming=%v cmd=%v message=%q", model.confirming, result, model.message)
	}
	if _, err := filepath.Glob(path); err != nil {
		t.Fatal(err)
	} else if matches, _ := filepath.Glob(path); len(matches) != 0 {
		t.Fatal("vault was created before confirmation matched")
	}
	model = typePassword(model, "new fixture password")
	model, result = submitVaultPassword(model)
	if result != nil || !model.confirming {
		t.Fatalf("second creation attempt did not request confirmation: %v", result)
	}
	model = typePassword(model, "new fixture password")
	model, result = submitVaultPassword(model)
	if result == nil {
		t.Fatal("matching confirmation did not start vault creation")
	}
	model = applyVaultUnlockResult(model, result())
	if !model.done {
		t.Fatalf("confirmed vault was not created: %q", model.message)
	}
	if _, err := vault.Open(path, []byte("new fixture password")); err != nil {
		t.Fatalf("created KDBX could not be reopened: %v", err)
	}
}

func TestVaultUnlockModelCancelClearsPasswordWithoutUnlocking(t *testing.T) {
	called := false
	model := newVaultUnlockModel(false, func([]byte) error {
		called = true
		return nil
	})
	model = typePassword(model, "cancelled fixture password")
	updated, _ := model.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	model = updated.(*vaultUnlockModel)
	if !model.cancelled || called || strings.Contains(model.View().Content, "cancelled fixture password") {
		t.Fatalf("cancel did not discard password safely: cancelled=%v called=%v view=%q", model.cancelled, called, model.View().Content)
	}
}

func typePassword(model *vaultUnlockModel, password string) *vaultUnlockModel {
	for _, r := range password {
		updated, _ := model.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		model = updated.(*vaultUnlockModel)
	}
	return model
}

func submitVaultPassword(model *vaultUnlockModel) (*vaultUnlockModel, tea.Cmd) {
	updated, cmd := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	return updated.(*vaultUnlockModel), cmd
}

func applyVaultUnlockResult(model *vaultUnlockModel, msg tea.Msg) *vaultUnlockModel {
	updated, _ := model.Update(msg)
	return updated.(*vaultUnlockModel)
}
