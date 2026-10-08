package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/process"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/render"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type claudePluginAdapter struct{ *mcpAdapter }
type nativePlugin struct {
	ID          string `json:"id"`
	Scope       string `json:"scope"`
	InstallPath string `json:"installPath"`
}

func (a *claudePluginAdapter) Features() FeatureSet {
	return FeatureSet{Skills: true, MCPs: true, PluginFormats: []string{"claude-code"}}
}

func (a *claudePluginAdapter) pluginCommand(ctx context.Context, scope Scope, args ...string) ([]byte, error) {
	probe := a.scopedProbe(scope)
	home := probe.Home
	if home == "" {
		return nil, fmt.Errorf("explicit agent home required")
	}
	root := filepath.Join(home, ".claude")
	if !scope.ExplicitHome && probe.Getenv != nil && probe.Getenv("CLAUDE_CONFIG_DIR") != "" {
		root = probe.Getenv("CLAUDE_CONFIG_DIR")
	}
	runner := a.deps.Runner
	if runner == nil {
		runner = process.OSExecutor{}
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var diagnostic []byte
	out, err := runner.Run(ctx, append([]string{"claude", "plugin"}, args...), home, nil, map[string]string{"CLAUDE_CONFIG_DIR": root}, func(data []byte) {
		diagnostic = append(diagnostic, data...)
		if len(diagnostic) > 65536 {
			diagnostic = diagnostic[len(diagnostic)-65536:]
		}
	})
	if err != nil {
		return nil, fmt.Errorf("Claude plugin %s failed: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(diagnostic)))
	}
	return out, nil
}
func (a *claudePluginAdapter) pluginInventory(ctx context.Context, scope Scope) ([]nativePlugin, error) {
	out, err := a.pluginCommand(ctx, scope, "list", "--json")
	if err != nil {
		return nil, err
	}
	var inventory []nativePlugin
	if err := json.Unmarshal(out, &inventory); err != nil {
		return nil, fmt.Errorf("Claude plugin list returned invalid JSON: %w", err)
	}
	return inventory, nil
}
func findNativePlugin(inventory []nativePlugin, id string) (nativePlugin, bool) {
	for _, plugin := range inventory {
		if plugin.ID == id && plugin.Scope == "user" {
			return plugin, true
		}
	}
	return nativePlugin{}, false
}
func (a *claudePluginAdapter) InstallPlugin(ctx context.Context, scope Scope, request PluginRequest) (PluginInstallResult, error) {
	if request.Plugin.Format != "claude-code" {
		return PluginInstallResult{}, fmt.Errorf("unsupported plugin format %q for Claude", request.Plugin.Format)
	}
	if a.deps.Store == nil {
		return PluginInstallResult{}, fmt.Errorf("state store required")
	}
	data, err := os.ReadFile(filepath.Join(request.StagedDir, ".claude-plugin", "plugin.json"))
	if err != nil {
		return PluginInstallResult{}, fmt.Errorf("native plugin manifest: %w", err)
	}
	var manifest struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil || manifest.Name == "" || strings.ContainsAny(manifest.Name, "/\\@\r\n") {
		return PluginInstallResult{}, fmt.Errorf("invalid native plugin manifest in %s", request.StagedDir)
	}
	d, err := a.Detect(ctx, scope)
	if err != nil {
		return PluginInstallResult{}, err
	}
	if !d.Installed {
		return PluginInstallResult{}, fmt.Errorf("Claude is not installed")
	}
	id := scope.ID
	if id == "" {
		id = "claude"
	}
	marketName := "aact-" + request.Key.ID()[:12] + "-" + manifest.Name
	pluginID := manifest.Name + "@" + marketName
	market := filepath.Join(a.deps.Store.Root(), "generated", request.Key.ID(), "plugins", id, manifest.Name)
	inventory, err := a.pluginInventory(ctx, scope)
	if err != nil {
		return PluginInstallResult{}, err
	}
	installed, exists := findNativePlugin(inventory, pluginID)
	source := filepath.Join(market, "plugins", manifest.Name)
	if err := refreshNativePluginSource(ctx, request.StagedDir, source); err != nil {
		return PluginInstallResult{}, err
	}
	if err := os.MkdirAll(filepath.Join(market, ".claude-plugin"), 0700); err != nil {
		return PluginInstallResult{}, err
	}
	descriptor := map[string]any{"name": marketName, "owner": map[string]string{"name": "AACT"}, "plugins": []map[string]string{{"name": manifest.Name, "source": "./plugins/" + manifest.Name}}}
	if err := state.WriteJSON(filepath.Join(market, ".claude-plugin", "marketplace.json"), descriptor); err != nil {
		return PluginInstallResult{}, err
	}
	result := PluginInstallResult{}
	marketplaceEffect := state.Installation{Key: request.Key, AgentID: id, AgentKind: "claude", AgentHome: a.scopedProbe(scope).Home, Component: "plugin-marketplace", SourcePath: market, Destination: market, Mode: "claude-code", ReleaseID: marketName}
	if !exists && !request.MarketplaceConfigured {
		if _, err := a.pluginCommand(ctx, scope, "marketplace", "add", market, "--scope", "user"); err != nil {
			return result, err
		}
		result.Effects = append(result.Effects, marketplaceEffect)
	} else if request.MarketplaceConfigured {
		result.Effects = append(result.Effects, marketplaceEffect)
	}
	if exists {
		if _, err := a.pluginCommand(ctx, scope, "uninstall", pluginID, "--scope", "user", "--keep-data"); err != nil {
			return result, err
		}
		if request.Existing != nil {
			result.Removed = append(result.Removed, *request.Existing)
		} else {
			result.Removed = append(result.Removed, state.Installation{Key: request.Key, AgentID: id, AgentKind: "claude", AgentHome: a.scopedProbe(scope).Home, Component: "plugin", SourcePath: market, Destination: installed.InstallPath, Mode: "claude-code", ReleaseID: pluginID})
		}
		if _, err := a.pluginCommand(ctx, scope, "marketplace", "update", marketName); err != nil {
			return result, err
		}
		if _, err := a.pluginCommand(ctx, scope, "install", pluginID, "--scope", "user"); err != nil {
			return result, err
		}
	} else {
		if _, err := a.pluginCommand(ctx, scope, "install", pluginID, "--scope", "user"); err != nil {
			return result, err
		}
	}
	inventory, err = a.pluginInventory(ctx, scope)
	if err != nil {
		return result, err
	}
	installed, exists = findNativePlugin(inventory, pluginID)
	if !exists {
		return result, fmt.Errorf("Claude plugin %s was not present after install", pluginID)
	}
	result.Installation = state.Installation{Key: request.Key, AgentID: id, AgentKind: "claude", AgentHome: a.scopedProbe(scope).Home, Component: "plugin", SourcePath: market, Destination: installed.InstallPath, Mode: "claude-code", ReleaseID: pluginID}
	return result, nil
}

func refreshNativePluginSource(ctx context.Context, staged, source string) error {
	parent := filepath.Dir(source)
	if err := os.MkdirAll(parent, 0700); err != nil {
		return err
	}
	temporary, err := os.MkdirTemp(parent, ".aact-plugin-stage-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temporary)
	if err := render.CopyTree(ctx, staged, temporary); err != nil {
		return err
	}
	backup := source + ".aact-previous"
	_ = os.RemoveAll(backup)
	if _, err := os.Stat(source); err == nil {
		if err := os.Rename(source, backup); err != nil {
			return err
		}
	}
	if err := os.Rename(temporary, source); err != nil {
		_ = os.Rename(backup, source)
		return err
	}
	return os.RemoveAll(backup)
}
func (a *claudePluginAdapter) RemovePlugin(ctx context.Context, scope Scope, row state.Installation) error {
	if row.Mode != "claude-code" || row.ReleaseID == "" {
		return fmt.Errorf("unsupported native plugin record")
	}
	id := scope.ID
	if id == "" {
		id = "claude"
	}
	if row.AgentID != id {
		return fmt.Errorf("plugin belongs to another agent scope")
	}
	if row.Component == "plugin-marketplace" {
		inventory, err := a.pluginInventory(ctx, scope)
		if err != nil {
			return err
		}
		for _, plugin := range inventory {
			if strings.HasSuffix(plugin.ID, "@"+row.ReleaseID) && plugin.Scope == "user" {
				return fmt.Errorf("Claude marketplace %s still has an installed plugin", row.ReleaseID)
			}
		}
		if _, err := a.pluginCommand(ctx, scope, "marketplace", "remove", row.ReleaseID, "--scope", "user"); err != nil {
			return err
		}
		return nil
	}
	inventory, err := a.pluginInventory(ctx, scope)
	if err != nil {
		return err
	}
	if _, exists := findNativePlugin(inventory, row.ReleaseID); !exists {
		return nil
	}
	if _, err := a.pluginCommand(ctx, scope, "uninstall", row.ReleaseID, "--scope", "user", "--keep-data"); err != nil {
		return err
	}
	inventory, err = a.pluginInventory(ctx, scope)
	if err != nil {
		return err
	}
	_, market, ok := strings.Cut(row.ReleaseID, "@")
	if !ok {
		return nil
	}
	for _, plugin := range inventory {
		if strings.HasSuffix(plugin.ID, "@"+market) {
			return nil
		}
	}
	if a.deps.Store != nil {
		rows, err := a.deps.Store.Installations()
		if err != nil {
			return err
		}
		for _, existing := range rows {
			if existing.Key == row.Key && existing.AgentID == row.AgentID && existing.Component == "plugin-marketplace" && existing.ReleaseID == market {
				return nil
			}
		}
	}
	_, err = a.pluginCommand(ctx, scope, "marketplace", "remove", market, "--scope", "user")
	return err
}
func (a *claudePluginAdapter) Observe(ctx context.Context, scope Scope, request ObservationRequest) (Observation, error) {
	result, err := a.mcpAdapter.Observe(ctx, scope, request)
	if err != nil {
		return result, err
	}
	var wanted []state.Installation
	id := scope.ID
	if id == "" {
		id = "claude"
	}
	for _, row := range request.Managed {
		if row.Component == "plugin" && row.AgentID == id {
			wanted = append(wanted, row)
		}
	}
	if len(wanted) == 0 && !request.IncludeInventory {
		return result, nil
	}
	inventory, readErr := a.pluginInventory(ctx, scope)
	seen := map[string]bool{}
	for _, row := range wanted {
		seen[row.ReleaseID] = true
		component := ComponentObservation{Kind: "plugin", Name: row.ReleaseID, Path: row.Destination, Status: "absent", Managed: true}
		if readErr != nil {
			component.Status = "unavailable"
			component.Error = readErr.Error()
		} else if _, exists := findNativePlugin(inventory, row.ReleaseID); exists {
			component.Status = "installed"
		}
		result.Components = append(result.Components, component)
	}
	if readErr != nil {
		result.Errors = append(result.Errors, readErr.Error())
	}
	if request.IncludeInventory {
		for _, plugin := range inventory {
			if !seen[plugin.ID] {
				result.Components = append(result.Components, ComponentObservation{Kind: "plugin", Name: plugin.ID, Path: plugin.InstallPath, Status: "installed"})
			}
		}
	}
	return result, nil
}
