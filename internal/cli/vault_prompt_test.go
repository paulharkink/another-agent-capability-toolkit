package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

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
