package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/agents"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
)

type managerOnlyRegistrationAdapter struct {
	registerCalls   int
	unregisterCalls int
	lastRequest     agents.MCPRequest
}

func (a *managerOnlyRegistrationAdapter) ID() string   { return "opencode" }
func (a *managerOnlyRegistrationAdapter) Name() string { return "manager fixture" }
func (a *managerOnlyRegistrationAdapter) Features() agents.FeatureSet {
	return agents.FeatureSet{MCPs: true}
}
func (a *managerOnlyRegistrationAdapter) Detect(_ context.Context, scope agents.Scope) (agents.Detection, error) {
	return agents.Detection{Home: scope.Home, ConfigPath: scope.ConfigPathOverride, Installed: true, State: "installed", CanCreateConfig: true}, nil
}
func (a *managerOnlyRegistrationAdapter) Observe(context.Context, agents.Scope, agents.ObservationRequest) (agents.Observation, error) {
	return agents.Observation{}, nil
}
func (a *managerOnlyRegistrationAdapter) Register(_ context.Context, scope agents.Scope, request agents.MCPRequest) (agents.MCPRegistrationResult, error) {
	a.registerCalls++
	a.lastRequest = request
	return agents.MCPRegistrationResult{Installation: state.Installation{
		Key: request.Key, AgentID: scope.ID, AgentHome: scope.Home, AgentKind: "opencode", Component: "mcp",
		Destination: scope.ConfigPathOverride, RegistrationName: request.Registration.Name,
		URL: request.Registration.URL, Transport: request.Registration.Transport,
		TimeoutMS: request.Registration.TimeoutMS, Mode: "registration",
	}}, nil
}
func (a *managerOnlyRegistrationAdapter) Unregister(context.Context, agents.Scope, state.Installation) error {
	a.unregisterCalls++
	return nil
}

type managerOnlyRegistrationRegistry struct {
	AdapterProvider
	adapter agents.Adapter
}

func (r managerOnlyRegistrationRegistry) Adapter(id string) (agents.Adapter, error) {
	if id == r.adapter.ID() {
		return r.adapter, nil
	}
	return r.AdapterProvider.Adapter(id)
}
func (r managerOnlyRegistrationRegistry) Adapters() []agents.Adapter {
	return append(r.AdapterProvider.Adapters(), r.adapter)
}

func TestConfigureRegistrationsUsesSelectedAdapterMCPManager(t *testing.T) {
	svc, agent, store := fixture(t)
	svc.Source.Catalog[0].MCP = &catalog.MCP{Name: "demo", Transport: "streamable-http"}
	agent.ID, agent.Kind = "opencode", "opencode"
	agent.ConfigPath = filepath.Join(t.TempDir(), "adapter-owned.jsonc")
	manager := &managerOnlyRegistrationAdapter{}
	svc.Options.Adapters = managerOnlyRegistrationRegistry{AdapterProvider: svc.adapterRegistry(), adapter: manager}
	key := state.Key{Source: svc.Source.ID, Package: "demo", Target: "profile"}

	result, err := configureRegistrationsTest(context.Background(), svc, RegistrationRequest{
		Key: key, URL: "http://localhost:8765/mcp", Transport: "streamable-http", Agents: []agents.Environment{agent},
	})
	if err != nil {
		t.Fatal(err)
	}
	if manager.registerCalls != 1 || manager.lastRequest.Key != key || manager.lastRequest.Registration.Name != registrationName(key) {
		t.Fatalf("selected adapter manager was not given the MCP request: calls=%d request=%+v", manager.registerCalls, manager.lastRequest)
	}
	if len(result.Changes) != 1 || result.Changes[0].Destination != agent.ConfigPath {
		t.Fatalf("manager installation was not recorded as the applied effect: %+v", result.Changes)
	}
	if _, err := os.Stat(agent.ConfigPath); !os.IsNotExist(err) {
		t.Fatalf("app bypassed adapter MCP manager and wrote a config file: err=%v", err)
	}
	rows, err := store.Installations()
	if err != nil || len(rows) != 1 || rows[0].Destination != agent.ConfigPath {
		t.Fatalf("manager result was not persisted: rows=%+v err=%v", rows, err)
	}
}

