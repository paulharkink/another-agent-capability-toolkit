package agents

import (
	"context"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestClaudePluginFeatureAndFormatValidation(t *testing.T) {
	r := NewRegistry(Dependencies{})
	a, _ := r.Adapter("claude")
	manager, ok := a.(PluginManager)
	if !ok || len(a.Features().PluginFormats) != 1 || a.Features().PluginFormats[0] != "claude-code" {
		t.Fatal("native Claude plugin feature missing")
	}
	if _, err := manager.InstallPlugin(context.Background(), Scope{}, PluginRequest{Plugin: catalog.Plugin{Format: "unsupported"}}); err == nil {
		t.Fatal("unsupported format accepted")
	}
	opencode, _ := r.Adapter("opencode")
	if _, ok := opencode.(PluginManager); ok {
		t.Fatal("unimplemented plugin format advertised")
	}
}

func TestClaudePluginNativeIsolatedInstallObserveRemove(t *testing.T) {
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("native Claude CLI unavailable; this test requires it")
	}
	home := t.TempDir()
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	source := t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, ".claude-plugin"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, ".claude-plugin", "plugin.json"), []byte(`{"name":"aact-guidance-fixture","version":"1.0.0"}`), 0600); err != nil {
		t.Fatal(err)
	}
	r := NewRegistry(Dependencies{Store: store, Probe: DiscoveryProbe{GOOS: "linux", Home: home, Getenv: func(string) string { return "" }, LookPath: exec.LookPath}})
	a, _ := r.Adapter("claude")
	manager, ok := a.(PluginManager)
	if !ok {
		t.Fatal("plugin manager missing")
	}
	scope := Scope{ID: "claude", Home: home, ExplicitHome: true}
	key := state.Key{Source: "pack", Package: "guidance", Target: "ota"}
	request := PluginRequest{Key: key, Plugin: catalog.Plugin{Name: "guidance", Format: "claude-code"}, StagedDir: source}
	row, err := manager.InstallPlugin(context.Background(), scope, request)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Record(row); err != nil {
		t.Fatal(err)
	}
	defer manager.RemovePlugin(context.Background(), scope, row)
	if _, err := manager.InstallPlugin(context.Background(), scope, request); err != nil {
		t.Fatal("plugin install was not idempotent:", err)
	}
	observation, err := a.Observe(context.Background(), scope, ObservationRequest{Managed: []state.Installation{row}})
	if err != nil || len(observation.Components) != 1 || observation.Components[0].Kind != "plugin" || observation.Components[0].Status != "installed" {
		t.Fatalf("native plugin observation: %#v %v", observation, err)
	}
	if err := manager.RemovePlugin(context.Background(), scope, row); err != nil {
		t.Fatal(err)
	}
	observation, err = a.Observe(context.Background(), scope, ObservationRequest{Managed: []state.Installation{row}})
	if err != nil || observation.Components[0].Status != "absent" {
		t.Fatalf("removed plugin still observed installed: %#v %v", observation, err)
	}
}
