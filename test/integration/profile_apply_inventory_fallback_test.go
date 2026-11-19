package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/agents"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/app"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/state"
)

func TestInstallOnlyHostPortMCPUsesConfiguredURLWhenInventoryUnavailable(t *testing.T) {
	root := copyCapabilityPackTree(t, "../../examples/capability-pack")
	packagePath := filepath.Join(root, "vendor", "neutral-tools", "guidance", "package.toml")
	manifest, err := os.ReadFile(packagePath)
	if err != nil {
		t.Fatal(err)
	}
	manifestText := strings.Replace(string(manifest), "[[ui.sections]]", "[[inputs]]\nname = \"local_port\"\nlabel = \"Local port\"\ntype = \"integer\"\nrequired = true\ndefault = 18871\n[[ui.sections]]", 1)
	manifestText = strings.Replace(manifestText, "fields = [\"project\", \"enabled\"]", "fields = [\"project\", \"enabled\", \"local_port\"]", 1)
	manifestText = strings.Replace(manifestText, "container_port = 8080", "container_port = 8080\nhost_port_input = \"local_port\"", 1)
	if err := os.WriteFile(packagePath, []byte(manifestText), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pack, err := config.DiscoverPack(root, filepath.Join(root, "aact.toml"), "", store.Root())
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	probe := agents.DiscoveryProbe{Home: home, GOOS: "linux", Getenv: func(string) string { return "" }, LookPath: func(name string) (string, error) {
		if name == "opencode" {
			return "injected-detection-evidence", nil
		}
		return "", fmt.Errorf("not installed")
	}}
	service := app.New(pack.LegacySource(), store, app.Options{
		Runtime: &failingListRuntime{}, DiscoveryProbe: &probe,
		AgentScopes: map[string]agents.Scope{"opencode": {ID: "opencode", Home: home, ExplicitHome: true}},
	})
	ref := config.InstanceRef{PackID: pack.ID, CapabilityID: "company-guidance", Name: "ota"}
	_, err = service.ApplyInstance(context.Background(), app.InstanceRequest{
		Ref: ref, InstallOnly: true, DestinationIDs: []string{"opencode"}, ItemIDs: []string{"set:reference"},
		Inputs: map[string]any{"project": "example-project", "enabled": false, "local_port": 18871},
	})
	if err != nil {
		t.Fatalf("install agent registration with runtime inventory unavailable: %v", err)
	}
	configPath := filepath.Join(home, ".config", "opencode", "opencode.jsonc")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		MCP map[string]struct {
			URL string `json:"url"`
		} `json:"mcp"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	for _, registration := range document.MCP {
		if registration.URL == "http://localhost:18871/mcp" {
			return
		}
	}
	t.Fatalf("agent config did not use the declared host endpoint: %s", data)
}