func TestConfigureRegistrationsForForeignMCPOnlyChangesLocalAgent(t *testing.T) {
	svc, localAgent, store := fixture(t)
	svc.Source.Catalog[0].MCP = &catalog.MCP{Name: "demo", Transport: "streamable-http"}
	runtime := &fakeRuntime{}
	svc.Options.Runtime = runtime
	localAgent.Kind = "opencode"
	localAgent.ConfigPath = filepath.Join(localAgent.Home, "mcp.jsonc")
	key := state.Key{Source: svc.Source.ID, Package: "demo", Target: "cluster"}

	out, err := configureRegistrationsTest(context.Background(), svc, RegistrationRequest{
		Key: key, URL: "http://127.0.0.1:8765/mcp", Transport: "streamable-http",
		Agents: []agents.Environment{localAgent},
	})
	if err != nil {
		t.Fatal(err)
	}
	if runtime.starts != 0 {
		t.Fatal("registration started Docker")
	}
	if len(out.Changes) != 1 || out.Changes[0].Component != "mcp" || out.Changes[0].Key.Package != key.Package {
		t.Fatalf("expected one foreign MCP registration: %+v", out)
	}
	rows, err := store.Installations()
	if err != nil || len(rows) != 1 || rows[0].Component != "mcp" {
		t.Fatalf("registration altered other installation state: %+v, %v", rows, err)
	}
	if !rows[0].ExternalRegistration {
		t.Fatalf("explicit external attach did not retain its provenance: %+v", rows[0])
	}
	if _, err := os.Stat(localAgent.SkillsDir); !os.IsNotExist(err) {
		t.Fatalf("registration installed a skill: %v", err)
	}
	config, err := os.ReadFile(localAgent.ConfigPath)
	if err != nil || !strings.Contains(string(config), "127.0.0.1:8765/mcp") {
		t.Fatalf("local agent lacks endpoint: %s, %v", config, err)
	}
}

func TestConfigureRegistrationsForLocalCapabilityOnlyAttachesMCP(t *testing.T) {
	svc, agent, store := fixture(t)
	svc.Source.Catalog[0].MCP = &catalog.MCP{Name: "demo", Transport: "streamable-http"}
	agent.Kind = "opencode"
	agent.ConfigPath = filepath.Join(agent.Home, "mcp.jsonc")
	key := state.Key{Source: svc.Source.ID, Package: "demo", Target: "default"}
	result, err := configureRegistrationsTest(context.Background(), svc, RegistrationRequest{
		Key: key, URL: "http://127.0.0.1:8765/mcp", Transport: "streamable-http", Agents: []agents.Environment{agent},
	})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := store.Installations()
	if err != nil {
		t.Fatal(err)
	}
	components := map[string]bool{}
	for _, row := range rows {
		components[row.Component] = true
	}
	if components["skill"] || !components["mcp"] || len(result.Changes) != 1 {
		t.Fatalf("registration operation changed profile skill state: result=%+v rows=%+v", result, rows)
	}
}

func TestConfigureRegistrationsUsesLocalPackageTimeout(t *testing.T) {
	svc, agent, store := fixture(t)
	svc.Source.Catalog[0].MCP = &catalog.MCP{Name: "demo", Transport: "streamable-http", RegistrationTimeoutMS: 60000}
	agent.Kind = "opencode"
	agent.ConfigPath = filepath.Join(agent.Home, "mcp.jsonc")
	key := state.Key{Source: svc.Source.ID, Package: "demo", Target: "default"}
	if _, err := configureRegistrationsTest(context.Background(), svc, RegistrationRequest{
		Key: key, URL: "http://127.0.0.1:8765/mcp", Transport: "streamable-http",
		Agents: []agents.Environment{agent},
	}); err != nil {
		t.Fatal(err)
	}
	rows, err := store.Installations()
	if err != nil || len(rows) != 1 {
		t.Fatalf("MCP registration timeout: %+v, %v", rows, err)
	}
	for _, row := range rows {
		if row.Component == "mcp" && row.TimeoutMS != 60000 {
			t.Fatalf("MCP registration timeout: %+v", row)
		}
	}
}

