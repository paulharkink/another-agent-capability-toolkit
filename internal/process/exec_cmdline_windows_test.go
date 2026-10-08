//go:build windows

package process

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestRunWindowsCommandLinePreservesNativeCmdQuotes(t *testing.T) {
	cmd, err := exec.LookPath("cmd.exe")
	if err != nil {
		t.Skip("cmd.exe is unavailable")
	}
	got, err := RunWindowsCommandLine(context.Background(), cmd, `/d /c echo "quoted path"`, t.TempDir(), nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(got)) != `"quoted path"` {
		t.Fatalf("cmd.exe output = %q, want native quoted text", got)
	}
}

func TestRunWindowsCommandLineHonorsCancellation(t *testing.T) {
	cmd, err := exec.LookPath("cmd.exe")
	if err != nil {
		t.Skip("cmd.exe is unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err = RunWindowsCommandLine(ctx, cmd, `/d /c ping -n 30 127.0.0.1 > nul`, t.TempDir(), nil, nil, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want deadline exceeded", err)
	}
	if time.Since(started) > 3*time.Second {
		t.Fatalf("raw cmd.exe command was not cancelled promptly (%s)", time.Since(started))
	}
}
