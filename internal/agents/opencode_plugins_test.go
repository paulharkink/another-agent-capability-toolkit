package agents

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
)

func TestOpenCodePluginManagerPreservesJSONCAndRemovesOnlyOwnedEntry(t *testing.T) {
	root := t.TempDir()
	cfg := filepath.Join(root, "opencode.jsonc")
	original := `{"$schema":"https://opencode.ai/config.json", // keep comment
"provider":{"test":{"name":"fixture"}},"mcp":{"x":{"type":"remote","url":"https://fixture.invalid"}},"plugins":["npm:other", // plugin comment survives edits
{"package":"npm:keep","options":{"a":1}}]}`
	if err := os.WriteFile(cfg, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	r := NewRegistry(Dependencies{Store: store, Probe: DiscoveryProbe{GOOS: "linux", Home: root, Getenv: func(string) string { return "" }, LookPath: func(string) (string, error) { return "fixture", nil }}})
	a, _ := r.Adapter("opencode")
	key := state.Key{Source: "fixture", Package: "x", Target: "opencode"}
	plugin := catalog.Plugin{Name: "coordinator", Format: "opencode", Reference: filepath.Join(root, "plugin", "index.js"), Options: map[string]any{"repoMapFile": filepath.Join(root, "inventory.json")}}
	result, err := a.(PluginManager).InstallPlugin(context.Background(), Scope{ID: "opencode", Home: root, ConfigPathOverride: cfg, ExplicitHome: true}, PluginRequest{Key: key, Plugin: plugin})
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	for _, want := range []string{"keep comment", "plugin comment survives edits", "\"provider\"", "\"mcp\"", "npm:other", "npm:keep", plugin.Reference, "repoMapFile"} {
		if !strings.Contains(text, want) {
			t.Fatalf("preserved/registered config missing %q: %s", want, text)
		}
	}
	if err := a.(PluginManager).RemovePlugin(context.Background(), Scope{ID: "opencode", Home: root, ConfigPathOverride: cfg, ExplicitHome: true}, result.Installation); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(cfg)
	if strings.Contains(string(b), plugin.Reference) || !strings.Contains(string(b), "npm:other") || !strings.Contains(string(b), "npm:keep") {
		t.Fatalf("remove changed unrelated plugin entries: %s", b)
	}
}
