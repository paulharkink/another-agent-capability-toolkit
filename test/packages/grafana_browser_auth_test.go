package packages_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestGrafanaBrowserCredentialsPrepareWithoutManualCookie(t *testing.T) {
	dir := t.TempDir()
	request := map[string]any{"protocol_version": 1, "target": map[string]any{"raw": map[string]any{"grafana": map[string]any{"auth_mode": "browser_credentials"}}}, "state_dir": dir, "inputs": map[string]any{"auth_mode": "browser_credentials", "browser_username": "synthetic-user", "browser_password": "synthetic-password"}}
	input, _ := json.Marshal(request)
	cmd := exec.Command("python3", filepath.Join("..", "..", "packages", "grafana-inspector", "mcp", "auth.py"), "prepare")
	cmd.Stdin = bytes.NewReader(input)
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("prepare browser authentication: %v", err)
	}
	var result struct {
		AuthRequired bool `json:"auth_required"`
	}
	if err := json.Unmarshal(output, &result); err != nil || result.AuthRequired {
		t.Fatalf("prepare should allow browser login without a cookie: %s (%v)", output, err)
	}
	contents, err := os.ReadFile(filepath.Join(dir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(contents, []byte("synthetic-password")) || bytes.Contains(output, []byte("synthetic-password")) {
		t.Fatal("browser password leaked into authentication state or action output")
	}
}
