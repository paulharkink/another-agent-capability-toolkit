package integration

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/cli"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/vault"
)

// These tests enter through the exported CLI and use an isolated state directory
// and fake connector process. They intentionally do not manufacture daemon APIs
// or private authorization metadata.
func TestPublicCLIRejectsStaleCredentialWithoutLaunchingInteractiveUI(t *testing.T) {
	const actual = "isolated-vault-password"
	prepareCredentialCLITest(t, actual)
	writeCredentialHookResponse(t, `{"status":"ok","secret":"`+base64.StdEncoding.EncodeToString([]byte("stale-password"))+`"}`)

	var out, errOut strings.Builder
	code := cli.Run(context.Background(), nil, os.Stdin, &out, &errOut)
	if code != 2 || !strings.Contains(errOut.String(), "interactive terminal") {
		t.Fatalf("stale saved password did not stop the nonterminal CLI: code=%d stdout=%q stderr=%q", code, out.String(), errOut.String())
	}
	if strings.Contains(out.String()+errOut.String(), actual) || strings.Contains(out.String()+errOut.String(), "stale-password") {
		t.Fatal("a vault password appeared in CLI output")
	}
	if strings.Contains(out.String(), "Unlock AACT shared vault") {
		t.Fatal("CLI launched the interactive unlock UI after rejecting the stale password")
	}
}

func TestPublicCLIUsesManualFallbackWhenCredentialHookUnavailable(t *testing.T) {
	prepareCredentialCLITest(t, "isolated-vault-password")
	writeCredentialHookResponse(t, `{"status":"unavailable"}`)

	var out, errOut strings.Builder
	code := cli.Run(context.Background(), nil, os.Stdin, &out, &errOut)
	if code != 2 || !strings.Contains(errOut.String(), "interactive terminal") {
		t.Fatalf("unavailable hook did not use the safe manual fallback: code=%d stdout=%q stderr=%q", code, out.String(), errOut.String())
	}
	if strings.Contains(out.String(), "Unlock AACT shared vault") {
		t.Fatal("nonterminal CLI launched an interactive unlock UI")
	}
}

func prepareCredentialCLITest(t *testing.T, password string) string {
	t.Helper()
	home := t.TempDir()
	root := filepath.Join(home, "state")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("LOCALAPPDATA", home)
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "xdg"))
	t.Setenv("AACT_STATE_DIR", root)
	t.Setenv("AACT_VAULT_CREDENTIAL_HOOK", filepath.Join(home, "hook"))
	unsetEnv(t, "AACT_VAULT_PASSWORD")
	unsetEnv(t, "AACT_VAULT_PASSWORD_FILE")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := vault.Open(filepath.Join(root, "credentials.kdbx"), []byte(password)); err != nil {
		t.Fatal(err)
	}
	return root
}

func writeCredentialHookResponse(t *testing.T, response string) {
	t.Helper()
	path := os.Getenv("AACT_VAULT_CREDENTIAL_HOOK")
	if runtime.GOOS == "windows" {
		script := "$null = [Console]::In.ReadToEnd(); [Console]::Out.WriteLine('" + strings.ReplaceAll(response, "'", "''") + "')\n"
		if err := os.WriteFile(path+".ps1", []byte(script), 0600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("AACT_VAULT_CREDENTIAL_HOOK", path+".ps1")
		return
	}
	script := fmt.Sprintf("#!/bin/sh\ncat >/dev/null\nprintf '%%s\\n' '%s'\n", strings.ReplaceAll(response, "'", "'\\''"))
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
}

func unsetEnv(t *testing.T, name string) {
	t.Helper()
	value, ok := os.LookupEnv(name)
	if err := os.Unsetenv(name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if ok {
			_ = os.Setenv(name, value)
		} else {
			_ = os.Unsetenv(name)
		}
	})
}
