package integration

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/cli"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/daemon"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/vault"
)

func TestDaemonStopUnlocksExistingVaultThroughCredentialHook(t *testing.T) {
	const password = "isolated-hook-password"
	root, _, control, stopped := credentialHookDaemon(t, password)
	writeCredentialHook(t, password)

	var out, errOut bytes.Buffer
	code := cli.Run(context.Background(), []string{"daemon", "stop"}, bytes.NewReader(nil), &out, &errOut)
	if code != 0 || !strings.Contains(out.String(), "daemon stopped") {
		t.Fatalf("daemon stop failed: code=%d stdout=%q stderr=%q", code, out.String(), errOut.String())
	}
	if !stopped.Load() {
		t.Fatal("credential hook did not authorize the daemon stop request")
	}
	if strings.Contains(out.String()+errOut.String(), password) {
		t.Fatal("vault password appeared in CLI output")
	}
	if _, err := os.Stat(filepath.Join(root, daemon.InfoFilename)); err != nil {
		t.Fatalf("daemon metadata was not preserved: %v", err)
	}
	clear(control)
}

func TestDaemonStopFallsBackWhenCredentialHookReportsMissing(t *testing.T) {
	_, _, _, stopped := credentialHookDaemon(t, "other-isolated-password")
	writeCredentialHookStatus(t, "missing")

	var out, errOut bytes.Buffer
	code := cli.Run(context.Background(), []string{"daemon", "stop"}, bytes.NewReader(nil), &out, &errOut)
	if code != 2 || stopped.Load() || !strings.Contains(errOut.String(), "interactive terminal") {
		t.Fatalf("missing credential did not fall back safely: code=%d stopped=%v stdout=%q stderr=%q", code, stopped.Load(), out.String(), errOut.String())
	}
	if strings.Contains(out.String()+errOut.String(), "other-isolated-password") {
		t.Fatal("test password appeared in CLI output")
	}
}

func TestDaemonStopRejectsStaleCredentialAndFallsBackToPrompt(t *testing.T) {
	_, _, _, stopped := credentialHookDaemon(t, "correct-isolated-password")
	writeCredentialHook(t, "stale-isolated-password")

	var out, errOut bytes.Buffer
	code := cli.Run(context.Background(), []string{"daemon", "stop"}, bytes.NewReader(nil), &out, &errOut)
	if code != 2 || stopped.Load() || !strings.Contains(errOut.String(), "interactive terminal") {
		t.Fatalf("stale credential was not rejected safely: code=%d stopped=%v stdout=%q stderr=%q", code, stopped.Load(), out.String(), errOut.String())
	}
	if strings.Contains(out.String()+errOut.String(), "stale-isolated-password") {
		t.Fatal("stale test password appeared in CLI output")
	}
}

func credentialHookDaemon(t *testing.T, password string) (string, vault.DaemonInfo, []byte, *atomic.Bool) {
	t.Helper()
	home := t.TempDir()
	stateRoot := filepath.Join(home, "state")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("LOCALAPPDATA", home)
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "xdg"))
	t.Setenv("AACT_STATE_DIR", stateRoot)
	unsetEnv(t, "AACT_VAULT_PASSWORD")
	unsetEnv(t, "AACT_VAULT_PASSWORD_FILE")
	if err := os.MkdirAll(stateRoot, 0700); err != nil {
		t.Fatal(err)
	}
	store, err := vault.Open(filepath.Join(stateRoot, "credentials.kdbx"), []byte(password))
	if err != nil {
		t.Fatal(err)
	}
	_ = store
	control, err := vault.ControlTokenForVault(filepath.Join(stateRoot, "credentials.kdbx"), []byte(password))
	if err != nil {
		t.Fatal(err)
	}
	stopped := &atomic.Bool{}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/health":
			if stopped.Load() {
				http.Error(w, "stopped", http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && r.URL.Path == "/shutdown":
			if r.Header.Get("Authorization") != "Bearer "+string(control) {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			stopped.Store(true)
			w.WriteHeader(http.StatusAccepted)
			go func() {
				time.Sleep(10 * time.Millisecond)
				server.Close()
			}()
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	info := vault.DaemonInfo{
		APIAddress:     "https://host.docker.internal:12345",
		APICertificate: "isolated test certificate",
		ControlAddress: server.URL,
		Services:       []string{"vault"},
	}
	metadata, err := json.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateRoot, daemon.InfoFilename), metadata, 0600); err != nil {
		t.Fatal(err)
	}
	return stateRoot, info, control, stopped
}

func writeCredentialHook(t *testing.T, password string) {
	t.Helper()
	encoded := base64.StdEncoding.EncodeToString([]byte(password))
	if runtime.GOOS == "windows" {
		path := filepath.Join(t.TempDir(), "hook.ps1")
		script := fmt.Sprintf("$null = [Console]::In.ReadToEnd(); [Console]::Out.WriteLine('{\"status\":\"ok\",\"secret\":\"%s\"}'); [Console]::Error.WriteLine('credential-hook-private-diagnostic')\n", encoded)
		if err := os.WriteFile(path, []byte(script), 0600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("AACT_VAULT_CREDENTIAL_HOOK", path)
		return
	}
	path := filepath.Join(t.TempDir(), "hook.sh")
	script := fmt.Sprintf("#!/bin/sh\ncat >/dev/null\nprintf '%%s\\n' '{\"status\":\"ok\",\"secret\":\"%s\"}'\nprintf 'credential-hook-private-diagnostic\\n' >&2\n", encoded)
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AACT_VAULT_CREDENTIAL_HOOK", path)
}

func writeCredentialHookStatus(t *testing.T, status string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		path := filepath.Join(t.TempDir(), "hook.ps1")
		script := fmt.Sprintf("$null = [Console]::In.ReadToEnd(); [Console]::Out.WriteLine('{\"status\":\"%s\"}')\n", status)
		if err := os.WriteFile(path, []byte(script), 0600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("AACT_VAULT_CREDENTIAL_HOOK", path)
		return
	}
	path := filepath.Join(t.TempDir(), "hook.sh")
	script := fmt.Sprintf("#!/bin/sh\ncat >/dev/null\nprintf '%%s\\n' '{\"status\":\"%s\"}'\n", status)
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AACT_VAULT_CREDENTIAL_HOOK", path)
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
