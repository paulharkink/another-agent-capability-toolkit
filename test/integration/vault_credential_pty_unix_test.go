//go:build !windows

package integration

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

func credentialPTYSupported() bool { return true }

func runPublicCLIOnPTY(t *testing.T, binary string, enroll, expectWarning bool) (int, string) {
	t.Helper()
	command := exec.Command(binary)
	command.Env = os.Environ()
	stderr := &bytes.Buffer{}
	command.Stderr = stderr
	terminal, err := pty.StartWithSize(command, &pty.Winsize{Rows: 30, Cols: 100})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if command.ProcessState == nil {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
		_ = terminal.Close()
	}()

	output := make(chan string, 64)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, readErr := terminal.Read(buf)
			if n > 0 {
				output <- string(buf[:n])
			}
			if readErr != nil {
				return
			}
		}
	}()
	seen := ""
	waitFor := func(needle string, timeout time.Duration) bool {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		for !strings.Contains(seen, needle) {
			select {
			case chunk := <-output:
				seen += chunk
			case <-timer.C:
				return false
			}
		}
		return true
	}
	write := func(value string) {
		t.Helper()
		if _, err := io.WriteString(terminal, value); err != nil {
			t.Fatalf("write PTY input: %v", err)
		}
	}
	if !waitFor("Master password", 10*time.Second) {
		t.Fatalf("CLI did not render the Vault unlock screen; output=%q stderr=%q", seen, stderr.String())
	}
	if enroll {
		write("\x13") // Ctrl+S opts in to credential storage.
	}
	write("terminal-only-test-password\r")
	if expectWarning {
		if !waitFor("Could not save the vault password", 40*time.Second) {
			t.Fatalf("failed keyring write warning was not shown in the unlock screen; output=%q stderr=%q", seen, stderr.String())
		}
		write("\r")
	}
	if !waitFor("AACT ·", 40*time.Second) {
		t.Fatalf("CLI did not reach its main TUI after valid unlock; output=%q stderr=%q", seen, stderr.String())
	}
	write("q")
	if err := command.Wait(); err != nil {
		t.Fatalf("CLI failed after quitting its TUI: %v; stderr=%q", err, stderr.String())
	}
	return command.ProcessState.ExitCode(), seen + stderr.String()
}
