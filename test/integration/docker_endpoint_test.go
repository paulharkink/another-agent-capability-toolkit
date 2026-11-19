package integration

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func requireLocalDockerEndpoint(t *testing.T) {
	t.Helper()
	contextName := os.Getenv("DOCKER_CONTEXT")
	endpoint := ""
	if contextName == "" {
		endpoint = os.Getenv("DOCKER_HOST")
	}
	if endpoint == "" {
		if contextName == "" {
			output, err := exec.Command("docker", "context", "show").Output()
			if err != nil {
				t.Fatalf("read selected Docker context: %v", err)
			}
			contextName = strings.TrimSpace(string(output))
		}
		output, err := exec.Command("docker", "context", "inspect", contextName, "--format", "{{.Endpoints.docker.Host}}").Output()
		if err != nil {
			t.Fatalf("inspect selected Docker context %q: %v", contextName, err)
		}
		endpoint = strings.TrimSpace(string(output))
	}
	if !strings.HasPrefix(endpoint, "unix://") {
		t.Fatalf("refusing to create test Docker resources on non-local endpoint %q", endpoint)
	}
}
