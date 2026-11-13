package cli

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/vault"
)

type passwordReader func(prompt string) ([]byte, error)

func needsSharedVault(args []string) bool {
	if len(args) == 0 {
		return true
	}
	switch args[0] {
	case "install", "uninstall":
		return true
	case "mcp":
		return len(args) > 1 && args[1] != "list" && args[1] != "status"
	default:
		return false
	}
}

func openVaultWithPrompt(stateRoot string, out io.Writer, read passwordReader) (*vault.Store, error) {
	path := filepath.Join(stateRoot, "credentials.kdbx")
	_, statErr := os.Stat(path)
	creating := errors.Is(statErr, os.ErrNotExist)
	if statErr != nil && !creating {
		return nil, fmt.Errorf("inspect shared vault: %w", statErr)
	}
	label := "Vault master password: "
	if creating {
		label = "Enter new vault master password: "
	}
	if _, err := fmt.Fprint(out, label); err != nil {
		return nil, err
	}
	password, err := read(label)
	if err != nil {
		return nil, fmt.Errorf("read vault master password: %w", err)
	}
	defer clear(password)
	if len(password) == 0 {
		return nil, errors.New("vault master password must not be empty")
	}
	if creating {
		if _, err := fmt.Fprint(out, "Confirm vault master password: "); err != nil {
			return nil, err
		}
		confirm, err := read("Confirm vault master password: ")
		if err != nil {
			return nil, fmt.Errorf("confirm vault master password: %w", err)
		}
		matches := subtle.ConstantTimeCompare(password, confirm) == 1
		clear(confirm)
		if !matches {
			return nil, errors.New("vault master passwords do not match")
		}
	}
	store, err := vault.Open(path, password)
	if err != nil {
		return nil, err
	}
	return store, nil
}

// initializeSharedVault prompts without echo, then attaches the encrypted
// backend used by state.SecretInputs and state.SaveSecretInputs.
func initializeSharedVault(store *state.Store, stateRoot string, out io.Writer, read passwordReader) error {
	var masterPassword []byte
	wrappedRead := func(prompt string) ([]byte, error) {
		password, err := read(prompt)
		if err == nil && prompt != "Confirm vault master password: " {
			clear(masterPassword)
			masterPassword = append([]byte(nil), password...)
		}
		return password, err
	}
	defer func() { clear(masterPassword) }()
	vaultStore, err := openVaultWithPrompt(stateRoot, out, wrappedRead)
	if err != nil {
		return err
	}
	return attachSharedVault(store, stateRoot, vaultStore, masterPassword)
}

func initializeSharedVaultWithPassword(store *state.Store, stateRoot string, password []byte) error {
	if len(password) == 0 {
		return errors.New("vault master password must not be empty")
	}
	vaultStore, err := vault.Open(filepath.Join(stateRoot, "credentials.kdbx"), password)
	if err != nil {
		return err
	}
	return attachSharedVault(store, stateRoot, vaultStore, password)
}

func attachSharedVault(store *state.Store, stateRoot string, vaultStore *vault.Store, masterPassword []byte) error {
	store.SetSecretBackend(vaultStore)
	if err := store.MigrateLegacySecretInputs(); err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	info, err := vault.StartDetached(executable, stateRoot, masterPassword)
	if err != nil {
		return err
	}
	controlToken, err := vault.ControlTokenForVault(filepath.Join(stateRoot, "credentials.kdbx"), masterPassword)
	if err != nil {
		return err
	}
	defer clear(controlToken)
	vaultStore.ConfigureDetachedDaemon(info, controlToken)
	return nil
}
