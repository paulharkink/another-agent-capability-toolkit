package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/tailscale/hujson"
)

// opencodePluginAdapter manages OpenCode's native V2 plugins array. It edits
// only that array and leaves unrelated settings and plugin entries intact.
type opencodePluginAdapter struct{ *mcpAdapter }

func (a *opencodePluginAdapter) Features() FeatureSet {
	return FeatureSet{Skills: true, MCPs: true, PluginFormats: []string{"opencode"}}
}

func (a *opencodePluginAdapter) InstallPlugin(ctx context.Context, scope Scope, q PluginRequest) (PluginInstallResult, error) {
	if q.Plugin.Format != "opencode" {
		return PluginInstallResult{}, fmt.Errorf("unsupported plugin format %q for OpenCode", q.Plugin.Format)
	}
	ref := strings.TrimSpace(q.Plugin.Reference)
	if ref == "" {
		if q.Plugin.Source != "" && q.StagedDir != "" && !filepath.IsAbs(q.Plugin.Source) && !strings.HasPrefix(q.Plugin.Source, "npm:") {
			ref = q.StagedDir
		} else {
			ref = q.Plugin.Source
		}
	}
	if ref == "" {
		ref = q.StagedDir
	}
	if ref == "" {
		return PluginInstallResult{}, fmt.Errorf("OpenCode plugin requires a package reference or local path")
	}
	if !filepath.IsAbs(ref) && strings.HasPrefix(ref, ".") {
		return PluginInstallResult{}, fmt.Errorf("OpenCode local plugin path must be absolute")
	}
	d, err := a.Detect(ctx, scope)
	if err != nil {
		return PluginInstallResult{}, err
	}
	if !d.Installed && scope.ConfigPathOverride == "" {
		return PluginInstallResult{}, fmt.Errorf("OpenCode is not installed")
	}
	path := d.ConfigPath
	if scope.ConfigPathOverride != "" {
		path = scope.ConfigPathOverride
	}
	owned := map[string]bool{}
	var previous []state.Installation
	if a.deps.Store != nil {
		rows, e := a.deps.Store.Installations()
		if e != nil {
			return PluginInstallResult{}, e
		}
		for _, row := range rows {
			if row.Component == "plugin" && row.Mode == "opencode-v2" && row.Key == q.Key && row.AgentID == scope.ID && row.Destination == path {
				owned[row.SourcePath] = true
				previous = append(previous, row)
			}
		}
	}
	if err := updateOpenCodePlugin(ctx, path, q.Plugin.Name, ref, q.Plugin.Options, owned); err != nil {
		return PluginInstallResult{}, err
	}
	id := scope.ID
	if id == "" {
		id = "opencode"
	}
	row := state.Installation{Key: q.Key, AgentID: id, AgentKind: "opencode", AgentHome: scope.Home, Component: "plugin", SourcePath: ref, Destination: path, Mode: "opencode-v2", ReleaseID: q.Plugin.Name + "@" + ref}
	result := PluginInstallResult{Installation: row}
	result.Removed = append(result.Removed, previous...)
	if q.Existing != nil && len(result.Removed) == 0 {
		result.Removed = append(result.Removed, *q.Existing)
	}
	return result, nil
}

func updateOpenCodePlugin(ctx context.Context, path, name, ref string, options map[string]any, owned map[string]bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if path == "" {
		return fmt.Errorf("OpenCode config path is unavailable")
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		data = []byte("{\"$schema\":\"https://opencode.ai/config.json\"}\n")
	} else if err != nil {
		return err
	}
	doc, err := standardJSON(data)
	if err != nil {
		return fmt.Errorf("invalid OpenCode config %s: %w", path, err)
	}
	plugins, ok := doc["plugins"].([]any)
	if exists := doc["plugins"]; exists != nil && !ok {
		return fmt.Errorf("OpenCode config plugins must be an array")
	}
	entry := map[string]any{"package": ref}
	if len(options) > 0 {
		entry["options"] = options
	}
	if _, exists := doc["plugins"]; !exists {
		patch, _ := json.Marshal([]map[string]any{{"op": "add", "path": "/plugins", "value": []any{entry}}})
		v, err := hujson.Parse(data)
		if err != nil {
			return err
		}
		if err = v.Patch(patch); err != nil {
			return err
		}
		return writeConfigAtomic(path, v.Pack())
	}
	ops := []map[string]any{}
	for i := len(plugins) - 1; i >= 0; i-- {
		p := plugins[i]
		pkg := ""
		switch v := p.(type) {
		case string:
			pkg = v
		case map[string]any:
			pkg, _ = v["package"].(string)
		}
		if pkg == ref || owned[pkg] {
			ops = append(ops, map[string]any{"op": "remove", "path": fmt.Sprintf("/plugins/%d", i)})
		}
	}
	ops = append(ops, map[string]any{"op": "add", "path": "/plugins/-", "value": entry})
	v, err := hujson.Parse(data)
	if err != nil {
		return err
	}
	patch, _ := json.Marshal(ops)
	if err := v.Patch(patch); err != nil {
		return err
	}
	return writeConfigAtomic(path, v.Pack())
}

func (a *opencodePluginAdapter) RemovePlugin(ctx context.Context, scope Scope, row state.Installation) error {
	if row.Mode != "opencode-v2" {
		return fmt.Errorf("unsupported OpenCode plugin record")
	}
	if scope.ConfigPathOverride != "" && scope.ConfigPathOverride != row.Destination {
		return fmt.Errorf("plugin belongs to another OpenCode config")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := os.ReadFile(row.Destination)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	doc, err := standardJSON(data)
	if err != nil {
		return err
	}
	plugins, ok := doc["plugins"].([]any)
	if !ok {
		return nil
	}
	ref := row.SourcePath
	if ref == "" {
		_, ref, _ = strings.Cut(row.ReleaseID, "@")
	}
	ops := []map[string]any{}
	for i := len(plugins) - 1; i >= 0; i-- {
		p := plugins[i]
		pkg := ""
		switch v := p.(type) {
		case string:
			pkg = v
		case map[string]any:
			pkg, _ = v["package"].(string)
		}
		if pkg != ref {
			continue
		}
		ops = append(ops, map[string]any{"op": "remove", "path": fmt.Sprintf("/plugins/%d", i)})
	}
	if len(ops) == 0 {
		return nil
	}
	v, err := hujson.Parse(data)
	if err != nil {
		return err
	}
	patch, _ := json.Marshal(ops)
	if err = v.Patch(patch); err != nil {
		return err
	}
	return writeConfigAtomic(row.Destination, v.Pack())
}

func writeConfigAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	mode := os.FileMode(0600)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".aact-config-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err = tmp.Chmod(mode); err == nil {
		_, err = tmp.Write(data)
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, path)
}
