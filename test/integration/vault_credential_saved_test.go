package integration

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/vault"
)

func TestPublicCLIStopsDaemonWithSavedCredential(t *testing.T) {
	const password = "isolated-valid-keyring-password"
	binary := buildPublicCLIBinary(t)
	root := prepareCredentialCLITest(t, password)
	t.Setenv("AACT_VAULT_API_BIND_IP", "127.0.0.1")
	trace := filepath.Join(t.TempDir(), "hook-trace")
	t.Setenv("AACT_TEST_HOOK_TRACE", trace)
	hookPath := writeSavedCredentialHook(t, password)
	t.Setenv("AACT_VAULT_CREDENTIAL_HOOK", hookPath)

	info, err := vault.StartDetached(binary, root, []byte(password))
	if err != nil {
		t.Fatalf("start isolated daemon: %v", err)
	}
	t.Cleanup(func() {
		token, tokenErr := vault.ControlTokenForVault(filepath.Join(root, "credentials.kdbx"), []byte(password))
		if tokenErr == nil {
			defer clear(token)
			_ = vault.StopDetached(info, token)
		}
	})

	command := exec.Command(binary, "daemon", "stop")
	command.Env = os.Environ()
	output, err := command.CombinedOutput()
	if err != nil {
		marker, _ := os.ReadFile(trace)
		t.Fatalf("public daemon stop failed using the saved credential: %v; output=%q hook-trace=%q", err, output, marker)
	}
	if !strings.Contains(string(output), "AACT daemon stopped") {
		t.Fatalf("CLI did not report daemon stop: %q", output)
	}
	if bytes.Contains(output, []byte(password)) {
		t.Fatal("Vault password appeared in CLI output")
	}
	if marker, err := os.ReadFile(trace); err != nil || !strings.Contains(strings.ReplaceAll(string(marker), "\r\n", "\n"), "get\n") {
		t.Fatalf("credential hook was not used by public CLI: trace=%q err=%v", marker, err)
	}
	if vault.DaemonRunning(info) {
		t.Fatal("public daemon stop left the isolated daemon running")
	}
}

func writeSavedCredentialHook(t *testing.T, password string) string {
	t.Helper()
	response := `{"status":"ok","secret":"` + base64.StdEncoding.EncodeToString([]byte(password)) + `"}`
	base := filepath.Join(filepath.Dir(os.Getenv("AACT_VAULT_CREDENTIAL_HOOK")), "hook")
	if runtime.GOOS == "windows" {
		path := base + " with spaces.cmd"
		script := "@echo off\r\nset /p \"request=\"\r\n>>\"%AACT_TEST_HOOK_TRACE%\" echo get\r\necho " + response + "\r\n"
		if err := os.WriteFile(path, []byte(script), 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	path := base + ".sh"
	script := fmt.Sprintf("#!/bin/sh\nrequest=$(cat)\nprintf 'get\\n' >> %q\nprintf '%%s\\n' '%s'\n", os.Getenv("AACT_TEST_HOOK_TRACE"), response)
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}
