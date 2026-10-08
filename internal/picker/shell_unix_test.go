//go:build !windows

package picker

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestResolveShellPathCancellationKillsShellDescendants(t *testing.T) {
	shell := testPathShell(t)
	cwd := t.TempDir()
	marker := filepath.Join(cwd, "child.pid")
	quotedMarker := "'" + strings.ReplaceAll(marker, "'", "'\\''") + "'"
	tmpMarker := quotedMarker + ".tmp"
	expression := "$(sleep 3 & child=$!; printf '%s\\n' \"$child\" > " + tmpMarker + "; mv " + tmpMarker + " " + quotedMarker + "; wait)"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := resolveShellPath(ctx, expression, cwd, shell)
		done <- err
	}()

	deadline := time.Now().Add(time.Second)
	var childPID int
	for {
		pidBytes, err := os.ReadFile(marker)
		if err == nil {
			if parsedPID, parseErr := strconv.Atoi(strings.TrimSpace(string(pidBytes))); parseErr == nil && parsedPID > 1 {
				childPID = parsedPID
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("shell descendant did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	started := time.Now()
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled resolution unexpectedly succeeded")
		}
	case <-time.After(800 * time.Millisecond):
		t.Fatal("resolution did not return promptly after cancellation")
	}

	deadline = time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if !unixProcessAlive(childPID) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	_ = exec.Command("kill", strconv.Itoa(childPID)).Run()
	t.Fatalf("owned shell descendant %d remained alive after cancellation (%s elapsed)", childPID, time.Since(started))
}

func unixProcessAlive(pid int) bool {
	status, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return false
	}
	fields := strings.Fields(string(status))
	return len(fields) > 0 && !strings.HasPrefix(fields[0], "Z")
}
