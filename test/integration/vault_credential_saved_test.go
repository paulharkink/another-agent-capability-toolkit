package integration

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

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

func TestPublicMCPStopUnlocksWithSavedCredential(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("isolated fake Docker executable uses a POSIX script")
	}
	const password = "isolated-mcp-keyring-password"
	binary := buildPublicCLIBinary(t)
	root := prepareCredentialCLITest(t, password)
	trace := filepath.Join(t.TempDir(), "hook-trace")
	t.Setenv("AACT_VAULT_CREDENTIAL_HOOK", writeSavedCredentialTraceHook(t, password, trace))
	t.Setenv("AACT_VAULT_API_BIND_IP", "127.0.0.1")
	packRoot := t.TempDir()
	packageID := "keyring-mcp-fixture"
	manifestPath := filepath.Join(packRoot, "aact.toml")
	manifest := fmt.Sprintf(`schema_version = 1
source_id = "keyring-mcp-pack"
[[catalog]]
id = %q
source = %q
`, packageID, "./packages/"+packageID)
	packageRoot := filepath.Join(packRoot, "packages", packageID)
	if err := os.MkdirAll(packageRoot, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	packageManifest := fmt.Sprintf(`schema_version = 1
id = %q
name = "Saved credential MCP fixture"
[[inputs]]
name = "host_port"
type = "integer"
label = "Host port"
default = 32981
[mcp]
name = "credential-test"
runtime = "docker"
image = "busybox:latest"
transport = "streamable-http"
container_port = 8765
host_port_input = "host_port"
endpoint_path = "/mcp"
`, packageID)
	if err := os.WriteFile(filepath.Join(packageRoot, "package.toml"), []byte(packageManifest), 0600); err != nil {
		t.Fatal(err)
	}
	create := exec.Command(binary, "--config", manifestPath, "instance", "create", packageID, "demo")
	create.Dir = packRoot
	if output, err := create.CombinedOutput(); err != nil {
		t.Fatalf("create isolated MCP instance: %v; output=%q", err, output)
	}
	fakeBin := t.TempDir()
	fakeDocker := filepath.Join(fakeBin, "docker")
	if err := os.WriteFile(fakeDocker, []byte("#!/bin/sh\nif [ \"$1\" = ps ]; then exit 0; fi\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	command := exec.Command(binary, "--config", manifestPath, "--instance", "demo", "mcp", "stop", packageID)
	command.Dir = packRoot
	t.Cleanup(func() {
		info, infoErr := vault.ReadDaemonInfo(root)
		if infoErr != nil {
			return
		}
		control, tokenErr := vault.ControlTokenForVault(filepath.Join(root, "credentials.kdbx"), []byte(password))
		if tokenErr != nil {
			return
		}
		defer clear(control)
		_ = vault.StopDetached(info, control)
	})
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "no current Docker runtime observation") || strings.Contains(string(output), "needs the shared vault") {
		t.Fatalf("MCP stop did not proceed through the normal saved-credential unlock path: err=%v output=%q", err, output)
	}
	if marker, err := os.ReadFile(trace); err != nil || !strings.Contains(strings.ReplaceAll(string(marker), "\r\n", "\n"), "get\n") {
		t.Fatalf("MCP stop did not retrieve its saved credential: trace=%q err=%v", marker, err)
	}
	if strings.Contains(string(output), password) {
		t.Fatal("Vault password appeared in CLI output")
	}
}

func writeSavedCredentialTraceHook(t *testing.T, password, trace string) string {
	t.Helper()
	response := `{"status":"ok","secret":"` + base64.StdEncoding.EncodeToString([]byte(password)) + `"}`
	base := os.Getenv("AACT_VAULT_CREDENTIAL_HOOK")
	if runtime.GOOS == "windows" {
		path := base + ".ps1"
		script := "$null = [Console]::In.ReadToEnd(); Add-Content -LiteralPath '" + strings.ReplaceAll(trace, "'", "''") + "' -Value get; [Console]::Out.WriteLine('" + response + "')\n"
		if err := os.WriteFile(path, []byte(script), 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	quotedTrace := shellQuote(trace)
	path := base
	script := "#!/bin/sh\ncat >/dev/null\nprintf 'get\\n' >> " + quotedTrace + "\nprintf '%s\\n' '" + strings.ReplaceAll(response, "'", "'\\''") + "'\n"
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func TestPublicCLIRejectsMetacharacterBatchHookPath(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("cmd.exe metacharacter handling is Windows-specific")
	}
	binary := buildPublicCLIBinary(t)
	const password = "isolated-cmd-metachar-password"
	root := prepareCredentialCLITest(t, password)
	t.Setenv("AACT_VAULT_API_BIND_IP", "127.0.0.1")
	marker := filepath.Join(t.TempDir(), "hook-ran")
	t.Setenv("AACT_TEST_HOOK_MARKER", marker)

	base := filepath.Join(filepath.Dir(os.Getenv("AACT_VAULT_CREDENTIAL_HOOK")), "hook-metachar")
	actualScript := base + ".cmd"
	injectedPath := actualScript + "&echo-injected.cmd"
	script := "@echo off\r\nset /p \"request=\"\r\n>>\"%AACT_TEST_HOOK_MARKER%\" echo ran\r\necho {\"status\":\"missing\"}\r\n"
	if err := os.WriteFile(actualScript, []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AACT_VAULT_CREDENTIAL_HOOK", injectedPath)

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

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, "daemon", "stop")
	command.Stdin = bytes.NewReader(nil)
	command.Env = os.Environ()
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "interactive terminal") {
		t.Fatalf("unsafe hook path was not rejected to manual fallback: err=%v output=%q", err, output)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("batch path with cmd metacharacters was executed: marker stat err=%v", err)
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
