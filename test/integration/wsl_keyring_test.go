package integration

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// This opt-in check queries a random, nonexistent account without changing any
// credentials. It must run in WSL with access to Windows Credential Locker.
func TestWSLCredentialHookCanQueryWindowsLocker(t *testing.T) {
	if runtime.GOOS != "linux" || os.Getenv("AACT_TEST_WSL_KEYRING") != "1" {
		t.Skip("requires an explicitly enabled WSL Credential Locker check")
	}
	account := make([]byte, 32)
	if _, err := rand.Read(account); err != nil {
		t.Fatal(err)
	}
	request, err := json.Marshal(map[string]any{
		"version": 1, "action": "get", "service": "AACT shared Vault",
		"account": "state-" + hex.EncodeToString(account),
	})
	if err != nil {
		t.Fatal(err)
	}
	hook := os.Getenv("AACT_TEST_WSL_HOOK")
	if hook == "" {
		hook = filepath.Join("..", "..", "scripts", "keyring", "wsl.sh")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "/bin/sh", hook)
	command.Stdin = bytes.NewReader(request)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("WSL credential hook could not query Windows Credential Locker: %v", err)
	}
	var response struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(output, &response); err != nil {
		t.Fatalf("WSL credential hook returned invalid JSON: %v", err)
	}
	if response.Status != "missing" {
		t.Fatalf("querying a new random account returned status %q, want missing", response.Status)
	}
}
