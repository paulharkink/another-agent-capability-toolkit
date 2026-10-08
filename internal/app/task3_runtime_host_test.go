package app

import (
	"context"
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
)

func TestStartConfigurationProfileUsesDeclaredRuntimeHostInputs(t *testing.T) {
	svc, _, _ := fixture(t)
	runtime := &providerTestRuntime{}
	svc.Options.Runtime = runtime
	pkg := catalog.Package{ID: "fixture", Dir: t.TempDir(), MCP: &catalog.MCP{
		Name: "fixture", Image: "fixture", Transport: "streamable-http", HostPortInput: "listen_port", ContainerPort: 80,
		BindIPInput: "docker_interface", AdvertisedHostInput: "client_dns_name", EndpointPath: "/mcp",
	}}
	key := state.Key{Source: "fixture", Package: "fixture", Target: "default"}
	_, err := svc.startConfigurationProfile(context.Background(), pkg, config.Profile{}, key, map[string]any{
		"listen_port": int64(8765), "docker_interface": "127.0.0.2", "client_dns_name": "mcp.example.test",
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(runtime.specs) != 1 {
		t.Fatalf("runtime start count = %d", len(runtime.specs))
	}
	got := runtime.specs[0]
	if got.BindIP != "127.0.0.2" || got.AdvertisedHost != "mcp.example.test" || got.Host != "" {
		t.Fatalf("declared runtime host inputs were not applied independently: %+v", got)
	}
}

func TestStartConfigurationProfileIgnoresUndeclaredHostInput(t *testing.T) {
	svc, _, _ := fixture(t)
	runtime := &providerTestRuntime{}
	svc.Options.Runtime = runtime
	pkg := catalog.Package{ID: "fixture", Dir: t.TempDir(), MCP: &catalog.MCP{
		Name: "fixture", Image: "fixture", Transport: "streamable-http", HostPortInput: "listen_port", ContainerPort: 80,
		EndpointPath: "/mcp",
	}}
	key := state.Key{Source: "fixture", Package: "fixture", Target: "default"}
	_, err := svc.startConfigurationProfile(context.Background(), pkg, config.Profile{}, key, map[string]any{
		"listen_port": int64(8765), "host": "mcp.undeclared.example.test",
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(runtime.specs) != 1 {
		t.Fatalf("runtime start count = %d", len(runtime.specs))
	}
	got := runtime.specs[0]
	if got.Host != "" || got.BindIP != "" || got.AdvertisedHost != "" {
		t.Fatalf("undeclared host input affected MCP runtime: %+v", got)
	}
}
