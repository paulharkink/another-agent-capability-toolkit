package cli

import (
	"bytes"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/vault"
)

type passwordReader func(prompt string) ([]byte, error)

const (
	vaultPasswordEnv     = "AACT_VAULT_PASSWORD"
	vaultPasswordFileEnv = "AACT_VAULT_PASSWORD_FILE"
	maxVaultPasswordFile = 1 << 20
)

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
	store, password, err := openVaultWithPassword(stateRoot, out, read)
	clear(password)
	return store, err
}

func openVaultWithPassword(stateRoot string, out io.Writer, read passwordReader) (*vault.Store, []byte, error) {
	path := filepath.Join(stateRoot, "credentials.kdbx")
	_, statErr := os.Stat(path)
	creating := errors.Is(statErr, os.ErrNotExist)
	if statErr != nil && !creating {
		return nil, nil, fmt.Errorf("inspect shared vault: %w", statErr)
	}
	password, configured, err := configuredVaultPassword()
	if err != nil {
		return nil, nil, err
	}
	label := "Vault master password: "
	if creating {
		label = "Enter new vault master password: "
	}
	if !configured {
		if read == nil {
			return nil, nil, errors.New("vault master password was not configured and cannot be prompted for in this command")
		}
		if _, err := fmt.Fprint(out, label); err != nil {
			return nil, nil, err
		}
		password, err = read(label)
		if err != nil {
			return nil, nil, fmt.Errorf("read vault master password: %w", err)
		}
	}
	if len(password) == 0 {
		clear(password)
		return nil, nil, errors.New("vault master password must not be empty")
	}
	if creating && !configured {
		if _, err := fmt.Fprint(out, "Confirm vault master password: "); err != nil {
			clear(password)
			return nil, nil, err
		}
		confirm, err := read("Confirm vault master password: ")
		if err != nil {
			clear(password)
			return nil, nil, fmt.Errorf("confirm vault master password: %w", err)
		}
		matches := subtle.ConstantTimeCompare(password, confirm) == 1
		clear(confirm)
		if !matches {
			clear(password)
			return nil, nil, errors.New("vault master passwords do not match")
		}
	}
	store, err := vault.Open(path, password)
	if err != nil {
		clear(password)
		return nil, nil, err
	}
	return store, password, nil
}

func configuredVaultPassword() ([]byte, bool, error) {
	password, hasPassword := os.LookupEnv(vaultPasswordEnv)
	passwordFile, hasPasswordFile := os.LookupEnv(vaultPasswordFileEnv)
	if hasPassword && hasPasswordFile {
		return nil, false, fmt.Errorf("configure only one of %s and %s", vaultPasswordEnv, vaultPasswordFileEnv)
	}
	if hasPassword {
		if password == "" {
			return nil, true, fmt.Errorf("%s must not be empty", vaultPasswordEnv)
		}
		return []byte(password), true, nil
	}
	if hasPasswordFile {
		if passwordFile == "" {
			return nil, true, fmt.Errorf("%s must not be empty", vaultPasswordFileEnv)
		}
		file, err := os.Open(passwordFile)
		if err != nil {
			return nil, true, fmt.Errorf("open vault master password file: %w", err)
		}
		defer file.Close()
		password, err := io.ReadAll(io.LimitReader(file, maxVaultPasswordFile+1))
		if err != nil {
			clear(password)
			return nil, true, fmt.Errorf("read vault master password file: %w", err)
		}
		if len(password) > maxVaultPasswordFile {
			clear(password)
			return nil, true, fmt.Errorf("vault master password file exceeds %d bytes", maxVaultPasswordFile)
		}
		original := password
		strippedNewline := false
		if bytes.HasSuffix(password, []byte("\r\n")) {
			password = append([]byte(nil), password[:len(password)-2]...)
			strippedNewline = true
		} else if bytes.HasSuffix(password, []byte("\n")) {
			password = append([]byte(nil), password[:len(password)-1]...)
			strippedNewline = true
		}
		if strippedNewline {
			clear(original)
		}
		if len(password) == 0 {
			return nil, true, errors.New("vault master password file is empty")
		}
		return password, true, nil
	}
	return nil, false, nil
}

func hasConfiguredVaultPassword() bool {
	_, hasPassword := os.LookupEnv(vaultPasswordEnv)
	_, hasPasswordFile := os.LookupEnv(vaultPasswordFileEnv)
	return hasPassword || hasPasswordFile
}

// initializeSharedVault prompts without echo, then attaches the encrypted
// backend used by state.SecretInputs and state.SaveSecretInputs.
func initializeSharedVault(store *state.Store, stateRoot string, out io.Writer, read passwordReader) error {
	vaultStore, masterPassword, err := openVaultWithPassword(stateRoot, out, read)
	if err != nil {
		return err
	}
	defer clear(masterPassword)
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
