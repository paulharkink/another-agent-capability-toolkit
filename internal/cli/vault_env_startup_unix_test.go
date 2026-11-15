//go:build !windows

package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/process"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/vault"
)

func TestFirstLaunchWithConfiguredVaultPasswordSourcesWithoutTTY(t *testing.T) {
	cfg, _, agentHome := cliFixture(t)
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate CLI test source")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(source), "../.."))
	work := t.TempDir()
	binary := filepath.Join(work, "aact")
	buildCtx, cancelBuild := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancelBuild()
	build := exec.CommandContext(buildCtx, "go", "build", "-o", binary, "./cmd/aact")
	build.Dir = repoRoot
	build.Env = append(process.EnvironmentWithoutVaultPassword(os.Environ()), "GOWORK=off")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build actual AACT binary: %v\n%s", err, output)
	}

	for _, source := range []string{"env", "file"} {
		t.Run(source, func(t *testing.T) {
			stateDir := filepath.Join(work, "state-"+source)
			password := "temporary-" + source + "-master-password"
			environment := process.EnvironmentWithoutVaultPassword(os.Environ())
			environment = append(environment,
				"AACT_STATE_DIR=",
				"AACT_VAULT_API_BIND_IP=",
				"AACT_BUNDLED_ROOT="+filepath.Join(repoRoot, "packages"),
			)
			if source == "env" {
				environment = append(environment, "AACT_VAULT_PASSWORD="+password)
			} else {
				passwordFile := filepath.Join(work, "vault-password")
				if err := os.WriteFile(passwordFile, []byte(password+"\n"), 0600); err != nil {
					t.Fatal(err)
				}
				environment = append(environment, "AACT_VAULT_PASSWORD_FILE="+passwordFile)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, binary,
				"install", "demo", "--config", cfg, "--state-dir", stateDir,
				"--agent", "codex", "--agent-home", filepath.Join(agentHome, source), "--set", "label=works")
			command.Env = environment
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("non-TTY first launch failed (%v):\n%s", err, strings.ReplaceAll(string(output), password, "[redacted]"))
			}
			if strings.Contains(string(output), password) {
				t.Fatal("command output exposed the vault master password")
			}
			if _, err := os.Stat(filepath.Join(stateDir, "credentials.kdbx")); err != nil {
				t.Fatalf("first launch did not create the KeePass vault: %v", err)
			}
			if _, err := vault.Open(filepath.Join(stateDir, "credentials.kdbx"), []byte(password)); err != nil {
				t.Fatalf("KeePass vault cannot be opened with the configured password: %v", err)
			}
			info, err := vault.ReadDaemonInfo(stateDir)
			if err != nil || !vault.DaemonRunning(info) {
				t.Fatalf("configured password did not start the AACT daemon: info=%+v err=%v", info, err)
			}
			t.Cleanup(func() {
				control, err := vault.ControlTokenForVault(filepath.Join(stateDir, "credentials.kdbx"), []byte(password))
				if err == nil {
					if stopErr := vault.StopDetached(info, control); stopErr != nil {
						t.Errorf("stop temporary AACT daemon: %v", stopErr)
					}
					clear(control)
				}
			})
			controlToken, err := vault.ControlTokenForVault(filepath.Join(stateDir, "credentials.kdbx"), []byte(password))
			if err != nil {
				t.Fatal(err)
			}
			clear(controlToken)
		})
	}
}
