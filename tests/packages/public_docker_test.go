//go:build docker_integration

package packages_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func dockerCommand(t *testing.T, args ...string) string {
	t.Helper()
	command := exec.Command("docker", args...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("docker %v: %v\n%s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

func TestAzureSSEPublishedPort(t *testing.T) {
	dir, err := filepath.Abs(filepath.Join("..", "..", "packages", "azure-inspector", "mcp"))
	if err != nil {
		t.Fatal(err)
	}
	dockerCommand(t, "build", "--quiet", "-t", "aact/azure-inspector:test-runtime", dir)
	name := fmt.Sprintf("aact-package-azure-validation-%d", time.Now().UnixNano())
	dockerCommand(t, "run", "--detach", "--rm", "--name", name, "--publish", "127.0.0.1::8084", "-e", "AZMCP_TRANSPORT=sse", "-e", "AZMCP_PORT=8084", "aact/azure-inspector:test-runtime", "server", "start")
	t.Cleanup(func() { exec.Command("docker", "rm", "--force", name).Run() })
	endpoint := dockerCommand(t, "port", name, "8084/tcp")
	client := http.Client{Timeout: time.Second, Transport: &http.Transport{Proxy: nil}}
	deadline := time.Now().Add(8 * time.Second)
	var last error
	for time.Now().Before(deadline) {
		response, err := client.Get("http://" + endpoint + "/sse")
		if err == nil {
			response.Body.Close()
			if response.StatusCode != 200 || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
				t.Fatalf("unexpected SSE response: %s %s", response.Status, response.Header.Get("Content-Type"))
			}
			return
		}
		last = err
		time.Sleep(100 * time.Millisecond)
	}
	logs, _ := exec.Command("docker", "logs", name).CombinedOutput()
	t.Fatalf("published Azure SSE endpoint unavailable: %v\n%s", last, logs)
}

func TestForgejoHTTPPublishedPort(t *testing.T) {
	dir, err := filepath.Abs(filepath.Join("..", "..", "packages", "forgejo", "mcp"))
	if err != nil {
		t.Fatal(err)
	}
	dockerCommand(t, "build", "--quiet", "-f", filepath.Join(dir, "Dockerfile.test"), "-t", "aact/forgejo-config-tests:local", dir)
	image := "git.b4mad.industries/agentic-forges/forgejo-mcp:v2.33.0@sha256:90256ca6219677cac950df0a5e9181638cb1899e0f845697543ed14d69598fcf"
	dockerCommand(t, "pull", image)
	fixture := fmt.Sprintf("aact-package-forgejo-fixture-%d", time.Now().UnixNano())
	server := fixture + "-mcp"
	dockerCommand(t, "run", "--detach", "--rm", "--name", fixture, "--publish", "127.0.0.1::8080", "--mount", "type=bind,src="+filepath.Join(dir, "tests", "fixtures")+",dst=/fixture,readonly", "--entrypoint", "python", "aact/forgejo-config-tests:local", "/fixture/api.py")
	t.Cleanup(func() { exec.Command("docker", "rm", "--force", server, fixture).Run() })
	dockerCommand(t, "run", "--detach", "--rm", "--name", server, "--network", "container:"+fixture, "-e", "FORGEJO_ACCESS_TOKEN=synthetic-token", image, "--transport", "http", "--http-port", "8080", "--url", "http://127.0.0.1:18080")
	endpoint := dockerCommand(t, "port", fixture, "8080/tcp")
	client := http.Client{Timeout: time.Second, Transport: &http.Transport{Proxy: nil}}
	payload := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"fixture","version":"1"}}}`
	deadline := time.Now().Add(8 * time.Second)
	var last error
	for time.Now().Before(deadline) {
		request, err := http.NewRequest("POST", "http://"+endpoint+"/mcp", strings.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json, text/event-stream")
		response, err := client.Do(request)
		if err == nil {
			defer response.Body.Close()
			if response.StatusCode != 200 {
				t.Fatalf("unexpected MCP status: %s", response.Status)
			}
			var result struct {
				Result struct {
					ServerInfo struct{ Name, Version string }
				}
			}
			if err = json.NewDecoder(response.Body).Decode(&result); err != nil {
				t.Fatal(err)
			}
			if result.Result.ServerInfo.Version != "v2.33.0" {
				t.Fatalf("unexpected upstream MCP version: %+v", result)
			}
			return
		}
		last = err
		time.Sleep(100 * time.Millisecond)
	}
	logs, _ := exec.Command("docker", "logs", server).CombinedOutput()
	t.Fatalf("published Forgejo MCP endpoint unavailable: %v\n%s", last, logs)
}