func TestConfigureRegistrationsRemovesDeselectedOwnedAgent(t *testing.T) {
	svc, first, store := fixture(t)
	first.Kind = "opencode"
	first.ConfigPath = filepath.Join(first.Home, "mcp.jsonc")
	second := first
	second.ID = "generic:second"
	second.Home = t.TempDir()
	second.ConfigPath = filepath.Join(second.Home, "mcp.jsonc")
	key := state.Key{Source: svc.Source.ID, Package: "demo", Target: "cluster"}
	request := RegistrationRequest{Key: key, URL: "http://127.0.0.1:8765/mcp", Transport: "streamable-http", Agents: []agents.Environment{first, second}}
	if _, err := configureRegistrationsTest(context.Background(), svc, request); err != nil {
		t.Fatal(err)
	}
	request.Agents = []agents.Environment{second}
	out, err := configureRegistrationsTest(context.Background(), svc, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Changes) != 1 || out.Changes[0].AgentID != first.ID {
		t.Fatalf("reconciliation touched an unchanged registration: %+v", out.Changes)
	}
	rows, err := store.Installations()
	if err != nil || len(rows) != 1 || rows[0].AgentID != second.ID {
		t.Fatalf("deselected agent still owned: %+v, %v", rows, err)
	}
	content, err := os.ReadFile(first.ConfigPath)
	if err != nil || strings.Contains(string(content), registrationName(key)) {
		t.Fatalf("deselected agent still configured: %s, %v", content, err)
	}
}

func TestConfigureRegistrationsCanRemoveEveryOwnedAgent(t *testing.T) {
	svc, agent, store := fixture(t)
	agent.Kind = "opencode"
	agent.ConfigPath = filepath.Join(agent.Home, "mcp.jsonc")
	key := state.Key{Source: svc.Source.ID, Package: "demo", Target: "cluster"}
	request := RegistrationRequest{Key: key, URL: "http://127.0.0.1:8765/mcp", Transport: "streamable-http", Agents: []agents.Environment{agent}}
	if _, err := configureRegistrationsTest(context.Background(), svc, request); err != nil {
		t.Fatal(err)
	}
	request.Agents = nil
	if _, err := configureRegistrationsTest(context.Background(), svc, request); err != nil {
		t.Fatal(err)
	}
	rows, err := store.Installations()
	if err != nil || len(rows) != 0 {
		t.Fatalf("owned registration survived empty desired state: %+v, %v", rows, err)
	}
}

func TestConfigureRegistrationsPreservesLocalRuntimeIntent(t *testing.T) {
	svc, agent, store := fixture(t)
	agent.Kind = "opencode"
	agent.ConfigPath = filepath.Join(t.TempDir(), "mcp.jsonc")
	key := state.Key{Source: "fixture", Package: "demo", Environment: "sample-env", Target: "target-a"}
	if err := store.Record(state.Installation{Key: key, AgentID: "docker", Component: "runtime", Mode: "docker", Destination: "aact-target-a", SourcePath: "missing-container"}); err != nil {
		t.Fatal(err)
	}
	if _, err := configureRegistrationsTest(context.Background(), svc, RegistrationRequest{
		Key: key, URL: "http://127.0.0.1:8765/mcp", Transport: "streamable-http", Agents: []agents.Environment{agent},
	}); err != nil {
		t.Fatal(err)
	}
	rows, err := store.Installations()
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Key == key && row.Component == "mcp" && row.AgentID == agent.ID {
			if row.ExternalRegistration {
				t.Fatalf("attaching an agent to a local, now-missing runtime changed target provenance: %+v", row)
			}
			return
		}
	}
	t.Fatalf("registration missing after local attach: %+v", rows)
}

func TestInstallRecordsMCPProfileIndependentlyOfRuntime(t *testing.T) {
	svc, localAgent, store := fixture(t)
	svc.Source.Catalog[0].MCP = &catalog.MCP{Name: "demo", Transport: "streamable-http"}
	localAgent.Kind = "opencode"
	localAgent.ConfigPath = filepath.Join(localAgent.Home, "mcp.jsonc")
	_, err := svc.applyProfileFixture(context.Background(), InstallRequest{
		Package: "demo", Agents: []agents.Environment{localAgent}, ExternalURL: "http://127.0.0.1:8765/mcp",
	})
	if err != nil {
		t.Fatal(err)
	}
	profiles, err := store.Profiles()
	if err != nil || len(profiles) != 1 || profiles[0].Key != (state.Key{Source: "fixture", Package: "demo", Target: "default"}) {
		t.Fatalf("configured MCP profile missing: %+v, %v", profiles, err)
	}
}
