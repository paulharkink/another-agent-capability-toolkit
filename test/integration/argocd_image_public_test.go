//go:build docker_integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestArgoStandardImagePublishesPinnedClientAndPolicyTools(t *testing.T) {
	if os.Getenv("AACT_TEST_ARGOCD_DOCKER") != "1" {
		t.Skip("set AACT_TEST_ARGOCD_DOCKER=1 to build and exercise the Argo CD MCP image")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Fatal("docker CLI is required when AACT_TEST_ARGOCD_DOCKER=1")
	}
	requireLocalDockerEndpoint(t)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	if output, err := argoDocker(ctx, "info", "--format", "{{.ServerVersion}}"); err != nil {
		t.Fatalf("Docker Engine is unavailable: %v\n%s", err, output)
	}
	buildContext, err := filepath.Abs("../../packages/argocd/mcp")
	if err != nil {
		t.Fatal(err)
	}
	tempRoot, err := os.MkdirTemp("", "aact-argocd-public-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tempRoot) })
	unique := strings.NewReplacer("/", "-", "\\", "-", ".", "-").Replace(filepath.Base(tempRoot))
	image := "aact/argocd-public-" + unique + ":test"
	container := "aact-argocd-public-" + unique
	if output, err := argoDocker(ctx, "build", "--tag", image, buildContext); err != nil {
		t.Fatalf("build standard Argo MCP image: %v\n%s", err, output)
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 45*time.Second)
		defer stop()
		_, _ = argoDocker(cleanup, "rm", "--force", container)
		_, _ = argoDocker(cleanup, "image", "rm", "--force", image)
	})

	port := argoFreeLoopbackPort(t)
	if output, err := argoDocker(ctx,
		"run", "--detach", "--rm", "--name", container,
		"--publish", fmt.Sprintf("127.0.0.1:%d:8765", port),
		"--env", "AACT_ARGOCD_SERVER=argocd.invalid",
		"--env", "AACT_ARGOCD_AUTH_MODE=sso",
		"--env", "AACT_ARGOCD_READ_ONLY=true",
		"--env", "AACT_AUTH_STATE_DIR=/tmp/aact-argocd-public-state",
		image); err != nil {
		t.Fatalf("start standard Argo MCP image: %v\n%s", err, output)
	}

	endpoint := fmt.Sprintf("http://127.0.0.1:%d/mcp", port)
	client := &http.Client{Timeout: 3 * time.Second}
	waitForArgoImage(t, ctx, client, endpoint, container)
	version, err := argoDocker(ctx, "exec", container, "argocd", "version", "--client", "--short")
	if err != nil {
		t.Fatalf("run the image's Argo CD client: %v\n%s", err, version)
	}
	if !regexp.MustCompile(`(?m)^argocd:\s*v?3\.2\.0(?:[+ ].*)?$`).Match(version) {
		t.Fatalf("standard image must expose the pinned Argo CD 3.2.0 client, got %q", strings.TrimSpace(string(version)))
	}

	initialize, sessionID := argoMCPRequest(t, client, endpoint, 1, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]string{"name": "aact-blackbox-test", "version": "1"},
	}, "")
	if len(initialize["result"]) == 0 || len(initialize["error"]) != 0 && string(initialize["error"]) != "null" {
		t.Fatalf("MCP initialize failed: %s", initialize)
	}
	toolsResponse, _ := argoMCPRequest(t, client, endpoint, 2, "tools/list", map[string]any{}, sessionID)
	var tools struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(toolsResponse["result"], &tools); err != nil {
		t.Fatalf("decode MCP tool list: %v; response %s", err, toolsResponse["result"])
	}
	names := make(map[string]bool, len(tools.Tools))
	for _, tool := range tools.Tools {
		names[tool.Name] = true
	}
	for _, name := range []string{"argocd_app_get", "argocd_app_list"} {
		if !names[name] {
			t.Fatalf("standard image did not expose %s from its selected CLI inventory", name)
		}
	}
	if names["argocd_app_sync"] {
		t.Fatal("read-only standard image exposed the app sync operation")
	}

	call, _ := argoMCPRequest(t, client, endpoint, 3, "tools/call", map[string]any{
		"name":      "argocd_app_list",
		"arguments": map[string]any{},
	}, sessionID)
	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(call["result"], &result); err != nil || len(result.Content) == 0 {
		t.Fatalf("app list tool did not return a public MCP result: %v; response %s", err, call["result"])
	}
	if !strings.Contains(result.Content[0].Text, "Argo CD authentication is required") {
		t.Fatalf("unauthenticated app list should stop before contacting an Argo server, got %q", result.Content[0].Text)
	}
}

func argoDocker(ctx context.Context, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, "docker", args...)
	return command.CombinedOutput()
}

func argoFreeLoopbackPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

func waitForArgoImage(t *testing.T, ctx context.Context, client *http.Client, endpoint, container string) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(endpoint, "/mcp")+"/health", nil)
		response, err := client.Do(request)
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(500 * time.Millisecond):
		}
	}
	logs, _ := argoDocker(ctx, "logs", container)
	t.Fatalf("Argo MCP image did not become healthy: %s", logs)
}

func argoMCPRequest(t *testing.T, client *http.Client, endpoint string, id int, method string, params map[string]any, sessionID string) (map[string]json.RawMessage, string) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	if sessionID != "" {
		request.Header.Set("Mcp-Session-Id", sessionID)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("send MCP %s request: %v", method, err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		t.Fatalf("read MCP %s response: %v", method, err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("MCP %s returned HTTP %d: %s", method, response.StatusCode, responseBody)
	}
	var decoded map[string]json.RawMessage
	if json.Unmarshal(responseBody, &decoded) == nil {
		return decoded, response.Header.Get("Mcp-Session-Id")
	}
	for _, line := range strings.Split(string(responseBody), "\n") {
		if data, found := strings.CutPrefix(strings.TrimSpace(line), "data: "); found && json.Unmarshal([]byte(data), &decoded) == nil {
			return decoded, response.Header.Get("Mcp-Session-Id")
		}
	}
	t.Fatalf("decode MCP %s response: %s", method, responseBody)
	return nil, ""
}
