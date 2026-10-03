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

func TestConfigureRegistrationsForForeignMCPOnlyChangesLocalAgent(t *testing.T) {
	svc, localAgent, store := fixture(t)
	svc.Source.Catalog[0].MCP = &catalog.MCP{Name: "demo", Transport: "streamable-http"}
	runtime := &fakeRuntime{}
	svc.Options.Runtime = runtime
	localAgent.Kind = "generic"
	localAgent.ConfigPath = filepath.Join(localAgent.Home, "mcp.json")
	key := state.Key{Source: "windows-source", Package: "demo", Environment: "prod", Target: "cluster"}

	out, err := svc.ConfigureRegistrations(context.Background(), RegistrationRequest{
		Key: key, URL: "http://127.0.0.1:8765/mcp", Transport: "streamable-http",
		Agents: []agents.Environment{localAgent},
	})
	if err != nil {
		t.Fatal(err)
	}
	if runtime.starts != 0 {
		t.Fatal("registration started Docker")
	}
	if len(out.Changes) != 1 || out.Changes[0].Component != "mcp" || out.Changes[0].Key != key {
		t.Fatalf("expected one foreign MCP registration: %+v", out)
	}
	rows, err := store.Installations()
	if err != nil || len(rows) != 1 || rows[0].Component != "mcp" {
		t.Fatalf("registration altered other installation state: %+v, %v", rows, err)
	}
	if _, err := os.Stat(localAgent.SkillsDir); !os.IsNotExist(err) {
		t.Fatalf("registration installed a skill: %v", err)
	}
	config, err := os.ReadFile(localAgent.ConfigPath)
	if err != nil || !strings.Contains(string(config), "127.0.0.1:8765/mcp") {
		t.Fatalf("local agent lacks endpoint: %s, %v", config, err)
	}
}

func TestConfigureRegistrationsUsesLocalPackageTimeout(t *testing.T) {
	svc, agent, store := fixture(t)
	svc.Source.Catalog[0].MCP = &catalog.MCP{Name: "demo", Transport: "streamable-http", RegistrationTimeoutMS: 60000}
	agent.Kind = "generic"
	agent.ConfigPath = filepath.Join(agent.Home, "mcp.json")
	key := state.Key{Source: svc.Source.ID, Package: "demo", Target: "default"}
	if _, err := svc.ConfigureRegistrations(context.Background(), RegistrationRequest{
		Key: key, URL: "http://127.0.0.1:8765/mcp", Transport: "streamable-http",
		Agents: []agents.Environment{agent},
	}); err != nil {
		t.Fatal(err)
	}
	rows, err := store.Installations()
	if err != nil || len(rows) != 1 || rows[0].TimeoutMS != 60000 {
		t.Fatalf("MCP registration timeout: %+v, %v", rows, err)
	}
}

func TestConfigureRegistrationsRemovesDeselectedOwnedAgent(t *testing.T) {
	svc, first, store := fixture(t)
	first.Kind = "generic"
	first.ConfigPath = filepath.Join(first.Home, "mcp.json")
	second := first
	second.ID = "generic:second"
	second.Home = t.TempDir()
	second.ConfigPath = filepath.Join(second.Home, "mcp.json")
	key := state.Key{Source: "windows-source", Package: "demo", Target: "cluster"}
	request := RegistrationRequest{Key: key, URL: "http://127.0.0.1:8765/mcp", Transport: "streamable-http", Agents: []agents.Environment{first, second}}
	if _, err := svc.ConfigureRegistrations(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	request.Agents = []agents.Environment{second}
	out, err := svc.ConfigureRegistrations(context.Background(), request)
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
	agent.Kind = "generic"
	agent.ConfigPath = filepath.Join(agent.Home, "mcp.json")
	key := state.Key{Source: "windows-source", Package: "demo", Target: "cluster"}
	request := RegistrationRequest{Key: key, URL: "http://127.0.0.1:8765/mcp", Transport: "streamable-http", Agents: []agents.Environment{agent}}
	if _, err := svc.ConfigureRegistrations(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	request.Agents = nil
	if _, err := svc.ConfigureRegistrations(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	rows, err := store.Installations()
	if err != nil || len(rows) != 0 {
		t.Fatalf("owned registration survived empty desired state: %+v, %v", rows, err)
	}
}

func TestInstallRecordsMCPProfileIndependentlyOfRuntime(t *testing.T) {
	svc, localAgent, store := fixture(t)
	svc.Source.Catalog[0].MCP = &catalog.MCP{Name: "demo", Transport: "streamable-http"}
	localAgent.Kind = "generic"
	localAgent.ConfigPath = filepath.Join(localAgent.Home, "mcp.json")
	_, err := svc.Install(context.Background(), InstallRequest{
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
