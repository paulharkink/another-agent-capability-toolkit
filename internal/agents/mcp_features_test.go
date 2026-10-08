package agents

import (
	"context"
	"errors"
	"fmt"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type codexReplacementExecutor struct {
	configPath string
	calls      [][]string
	failAdd    bool
}

func (r *codexReplacementExecutor) Run(_ context.Context, args []string, _ string, _ []byte, _ map[string]string, onStderr func([]byte)) ([]byte, error) {
	r.calls = append(r.calls, append([]string(nil), args...))
	if len(args) >= 3 && args[1] == "mcp" {
		switch args[2] {
		case "get":
			contents, _ := os.ReadFile(r.configPath)
			if strings.Contains(string(contents), "[mcp_servers.local]") {
				return []byte(`{"name":"local","transport":{"type":"streamable_http","url":"http://original.example/mcp"}}`), nil
			}
			if onStderr != nil {
				onStderr([]byte("Error: No MCP server named 'local' found."))
			}
			return nil, errors.New("exit status 1")
		case "remove":
			return nil, os.WriteFile(r.configPath, []byte("model = 'untouched'\n\n[mcp_servers.unrelated]\nurl = 'http://unrelated.example/mcp'\n"), 0600)
		case "add":
			if r.failAdd {
				if onStderr != nil {
					onStderr([]byte("fixture CLI stderr token"))
				}
				return nil, errors.New("exit status 1")
			}
			return nil, os.WriteFile(r.configPath, []byte("model = 'untouched'\n\n[mcp_servers.local]\nurl = 'http://replacement.example/mcp'\n\n[mcp_servers.unrelated]\nurl = 'http://unrelated.example/mcp'\n"), 0600)
		}
	}
	return nil, nil
}

func TestMCPAdapterReportsRemovedOwnedRegistrationAfterFailedCLIReplacement(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e, err := ResolveEnvironment("codex", "codex", root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(e.ConfigPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(e.ConfigPath, []byte("model = 'untouched'\n\n[mcp_servers.local]\nurl = 'http://original.example/mcp'\n\n[mcp_servers.unrelated]\nurl = 'http://unrelated.example/mcp'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	key := state.Key{Source: "pack", Package: "demo", Target: "ota", MCP: "alpha"}
	adapter := &mcpAdapter{skillAdapter: &skillAdapter{registeredAdapter: &registeredAdapter{kind: "codex", deps: Dependencies{Store: store, Probe: DiscoveryProbe{GOOS: "linux", Home: root, Getenv: func(string) string { return "" }, LookPath: func(string) (string, error) { return "/fixture/codex", nil }}}}}}
	entries, err := adapter.readMCPEntries(e.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	old := state.Installation{Key: key, AgentID: "codex", AgentHome: root, AgentKind: "codex", Component: "mcp", Destination: e.ConfigPath, RegistrationName: "local", URL: "http://original.example/mcp", Mode: "registration", Digest: nativeEntryDigest(entries["local"])}
	if err := store.Record(old); err != nil {
		t.Fatal(err)
	}
	runner := &codexReplacementExecutor{configPath: e.ConfigPath, failAdd: true}
	adapter.deps.Runner = runner
	scope := Scope{ID: "codex", Home: root, ExplicitHome: true}
	request := MCPRequest{Key: key, Registration: Registration{Name: "local", URL: "http://replacement.example/mcp"}}
	result, err := adapter.Register(context.Background(), scope, request)
	if err == nil || !strings.Contains(err.Error(), "fixture CLI stderr token") {
		t.Fatalf("add stderr missing: %v", err)
	}
	if len(result.Removed) != 1 || result.Removed[0] != old {
		t.Fatalf("removed effect not reported: %+v", result)
	}
	wantCalls := [][]string{{"codex", "mcp", "get", "local", "--json"}, {"codex", "mcp", "remove", "local"}, {"codex", "mcp", "add", "local", "--url", "http://replacement.example/mcp"}}
	if !reflect.DeepEqual(runner.calls, wantCalls) {
		t.Fatalf("CLI call sequence = %#v", runner.calls)
	}
	contents, err := os.ReadFile(e.ConfigPath)
	if err != nil || !strings.Contains(string(contents), "untouched") || !strings.Contains(string(contents), "unrelated") || strings.Contains(string(contents), "mcp_servers.local") {
		t.Fatalf("partial config contents = %s, err=%v", contents, err)
	}
	if err := store.Remove(old); err != nil {
		t.Fatal(err)
	}
	runner.failAdd = false
	runner.calls = nil
	result, err = adapter.Register(context.Background(), scope, request)
	if err != nil || result.Installation.URL != request.Registration.URL || len(result.Removed) != 0 {
		t.Fatalf("retry registration = %+v, %v", result, err)
	}
	if err := store.Record(result.Installation); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(runner.calls, [][]string{{"codex", "mcp", "get", "local", "--json"}, {"codex", "mcp", "add", "local", "--url", request.Registration.URL}}) {
		t.Fatalf("retry calls = %#v", runner.calls)
	}
	contents, err = os.ReadFile(e.ConfigPath)
	if err != nil || !strings.Contains(string(contents), "untouched") || !strings.Contains(string(contents), "unrelated") || !strings.Contains(string(contents), "replacement.example") {
		t.Fatalf("successful replacement damaged config: %s err=%v", contents, err)
	}
	observed, err := adapter.Observe(context.Background(), scope, ObservationRequest{Key: key, Managed: []state.Installation{result.Installation}})
	if err != nil || len(observed.Components) != 1 || observed.Components[0].Status != "installed" {
		t.Fatalf("successful replacement observation = %+v, %v", observed, err)
	}
}

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
	result, err := manager.Register(ctx, scope, MCPRequest{Key: key, Registration: Registration{Name: "guidance-ota", URL: "http://127.0.0.1:19876/mcp", Transport: "streamable-http"}})
	if err != nil {
		t.Fatal(err)
	}
	row := result.Installation
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
	result, err := manager.Register(context.Background(), scope, MCPRequest{Key: key, Registration: registration})
	if err != nil {
		t.Fatal(err)
	}
	row := result.Installation
	if err := store.Record(row); err != nil {
		t.Fatal(err)
	}
	registration.Headers["Authorization"] = "Bearer fixture-new"
	update, err := manager.Register(context.Background(), scope, MCPRequest{Key: key, Registration: registration})
	if err != nil {
		t.Fatal("owned token could not be changed:", err)
	}
	updated := update.Installation
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
