//go:build docker_integration

package packages_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestJenkinsDockerServerReadOnlyFlagControlsWriteTools(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	image := fmt.Sprintf("aact/jenkins-contract-test:%d", time.Now().UnixNano())
	runDocker(t, ctx, root, "build", "--tag", image, filepath.Join(root, "packages", "jenkins", "mcp"))

	for _, mode := range []struct {
		name        string
		value       string
		readOnly    bool
		setReadOnly bool
	}{{name: "default", readOnly: true}, {name: "enabled", value: "true", readOnly: true, setReadOnly: true}, {name: "disabled", value: "false", readOnly: false, setReadOnly: true}} {
		t.Run("read_only="+mode.name, func(t *testing.T) {
			port := unusedTCPPort(t)
			name := fmt.Sprintf("aact-jenkins-contract-%d", time.Now().UnixNano())
			t.Cleanup(func() {
				cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cleanupCancel()
				_ = exec.CommandContext(cleanupCtx, "docker", "rm", "--force", name).Run()
			})
			args := []string{
				"run", "--detach", "--name", name,
				"--publish", fmt.Sprintf("127.0.0.1:%d:9887", port),
				"--env", "jenkins_url=http://127.0.0.1:9",
				"--env", "jenkins_username=aact-test-user",
				"--env", "jenkins_password=aact-test-token",
			}
			if mode.setReadOnly {
				args = append(args, "--env", "JENKINS_READ_ONLY="+mode.value)
			}
			args = append(args, image)
			runDocker(t, ctx, root, args...)
			endpoint := fmt.Sprintf("http://127.0.0.1:%d/mcp", port)
			var tools []string
			deadline := time.Now().Add(45 * time.Second)
			for time.Now().Before(deadline) {
				tools, err = jenkinsTools(ctx, endpoint)
				if err == nil {
					break
				}
				time.Sleep(250 * time.Millisecond)
			}
			if err != nil {
				t.Fatalf("Jenkins MCP did not return tools: %v", err)
			}
			available := map[string]bool{}
			for _, name := range tools {
				available[name] = true
			}
			for _, name := range []string{"get_all_items", "get_build", "get_build_console_tail", "get_build_failure_excerpt"} {
				if !available[name] {
					t.Errorf("expected read tool %q, got %v", name, tools)
				}
			}
			for _, name := range []string{"set_item_config", "stop_build", "set_node_config", "cancel_queue_item"} {
				if mode.readOnly && available[name] {
					t.Errorf("write tool %q must not be registered in read-only mode", name)
				}
				if !mode.readOnly && !available[name] {
					t.Errorf("write tool %q should be registered when read-only mode is disabled", name)
				}
			}
			if available["build_item"] != !mode.readOnly {
				t.Errorf("build_item availability = %t, want %t (tools: %v)", available["build_item"], !mode.readOnly, tools)
			}
		})
	}
}

func runDocker(t *testing.T, ctx context.Context, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Dir = dir
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		t.Fatalf("docker %s failed: %v\n%s", strings.Join(args, " "), err, output.String())
	}
}

func unusedTCPPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func jenkinsTools(ctx context.Context, endpoint string) ([]string, error) {
	client := &http.Client{Timeout: 3 * time.Second}
	request := func(payload string, session string) (*http.Response, []byte, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(payload))
		if err != nil {
			return nil, nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if session != "" {
			req.Header.Set("Mcp-Session-Id", session)
			req.Header.Set("Mcp-Protocol-Version", "2025-06-18")
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, nil, err
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return resp, body, err
	}
	response, body, err := request(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"aact-test","version":"1"}}}`, "")
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("initialize HTTP %d: %s", response.StatusCode, body)
	}
	body, err = mcpResponseJSON(response, body)
	if err != nil {
		return nil, err
	}
	var initialized struct {
		Result map[string]any `json:"result"`
		Error  any            `json:"error"`
	}
	if err := json.Unmarshal(body, &initialized); err != nil {
		return nil, fmt.Errorf("decode initialize: %w: %s", err, body)
	}
	if initialized.Error != nil || initialized.Result["protocolVersion"] == nil {
		return nil, fmt.Errorf("invalid initialize response: %s", body)
	}
	session := response.Header.Get("Mcp-Session-Id")
	if session != "" {
		if _, _, err := request(`{"jsonrpc":"2.0","method":"notifications/initialized"}`, session); err != nil {
			return nil, err
		}
	}
	response, body, err = request(`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`, session)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tools/list HTTP %d: %s", response.StatusCode, body)
	}
	body, err = mcpResponseJSON(response, body)
	if err != nil {
		return nil, err
	}
	var listed struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
		Error any `json:"error"`
	}
	if err := json.Unmarshal(body, &listed); err != nil {
		return nil, fmt.Errorf("decode tools/list: %w: %s", err, body)
	}
	if listed.Error != nil {
		return nil, fmt.Errorf("tools/list failed: %v", listed.Error)
	}
	tools := make([]string, 0, len(listed.Result.Tools))
	for _, tool := range listed.Result.Tools {
		tools = append(tools, tool.Name)
	}
	return tools, nil
}

func mcpResponseJSON(response *http.Response, body []byte) ([]byte, error) {
	if !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		return body, nil
	}
	scanner := bufio.NewScanner(bytes.NewReader(body))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "data:") {
			return []byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), nil
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("MCP event stream did not contain a data event: %s", body)
}
