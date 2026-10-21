package polyrepo

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/agents"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/process"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
)

func OpenCodeHome(homes []string) string {
	for _, v := range homes {
		k, p, ok := strings.Cut(v, "=")
		if ok && k == "opencode" {
			return p
		}
	}
	return ""
}

func Apply(ctx context.Context, store *state.Store, source, configPath, agentHome string, logs io.Writer) (string, int, string, string, error) {
	if configPath == "" || agentHome == "" {
		return "", 0, "", "", fmt.Errorf("polyrepo apply requires explicit OpenCode config and agent-home paths")
	}
	inv, err := Refresh(ctx, store.Root())
	if err != nil {
		return "", 0, "", "", err
	}
	runner := process.OSExecutor{}
	entry, revision, err := BuildCourier(ctx, store.Root(), source, runner, func(b []byte) {
		if logs != nil {
			_, _ = logs.Write(b)
		}
	})
	if err != nil {
		return "", 0, "", "", err
	}
	registry := agents.NewRegistry(agents.Dependencies{Store: store, Runner: runner})
	adapter, err := registry.Adapter("opencode")
	if err != nil {
		return "", 0, "", "", err
	}
	key := state.Key{Source: "builtin", Package: "polyrepo-coordinator", Environment: "local", Target: "opencode"}
	plugin := catalog.Plugin{Name: "polyrepo-coordinator", Format: "opencode", Reference: entry, Options: map[string]any{"repoMapFile": filepath.Join(store.Root(), InventoryName)}}
	manager := adapter.(agents.PluginManager)
	result, err := manager.InstallPlugin(ctx, agents.Scope{ID: "opencode", Home: agentHome, ConfigPathOverride: configPath, ExplicitHome: true}, agents.PluginRequest{Key: key, Plugin: plugin})
	if err != nil {
		return "", 0, "", "", err
	}
	for _, old := range result.Removed {
		if err = store.Remove(old); err != nil {
			return "", 0, "", "", err
		}
	}
	if err = store.Record(result.Installation); err != nil {
		return "", 0, "", "", err
	}
	return entry, len(inv.Repositories), inv.NodeID, revision, nil
}
