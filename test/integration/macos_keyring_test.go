package integration

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestMacOSCredentialHookWorksWithoutPython(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("requires macOS Keychain")
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
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-p",
		`(version 1)(allow default)(deny process-exec (literal "/usr/bin/python3"))`,
		filepath.Join("..", "..", "scripts", "keyring", "macos.sh"))
	command.Stdin = bytes.NewReader(request)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("macOS credential hook failed without Python: %v", err)
	}
	var response struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(output, &response); err != nil {
		t.Fatalf("invalid credential hook response: %v", err)
	}
	if response.Status != "missing" {
		t.Fatalf("querying a new random account returned %q, want missing", response.Status)
	}
}
