//go:build !windows

package tui

import (
	"context"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestVaultUnlockRunsInPTY(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is unavailable for the PTY smoke test")
	}
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate PTY test source")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(source), "../.."))
	helper := filepath.Join(t.TempDir(), "vault-unlock-pty")
	buildCtx, cancelBuild := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancelBuild()
	build := exec.CommandContext(buildCtx, "go", "build", "-o", helper, "./internal/tui/testdata/vaultunlock")
	build.Dir = root
	build.Env = append(build.Environ(), "GOWORK=off")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build PTY helper: %v\n%s", err, output)
	}
	smokeCtx, cancelSmoke := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancelSmoke()
	smoke := exec.CommandContext(smokeCtx, python, filepath.Join(root, "internal/tui/testdata/vault_unlock_pty.py"), helper, t.TempDir())
	if output, err := smoke.CombinedOutput(); err != nil {
		t.Fatalf("PTY unlock flow: %v\n%s", err, output)
	}
}
