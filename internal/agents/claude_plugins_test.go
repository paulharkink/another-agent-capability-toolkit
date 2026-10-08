package agents

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type failingClaudePluginRunner struct {
	added, installed bool
	failInstall      bool
	pluginID         string
	calls            [][]string
}

func (r *failingClaudePluginRunner) Run(_ context.Context, args []string, _ string, _ []byte, _ map[string]string, _ func([]byte)) ([]byte, error) {
	r.calls = append(r.calls, append([]string(nil), args...))
	if len(args) < 3 {
		return nil, nil
	}
	switch args[2] {
	case "list":
		plugins := []nativePlugin{{ID: "unrelated@community", Scope: "user", InstallPath: "/fixture/unrelated"}}
		if r.installed {
			id := r.pluginID
			if id == "" {
				id = "guidance@aact-fixture-guidance"
			}
			plugins = append(plugins, nativePlugin{ID: id, Scope: "user", InstallPath: "/fixture/guidance"})
		}
		return json.Marshal(plugins)
	case "marketplace":
		if len(args) > 3 && args[3] == "add" {
			r.added = true
		}
	case "install":
		if r.failInstall {
			return nil, errors.New("injected plugin install failure")
		}
		r.installed = true
	case "uninstall":
		r.installed = false
	}
	return nil, nil
}

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

func TestClaudePluginInstallReturnsMarketplaceEffectWhenInstallFails(t *testing.T) {
	home := t.TempDir()
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	staged := t.TempDir()
	if err := os.MkdirAll(filepath.Join(staged, ".claude-plugin"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staged, ".claude-plugin", "plugin.json"), []byte(`{"name":"guidance"}`), 0600); err != nil {
		t.Fatal(err)
	}
	runner := &failingClaudePluginRunner{failInstall: true}
	r := NewRegistry(Dependencies{Store: store, Runner: runner, Probe: DiscoveryProbe{GOOS: "linux", Home: home, Getenv: func(string) string { return "" }, LookPath: func(string) (string, error) { return "/fixture/claude", nil }}})
	a, _ := r.Adapter("claude")
	manager := a.(PluginManager)
	result, err := manager.InstallPlugin(context.Background(), Scope{ID: "claude", Home: home, ExplicitHome: true}, PluginRequest{Key: state.Key{Source: "fixture", Package: "demo", Target: "ota"}, Plugin: catalog.Plugin{Name: "guidance", Format: "claude-code"}, StagedDir: staged})
	if err == nil || !runner.added || len(result.Effects) != 1 || result.Effects[0].Component != "plugin-marketplace" || result.Installation.Component != "" {
		t.Fatalf("partial marketplace effect not reported: result=%+v err=%v runner=%+v", result, err, runner)
	}
	if runner.installed {
		t.Fatal("failure fixture marked the plugin installed")
	}
}

func TestClaudePluginRepeatInstallReportsRemovalWhenReinstallFails(t *testing.T) {
	home := t.TempDir()
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	staged := t.TempDir()
	if err := os.MkdirAll(filepath.Join(staged, ".claude-plugin"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staged, ".claude-plugin", "plugin.json"), []byte(`{"name":"guidance"}`), 0600); err != nil {
		t.Fatal(err)
	}
	previous := state.Installation{Key: state.Key{Source: "fixture", Package: "demo", Target: "ota"}, AgentID: "claude", AgentKind: "claude", Component: "plugin", Destination: "/fixture/installed/guidance", Mode: "claude-code", ReleaseID: "guidance@aact-fixture-guidance"}
	pluginID := "guidance@aact-" + previous.Key.ID()[:12] + "-guidance"
	runner := &failingClaudePluginRunner{installed: true, failInstall: true, pluginID: pluginID}
	r := NewRegistry(Dependencies{Store: store, Runner: runner, Probe: DiscoveryProbe{GOOS: "linux", Home: home, Getenv: func(string) string { return "" }, LookPath: func(string) (string, error) { return "/fixture/claude", nil }}})
	a, _ := r.Adapter("claude")
	result, err := a.(PluginManager).InstallPlugin(context.Background(), Scope{ID: "claude", Home: home, ExplicitHome: true}, PluginRequest{
		Key: previous.Key, Plugin: catalog.Plugin{Name: "guidance", Format: "claude-code"}, StagedDir: staged,
		MarketplaceConfigured: true, Existing: &previous,
	})
	if err == nil || !strings.Contains(err.Error(), "injected plugin install failure") {
		t.Fatalf("real install error was not preserved: %v", err)
	}
	if runner.installed {
		t.Fatal("failure fixture still claims the removed plugin is installed")
	}
	if len(result.Removed) != 1 || result.Removed[0] != previous || len(result.Effects) != 1 || result.Effects[0].Component != "plugin-marketplace" || result.Installation.Component != "" {
		t.Fatalf("partial repeat result lost achieved effects or invented installation: %+v", result)
	}
	if got := runner.calls; len(got) < 4 || strings.Join(got[len(got)-1], " ") != "claude plugin install "+pluginID+" --scope user" {
		t.Fatalf("retry did not surface failing install command: %#v", got)
	}
}

func TestClaudePluginNativeIsolatedInstallObserveRemove(t *testing.T) {
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("native Claude CLI unavailable; this test requires it")
	}
	home := t.TempDir()
	claudeConfig := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeConfig, 0700); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(claudeConfig, "settings.json")
	if err := os.WriteFile(settingsPath, []byte(`{"permissions":{"allow":["Bash(ls:*)"]},"enabledPlugins":{"unrelated@community":true}}`), 0600); err != nil {
		t.Fatal(err)
	}
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
	result, err := manager.InstallPlugin(context.Background(), scope, request)
	if err != nil {
		t.Fatal(err)
	}
	row := result.Installation
	if err := store.Record(row); err != nil {
		t.Fatal(err)
	}
	defer manager.RemovePlugin(context.Background(), scope, row)
	if _, err := manager.InstallPlugin(context.Background(), scope, request); err != nil {
		t.Fatal("plugin install was not idempotent:", err)
	}
	if err := os.WriteFile(filepath.Join(source, "updated.txt"), []byte("updated staged content"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.InstallPlugin(context.Background(), scope, request); err != nil {
		t.Fatal("plugin update failed:", err)
	}
	marketCopy := filepath.Join(row.SourcePath, "plugins", "aact-guidance-fixture", "updated.txt")
	if content, err := os.ReadFile(marketCopy); err != nil || string(content) != "updated staged content" {
		t.Fatalf("repeat apply did not reconcile staged plugin content: %q %v", content, err)
	}
	installedCopy := filepath.Join(row.Destination, "updated.txt")
	installedContent, err := os.ReadFile(installedCopy)
	if err != nil || string(installedContent) != "updated staged content" {
		t.Fatalf("Claude update did not reconcile installed plugin content at %s: %q %v", installedCopy, installedContent, err)
	}
	t.Logf("Claude installed-cache evidence: destination=%s content=%q", installedCopy, installedContent)
	var settings map[string]any
	if data, err := os.ReadFile(settingsPath); err != nil {
		t.Fatal(err)
	} else if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("Claude settings JSON invalid after repeat install: %v", err)
	}
	plugins, ok := settings["enabledPlugins"].(map[string]any)
	permissions, permissionsOK := settings["permissions"].(map[string]any)
	allow, allowOK := permissions["allow"].([]any)
	if !permissionsOK || !allowOK || len(allow) != 1 || allow[0] != "Bash(ls:*)" || !ok || plugins["unrelated@community"] != true {
		t.Fatalf("repeat apply damaged unrelated native plugin/config settings: %#v", settings)
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
