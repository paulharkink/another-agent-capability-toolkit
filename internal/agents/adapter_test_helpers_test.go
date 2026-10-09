package agents

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/process"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
)

type mcpTestHarness struct {
	*mcpAdapter
	t *testing.T
}

func managerAdapterForTest(t *testing.T, kind string, runner process.Executor, home string) *mcpTestHarness {
	t.Helper()
	storeRoot := t.TempDir()
	store, err := state.Open(storeRoot)
	if err != nil {
		t.Fatal(err)
	}
	probe := DiscoveryProbe{
		GOOS: runtime.GOOS, Home: home, Getenv: os.Getenv,
		LookPath: func(name string) (string, error) { return filepath.Join(storeRoot, "bin", name), nil },
	}
	return &mcpTestHarness{mcpAdapter: &mcpAdapter{skillAdapter: &skillAdapter{registeredAdapter: &registeredAdapter{
		kind: kind,
		deps: Dependencies{Store: store, Runner: runner, Probe: probe},
	}}}, t: t}
}

var managerTestKey = state.Key{Source: "adapter-test", Package: "fixture", Target: "profile"}

func recordTestOwned(t *testing.T, adapter *mcpAdapter, env Environment) {
	t.Helper()
	ownedEnv, err := adapter.registrationScope(Scope{
		ID: env.ID, Home: env.Home, ConfigPathOverride: env.ConfigPath, ExplicitHome: true,
	}, managerTestKey)
	if err != nil {
		t.Fatal(err)
	}
	for name, registration := range env.Owned {
		row := state.Installation{
			Key: managerTestKey, AgentID: env.ID, AgentHome: env.Home, AgentKind: env.Kind,
			Component: "mcp", Destination: ownedEnv.ConfigPath, RegistrationName: name,
			URL: registration.URL, Transport: registration.Transport,
			TimeoutMS: registration.TimeoutMS, Mode: "registration",
		}
		if entries, err := adapter.readMCPEntries(ownedEnv.ConfigPath); err == nil {
			if entry, ok := entries[name]; ok {
				row.Digest = nativeEntryDigest(entry)
			}
		}
		if err := adapter.deps.Store.Record(row); err != nil {
			t.Fatal(err)
		}
	}
}

func (h *mcpTestHarness) Register(ctx context.Context, env Environment, registration Registration) error {
	return registerThroughManager(h, ctx, env, registration)
}

func (h *mcpTestHarness) Unregister(ctx context.Context, env Environment, name string) error {
	return unregisterThroughManager(h, ctx, env, name)
}

func (h *mcpTestHarness) current(ctx context.Context, env Environment, name string) (*Registration, error) {
	return h.mcpAdapter.current(ctx, env, name)
}

func registerThroughManager(h *mcpTestHarness, ctx context.Context, env Environment, registration Registration) error {
	h.t.Helper()
	recordTestOwned(h.t, h.mcpAdapter, env)
	result, err := h.mcpAdapter.Register(ctx, Scope{
		ID: env.ID, Home: env.Home, ConfigPathOverride: env.ConfigPath, ExplicitHome: true,
	}, MCPRequest{Key: managerTestKey, Registration: registration})
	for _, row := range result.Removed {
		if removeErr := h.deps.Store.Remove(row); removeErr != nil {
			h.t.Fatal(removeErr)
		}
	}
	if err != nil {
		return err
	}
	return h.deps.Store.Record(result.Installation)
}

func unregisterThroughManager(h *mcpTestHarness, ctx context.Context, env Environment, name string) error {
	h.t.Helper()
	recordTestOwned(h.t, h.mcpAdapter, env)
	rows, err := h.deps.Store.Installations()
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.Key == managerTestKey && row.Component == "mcp" && row.AgentID == env.ID && row.RegistrationName == name {
			err = h.mcpAdapter.Unregister(ctx, Scope{
				ID: env.ID, Home: env.Home, ConfigPathOverride: env.ConfigPath, ExplicitHome: true,
			}, row)
			if err != nil {
				return err
			}
			return h.deps.Store.Remove(row)
		}
	}
	return nil
}
