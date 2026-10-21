package agents

import (
	"context"
	"encoding/json"
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
	if result.Installation.Digest == "" {
		t.Fatal("managed plugin row lacks an identity digest")
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

func TestOpenCodePluginDoesNotTakeOverForeignSamePackageOptions(t *testing.T) {
	root := t.TempDir()
	cfg := filepath.Join(root, "opencode.jsonc")
	ref := filepath.Join(root, "plugin", "index.js")
	original := `{"plugins":[{"package":"` + ref + `","options":{"owner":"user"}},"npm:other"]}`
	if err := os.WriteFile(cfg, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	r := NewRegistry(Dependencies{Store: store, Probe: DiscoveryProbe{GOOS: "linux", Home: root, Getenv: func(string) string { return "" }, LookPath: func(string) (string, error) { return "fixture", nil }}})
	a, _ := r.Adapter("opencode")
	_, err = a.(PluginManager).InstallPlugin(context.Background(), Scope{ID: "opencode", Home: root, ConfigPathOverride: cfg, ExplicitHome: true}, PluginRequest{Key: state.Key{Source: "x", Package: "y"}, Plugin: catalog.Plugin{Name: "new-name", Format: "opencode", Reference: ref, Options: map[string]any{"owner": "aact"}}})
	if err == nil || !strings.Contains(err.Error(), "not owned") {
		t.Fatalf("expected an ownership conflict, got %v", err)
	}
	got, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != original {
		t.Fatalf("foreign same-package entry was modified: %s", got)
	}
}

func TestOpenCodePluginKeepsExplicitNPMReference(t *testing.T) {
	root := t.TempDir()
	cfg := filepath.Join(root, "opencode.json")
	store, err := state.Open(filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	r := NewRegistry(Dependencies{Store: store, Probe: DiscoveryProbe{GOOS: "linux", Home: root, Getenv: func(string) string { return "" }, LookPath: func(string) (string, error) { return "fixture", nil }}})
	a, _ := r.Adapter("opencode")
	plugin := catalog.Plugin{Name: "courier", Format: "opencode", Reference: "npm:opencode-courier"}
	if _, err = a.(PluginManager).InstallPlugin(context.Background(), Scope{ID: "opencode", Home: root, ConfigPathOverride: cfg, ExplicitHome: true}, PluginRequest{Key: state.Key{Source: "fixture", Package: "demo"}, Plugin: plugin}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "npm:opencode-courier") {
		t.Fatalf("explicit npm package reference was altered: %s", b)
	}
}

func TestOpenCodePluginRepeatedUpdateAndRemovalUsesExactOwnedDigest(t *testing.T) {
	root := t.TempDir()
	cfg := filepath.Join(root, "opencode.jsonc")
	ref := filepath.Join(root, "plugin", "index.js")
	if err := os.WriteFile(cfg, []byte(`{"plugins":["npm:unrelated"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	r := NewRegistry(Dependencies{Store: store, Probe: DiscoveryProbe{GOOS: "linux", Home: root, Getenv: func(string) string { return "" }, LookPath: func(string) (string, error) { return "fixture", nil }}})
	a, _ := r.Adapter("opencode")
	mgr := a.(PluginManager)
	scope := Scope{ID: "opencode", Home: root, ConfigPathOverride: cfg, ExplicitHome: true}
	key := state.Key{Source: "x", Package: "y"}
	first, err := mgr.InstallPlugin(context.Background(), scope, PluginRequest{Key: key, Plugin: catalog.Plugin{Name: "coordinator", Format: "opencode", Reference: ref, Options: map[string]any{"owner": "aact"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Record(first.Installation); err != nil {
		t.Fatal(err)
	}
	second, err := mgr.InstallPlugin(context.Background(), scope, PluginRequest{Key: key, Plugin: catalog.Plugin{Name: "coordinator", Format: "opencode", Reference: ref, Options: map[string]any{"owner": "updated"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Removed) != 1 || second.Removed[0].Digest != first.Installation.Digest {
		t.Fatalf("did not report exact replaced entry: %+v", second.Removed)
	}
	foreign := map[string]any{"package": ref, "options": map[string]any{"owner": "user"}}
	// Inject a user-owned same-package entry after the managed update; removal
	// must remove only the managed digest and preserve the user's options.
	b, _ := os.ReadFile(cfg)
	doc, _ := standardJSON(b)
	plugins := doc["plugins"].([]any)
	plugins = append(plugins, foreign)
	doc["plugins"] = plugins
	encoded, _ := json.Marshal(doc)
	if err = os.WriteFile(cfg, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	if err = mgr.RemovePlugin(context.Background(), scope, second.Installation); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(cfg)
	doc, err = standardJSON(b)
	if err != nil {
		t.Fatal(err)
	}
	plugins = doc["plugins"].([]any)
	if len(plugins) != 2 || plugins[0] != "npm:unrelated" {
		t.Fatalf("unrelated entries were not preserved: %#v", plugins)
	}
	user, ok := plugins[1].(map[string]any)
	if !ok || user["options"].(map[string]any)["owner"] != "user" {
		t.Fatalf("foreign plugin was removed or changed: %#v", plugins)
	}
}
