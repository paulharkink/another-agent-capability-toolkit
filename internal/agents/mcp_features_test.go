package agents

import (
	"context"
	"fmt"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMCPAdapterMissingConfigCreateObserveUpdateRemove(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := NewRegistry(Dependencies{Store: store, Probe: DiscoveryProbe{GOOS: "linux", Home: root, Getenv: func(string) string { return "" }, LookPath: func(string) (string, error) { return "fixture-opencode", nil }}})
	a, _ := r.Adapter("opencode")
	manager, ok := a.(MCPManager)
	if !ok || !a.Features().MCPs {
		t.Fatal("MCP manager not implemented")
	}
	ctx := context.Background()
	scope := Scope{ID: "opencode", Home: root, ExplicitHome: true}
	key := state.Key{Source: "pack", Package: "guidance", Target: "ota"}
	row, err := manager.Register(ctx, scope, MCPRequest{Key: key, Registration: Registration{Name: "guidance-ota", URL: "http://127.0.0.1:19876/mcp", Transport: "streamable-http"}})
	if err != nil {
		t.Fatal(err)
	}
	if row.Destination != filepath.Join(root, ".config", "opencode", "opencode.jsonc") {
		t.Fatalf("missing config created in wrong format: %#v", row)
	}
	if err := store.Record(row); err != nil {
		t.Fatal(err)
	}
	observation, err := a.Observe(ctx, scope, ObservationRequest{Key: key, Managed: []state.Installation{row}})
	if err != nil || len(observation.Components) != 1 || observation.Components[0].Status != "installed" {
		t.Fatalf("registration observation: %#v %v", observation, err)
	}
	if err := os.WriteFile(row.Destination, []byte("{ // preserved comment\n\"other\":true,\"mcp\":{\"unrelated\":{\"type\":\"remote\",\"url\":\"http://localhost:1111/mcp\"},\"guidance-ota\":{\"type\":\"remote\",\"url\":\"http://localhost:9999/mcp\",\"enabled\":true,\"oauth\":false,\"timeout\":0}}}"), 0600); err != nil {
		t.Fatal(err)
	}
	observation, err = a.Observe(ctx, scope, ObservationRequest{Key: key, Managed: []state.Installation{row}})
	if err != nil || observation.Components[0].Status != "modified" {
		t.Fatalf("modified config reported intact: %#v %v", observation, err)
	}
	// Restore ownership's expected registration before exercising removal.
	text, err := os.ReadFile(row.Destination)
	if err != nil {
		t.Fatal(err)
	}
	text = []byte(strings.ReplaceAll(string(text), "http://localhost:9999/mcp", row.URL))
	if err := os.WriteFile(row.Destination, text, 0600); err != nil {
		t.Fatal(err)
	}
	if err := manager.Unregister(ctx, scope, row); err != nil {
		t.Fatal(err)
	}
	text, err = os.ReadFile(row.Destination)
	if err != nil || !strings.Contains(string(text), "unrelated") || !strings.Contains(string(text), "preserved comment") || strings.Contains(string(text), "guidance-ota") {
		t.Fatalf("unrelated config damaged: %s %v", text, err)
	}
	observation, err = a.Observe(ctx, scope, ObservationRequest{Key: key, Managed: []state.Installation{row}})
	if err != nil || observation.Components[0].Status != "absent" {
		t.Fatalf("stale registration ledger reported installed: %#v %v", observation, err)
	}
}

func TestMCPAdapterOwnedAuthenticationHeaderCanBeUpdated(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := NewRegistry(Dependencies{Store: store, Probe: DiscoveryProbe{GOOS: "linux", Home: root, Getenv: func(string) string { return "" }, LookPath: func(string) (string, error) { return "fixture-opencode", nil }}})
	a, _ := r.Adapter("opencode")
	manager := a.(MCPManager)
	scope := Scope{ID: "opencode", Home: root, ExplicitHome: true}
	key := state.Key{Source: "pack", Package: "guidance", Target: "ota"}
	registration := Registration{Name: "guidance", URL: "http://localhost:1234/mcp", Transport: "streamable-http", Headers: map[string]string{"Authorization": "Bearer fixture-old"}}
	row, err := manager.Register(context.Background(), scope, MCPRequest{Key: key, Registration: registration})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Record(row); err != nil {
		t.Fatal(err)
	}
	registration.Headers["Authorization"] = "Bearer fixture-new"
	updated, err := manager.Register(context.Background(), scope, MCPRequest{Key: key, Registration: registration})
	if err != nil {
		t.Fatal("owned token could not be changed:", err)
	}
	if err := store.Record(updated); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(updated.Destination)
	if err != nil || !strings.Contains(string(data), "fixture-new") || strings.Contains(string(data), "fixture-old") {
		t.Fatal("updated auth header not written")
	}
	if err := manager.Unregister(context.Background(), scope, updated); err != nil {
		t.Fatal("owned authenticated registration could not be removed:", err)
	}
}

func TestMCPAdapterAbsentAgentDoesNotCreateConfig(t *testing.T) {
	root := t.TempDir()
	r := NewRegistry(Dependencies{Probe: DiscoveryProbe{GOOS: "linux", Home: root, Getenv: func(string) string { return "" }, LookPath: func(string) (string, error) { return "", fmt.Errorf("not installed") }}})
	a, _ := r.Adapter("opencode")
	_, err := a.(MCPManager).Register(context.Background(), Scope{Home: root, ExplicitHome: true}, MCPRequest{Registration: Registration{Name: "guidance", URL: "http://localhost:1234/mcp"}})
	if err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("absent agent not rejected: %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatal("absent agent operation wrote config")
	}
}

func TestMCPAdapterMalformedConfigObservationReportsError(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "opencode.jsonc")
	if err := os.WriteFile(path, []byte(`{"mcp":{"guidance":{"url":"http://one"},"guidance":{"url":"http://two"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	r := NewRegistry(Dependencies{Probe: DiscoveryProbe{GOOS: "linux", Home: root, Getenv: func(string) string { return "" }, LookPath: func(string) (string, error) { return "fixture-opencode", nil }}})
	a, _ := r.Adapter("opencode")
	result, err := a.Observe(context.Background(), Scope{ID: "opencode", Home: root, ConfigPathOverride: path, ExplicitHome: true}, ObservationRequest{Managed: []state.Installation{{AgentID: "opencode", Component: "mcp", Destination: path, RegistrationName: "guidance", URL: "http://one"}}})
	if err != nil || len(result.Components) != 1 || result.Components[0].Status != "unavailable" || result.Components[0].Error == "" {
		t.Fatalf("ambiguous config falsely reported registered: %#v %v", result, err)
	}
}
