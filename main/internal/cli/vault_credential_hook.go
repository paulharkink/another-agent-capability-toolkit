package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/process"
)

const (
	vaultCredentialService = "AACT shared Vault"
	maxCredentialHookData  = 2 << 20
)

type credentialHookRequest struct {
	Version int    `json:"version"`
	Action  string `json:"action"`
	Service string `json:"service"`
	Account string `json:"account"`
	Secret  []byte `json:"secret,omitempty"`
}

type credentialHookResponse struct {
	Status string `json:"status"`
	Secret []byte `json:"secret,omitempty"`
}

var errCredentialMissing = errors.New("vault credential is missing")
var errCredentialUnavailable = errors.New("vault credential store is unavailable")

type credentialOutput struct {
	bytes.Buffer
	limit    int
	overflow bool
}

func (b *credentialOutput) Write(data []byte) (int, error) {
	original := len(data)
	remaining := b.limit + 1 - b.Len()
	if remaining <= 0 {
		b.overflow = true
		return original, nil
	}
	if len(data) > remaining {
		data = data[:remaining]
		b.overflow = true
	}
	_, _ = b.Buffer.Write(data)
	return original, nil
}

func vaultCredentialAccount(stateRoot string) (string, error) {
	root, err := filepath.Abs(stateRoot)
	if err != nil {
		return "", fmt.Errorf("resolve vault credential scope: %w", err)
	}
	sum := sha256.Sum256([]byte(filepath.Clean(root)))
	return "state-" + hex.EncodeToString(sum[:]), nil
}

func credentialHookPath() (string, error) {
	if configured := os.Getenv("AACT_VAULT_CREDENTIAL_HOOK"); configured != "" {
		if !filepath.IsAbs(configured) {
			return "", errors.New("AACT_VAULT_CREDENTIAL_HOOK must be an absolute path")
		}
		return configured, nil
	}
	name := "linux-secret-service.sh"
	switch runtime.GOOS {
	case "darwin":
		name = "macos.sh"
	case "windows":
		name = "windows.ps1"
	case "linux":
		if isWSL() {
			name = "wsl.sh"
		}
	default:
		return "", errCredentialUnavailable
	}
	if executable, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(filepath.Dir(executable)), "scripts", "keyring", name)
		if info, statErr := os.Stat(candidate); statErr == nil && info.Mode().IsRegular() {
			return candidate, nil
		}
	}
	if bundle := bundledRoot(); bundle != "" {
		candidate := filepath.Join(filepath.Dir(bundle), "scripts", "keyring", name)
		if info, statErr := os.Stat(candidate); statErr == nil && info.Mode().IsRegular() {
			return candidate, nil
		}
	}
	return "", errCredentialUnavailable
}

func isWSL() bool {
	if os.Getenv("WSL_INTEROP") != "" || os.Getenv("WSL_DISTRO_NAME") != "" {
		return true
	}
	data, err := os.ReadFile("/proc/sys/kernel/osrelease")
	return err == nil && strings.Contains(strings.ToLower(string(data)), "microsoft")
}

func getVaultCredential(stateRoot string) ([]byte, error) {
	account, err := vaultCredentialAccount(stateRoot)
	if err != nil {
		return nil, err
	}
	response, err := runCredentialHook(credentialHookRequest{Version: 1, Action: "get", Service: vaultCredentialService, Account: account})
	if err != nil {
		return nil, err
	}
	switch response.Status {
	case "ok":
		if len(response.Secret) == 0 {
			return nil, errCredentialMissing
		}
		return response.Secret, nil
	case "missing":
		return nil, errCredentialMissing
	default:
		return nil, errCredentialUnavailable
	}
}

func setVaultCredential(stateRoot string, secret []byte) error {
	if len(secret) == 0 {
		return errors.New("vault credential must not be empty")
	}
	account, err := vaultCredentialAccount(stateRoot)
	if err != nil {
		return err
	}
	copyOfSecret := append([]byte(nil), secret...)
	defer clear(copyOfSecret)
	response, err := runCredentialHook(credentialHookRequest{Version: 1, Action: "set", Service: vaultCredentialService, Account: account, Secret: copyOfSecret})
	if err != nil || response.Status != "ok" {
		return errCredentialUnavailable
	}
	return nil
}

func runCredentialHook(request credentialHookRequest) (credentialHookResponse, error) {
	path, err := credentialHookPath()
	if err != nil {
		return credentialHookResponse{}, errCredentialUnavailable
	}
	input, err := json.Marshal(request)
	if err != nil {
		return credentialHookResponse{}, errCredentialUnavailable
	}
	if len(input) > maxCredentialHookData {
		clear(input)
		return credentialHookResponse{}, errCredentialUnavailable
	}
	defer clear(input)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var command *exec.Cmd
	if runtime.GOOS == "windows" {
		if !filepath.IsAbs(path) {
			return credentialHookResponse{}, errCredentialUnavailable
		}
		switch strings.ToLower(filepath.Ext(path)) {
		case ".ps1":
			powershell := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
			if !filepath.IsAbs(powershell) {
				return credentialHookResponse{}, errCredentialUnavailable
			}
			command = exec.CommandContext(ctx, powershell, "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", path)
		case ".cmd", ".bat":
			cmd := filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
			if !filepath.IsAbs(cmd) || strings.ContainsAny(path, "\"\r\n&|^()<>%!") {
				return credentialHookResponse{}, errCredentialUnavailable
			}
			// Go quotes the path as a single argument. Avoid /s, which changes
			// cmd.exe's /c quote handling for paths containing spaces. The hook
			// payload remains on stdin, never in this command line.
			command = exec.CommandContext(ctx, cmd, "/d", "/c", path)
		default:
			command = exec.CommandContext(ctx, path)
		}
	} else {
		if !filepath.IsAbs(path) {
			return credentialHookResponse{}, errCredentialUnavailable
		}
		command = exec.CommandContext(ctx, path)
	}
	command.Env = process.EnvironmentWithoutVaultPassword(os.Environ())
	command.Stdin = bytes.NewReader(input)
	stdout := &credentialOutput{limit: maxCredentialHookData}
	defer func() { clear(stdout.Bytes()) }()
	command.Stdout = stdout
	command.Stderr = io.Discard
	if err := command.Run(); err != nil || stdout.overflow || stdout.Len() > maxCredentialHookData {
		return credentialHookResponse{}, errCredentialUnavailable
	}
	decoder := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	decoder.DisallowUnknownFields()
	var response credentialHookResponse
	if err := decoder.Decode(&response); err != nil {
		return credentialHookResponse{}, errCredentialUnavailable
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return credentialHookResponse{}, errCredentialUnavailable
	}
	if response.Status != "ok" && response.Status != "missing" && response.Status != "unavailable" {
		return credentialHookResponse{}, errCredentialUnavailable
	}
	return response, nil
}
