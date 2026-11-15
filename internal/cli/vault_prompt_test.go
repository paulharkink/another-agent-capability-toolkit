package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/vault"
)

func unsetEnv(t *testing.T, name string) {
	t.Helper()
	previous, existed := os.LookupEnv(name)
	if err := os.Unsetenv(name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if existed {
			_ = os.Setenv(name, previous)
		} else {
			_ = os.Unsetenv(name)
		}
	})
}

func TestOpenVaultPromptsTwiceOnlyWhenCreatingAndAttachesBackend(t *testing.T) {
	root := t.TempDir()
	answers := [][]byte{[]byte("test-vault-password"), []byte("test-vault-password")}
	calls := 0
	read := func(string) ([]byte, error) {
		value := answers[calls]
		calls++
		return append([]byte(nil), value...), nil
	}
	var prompt bytes.Buffer
	store, err := openVaultWithPrompt(root, &prompt, read)
	if err != nil {
		t.Fatal(err)
	}
	if store == nil || calls != 2 {
		t.Fatalf("store=%v prompts=%d", store, calls)
	}
	if _, err := os.Stat(filepath.Join(root, "credentials.kdbx")); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(prompt.Bytes(), []byte("Enter new vault master password")) || !bytes.Contains(prompt.Bytes(), []byte("Confirm vault master password")) {
		t.Fatalf("prompts=%q", prompt.String())
	}
}

func TestOpenVaultRejectsMismatchWithoutCreatingFile(t *testing.T) {
	root := t.TempDir()
	answers := [][]byte{[]byte("one"), []byte("two")}
	calls := 0
	read := func(string) ([]byte, error) {
		value := answers[calls]
		calls++
		return append([]byte(nil), value...), nil
	}
	if _, err := openVaultWithPrompt(root, &bytes.Buffer{}, read); err == nil {
		t.Fatal("mismatched master passwords accepted")
	}
	if _, err := os.Stat(filepath.Join(root, "credentials.kdbx")); !os.IsNotExist(err) {
		t.Fatalf("vault created after mismatch: %v", err)
	}
}

func TestOpenVaultUsesConfiguredPasswordFileWithoutPrompt(t *testing.T) {
	root := t.TempDir()
	passwordFile := filepath.Join(t.TempDir(), "master-password")
	if err := os.WriteFile(passwordFile, []byte("file-master-password\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AACT_VAULT_PASSWORD_FILE", passwordFile)
	unsetEnv(t, "AACT_VAULT_PASSWORD")
	var prompts bytes.Buffer
	read := func(prompt string) ([]byte, error) {
		return nil, fmt.Errorf("unexpected prompt: %s", prompt)
	}
	store, err := openVaultWithPrompt(root, &prompts, read)
	if err != nil {
		t.Fatal(err)
	}
	if store == nil {
		t.Fatal("openVaultWithPrompt returned a nil store")
	}
	if prompts.Len() != 0 {
		t.Fatalf("unexpected prompt output: %q", prompts.String())
	}
	opened, err := vault.Open(filepath.Join(root, "credentials.kdbx"), []byte("file-master-password"))
	if err != nil {
		t.Fatalf("open vault with mounted-file password: %v", err)
	}
	if opened == nil {
		t.Fatal("vault.Open returned a nil store")
	}
}

func TestOpenVaultUsesConfiguredPasswordEnvironmentWithoutPrompt(t *testing.T) {
	root := t.TempDir()
	unsetEnv(t, "AACT_VAULT_PASSWORD_FILE")
	t.Setenv("AACT_VAULT_PASSWORD", "environment-master-password")
	var prompts bytes.Buffer
	read := func(prompt string) ([]byte, error) {
		return nil, fmt.Errorf("unexpected prompt: %s", prompt)
	}
	store, err := openVaultWithPrompt(root, &prompts, read)
	if err != nil {
		t.Fatal(err)
	}
	if store == nil {
		t.Fatal("openVaultWithPrompt returned a nil store")
	}
	if prompts.Len() != 0 {
		t.Fatalf("unexpected prompt output: %q", prompts.String())
	}
	opened, err := vault.Open(filepath.Join(root, "credentials.kdbx"), []byte("environment-master-password"))
	if err != nil {
		t.Fatalf("open vault with environment password: %v", err)
	}
	if opened == nil {
		t.Fatal("vault.Open returned a nil store")
	}
}

func TestOpenVaultRejectsConflictingConfiguredPasswordSources(t *testing.T) {
	root := t.TempDir()
	passwordFile := filepath.Join(t.TempDir(), "master-password")
	if err := os.WriteFile(passwordFile, []byte("file-master-password"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AACT_VAULT_PASSWORD_FILE", passwordFile)
	t.Setenv("AACT_VAULT_PASSWORD", "environment-master-password")
	read := func(prompt string) ([]byte, error) {
		return nil, fmt.Errorf("unexpected prompt: %s", prompt)
	}
	if _, err := openVaultWithPrompt(root, &bytes.Buffer{}, read); err == nil || !strings.Contains(err.Error(), "configure only one of AACT_VAULT_PASSWORD and AACT_VAULT_PASSWORD_FILE") {
		t.Fatal("conflicting password sources were accepted")
	}
	if _, err := os.Stat(filepath.Join(root, "credentials.kdbx")); !os.IsNotExist(err) {
		t.Fatalf("vault created with conflicting sources: %v", err)
	}
}
