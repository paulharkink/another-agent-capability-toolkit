package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/vault"
)

func TestPublicCLIKeyringEnrollmentChoice(t *testing.T) {
	if !credentialPTYSupported() {
		t.Skip("public terminal interaction requires a Unix PTY")
	}
	binary := buildPublicCLIBinary(t)
	for _, tc := range []struct {
		name       string
		enroll     bool
		writeState string
		wantSet    bool
		wantWarn   bool
	}{
		{name: "decline", enroll: false, writeState: "ok", wantSet: false},
		{name: "opt in", enroll: true, writeState: "ok", wantSet: true},
		{name: "failed write keeps unlock", enroll: true, writeState: "unavailable", wantSet: true, wantWarn: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := prepareCredentialCLITest(t, "terminal-only-test-password")
			vaultPath := filepath.Join(root, "credentials.kdbx")
			seed, err := vault.Open(vaultPath, []byte("terminal-only-test-password"))
			if err != nil {
				t.Fatalf("open isolated Vault for a sentinel entry: %v", err)
			}
			if err := seed.Put(strings.Repeat("a", 64), "sentinel", []byte("preserve-this-value")); err != nil {
				t.Fatalf("seed existing Vault secret: %v", err)
			}
			trace := filepath.Join(t.TempDir(), "hook-trace")
			t.Setenv("AACT_TEST_HOOK_TRACE", trace)
			t.Setenv("AACT_TEST_HOOK_SET_STATUS", tc.writeState)
			writeCredentialTraceHook(t, os.Getenv("AACT_VAULT_CREDENTIAL_HOOK"))
			t.Setenv("AACT_VAULT_API_BIND_IP", "127.0.0.1")

			code, errText := runPublicCLIOnPTY(t, binary, tc.enroll)
			if code != 0 {
				t.Fatalf("CLI failed after valid Vault unlock: code=%d stderr=%q", code, errText)
			}
			traceBytes, err := os.ReadFile(trace)
			if err != nil && tc.wantSet {
				t.Fatalf("hook trace was not written: %v", err)
			}
			sawSet := strings.Contains(string(traceBytes), "set\n")
			if sawSet != tc.wantSet {
				t.Fatalf("credential save request mismatch: got set=%v trace=%q", sawSet, traceBytes)
			}
			if strings.Contains(errText, "terminal-only-test-password") {
				t.Fatal("Vault password appeared in CLI diagnostics")
			}
			if strings.Contains(errText, "Could not save the vault password") != tc.wantWarn {
				t.Fatalf("credential save warning mismatch: stderr=%q", errText)
			}
			unlocked, err := vault.Open(vaultPath, []byte("terminal-only-test-password"))
			if err != nil {
				t.Fatalf("Vault was not left usable after enrollment choice: %v", err)
			}
			secret, found, err := unlocked.Get(strings.Repeat("a", 64), "sentinel")
			if err != nil {
				t.Fatalf("read existing Vault secret: %v", err)
			}
			if !found || string(secret) != "preserve-this-value" {
				t.Fatalf("existing Vault secret changed during enrollment: found=%v value=%q", found, secret)
			}
			clear(secret)
			stopIsolatedVaultDaemon(t, root, []byte("terminal-only-test-password"))
		})
	}
}

func writeCredentialTraceHook(t *testing.T, path string) {
	t.Helper()
	script := `#!/bin/sh
set -eu
request=$(cat)
case "$request" in
  *'"action":"get"'*) printf '{"status":"missing"}\n' ;;
  *'"action":"set"'*)
    printf 'set\n' >> "$AACT_TEST_HOOK_TRACE"
    printf '{"status":"%s"}\n' "$AACT_TEST_HOOK_SET_STATUS"
    ;;
  *) printf '{"status":"unavailable"}\n' ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
}

func stopIsolatedVaultDaemon(t *testing.T, root string, password []byte) {
	t.Helper()
	info, err := vault.ReadDaemonInfo(root)
	if err != nil {
		t.Fatalf("read started daemon metadata: %v", err)
	}
	token, err := vault.ControlTokenForVault(filepath.Join(root, "credentials.kdbx"), password)
	if err != nil {
		t.Fatalf("derive cleanup control token: %v", err)
	}
	defer clear(token)
	if err := vault.StopDetached(info, token); err != nil {
		t.Fatalf("stop isolated daemon: %v", err)
	}
}
