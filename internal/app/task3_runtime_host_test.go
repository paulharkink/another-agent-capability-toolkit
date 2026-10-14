package app

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/mcp"
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

func TestStartConfigurationProfileResolvesConditionalPortsAndStateMount(t *testing.T) {
	svc, _, _ := fixture(t)
	runtime := &providerTestRuntime{}
	svc.Options.Runtime = runtime
	pkg := catalog.Package{ID: "fixture", Dir: t.TempDir(), Inputs: []catalog.Input{
		{Name: "mode", Type: "string"},
		{Name: "network_enabled", Type: "boolean"},
		{Name: "callback_enabled", Type: "boolean", VisibleWhen: map[string]any{"mode": "sso"}},
		{Name: "callback_port", Type: "integer", VisibleWhen: map[string]any{"callback_enabled": true}},
	}, MCP: &catalog.MCP{
		Name: "fixture", Image: "fixture", Transport: "streamable-http", HostPortInput: "listen_port", ContainerPort: 80,
		AdditionalPorts: []catalog.ConditionalPort{{ContainerPort: 7443, HostPortInput: "callback_port", When: map[string]any{"network_enabled": true}}},
	}}
	key := state.Key{Source: "fixture", Package: "fixture", Target: "default", Profile: "sso-profile"}
	values := map[string]any{"mode": "sso", "callback_enabled": true, "callback_port": json.Number("17443"), "listen_port": int64(8765)}
	values["network_enabled"] = true
	if _, err := svc.startConfigurationProfile(context.Background(), pkg, config.Profile{}, key, values, false); err != nil {
		t.Fatal(err)
	}
	if len(runtime.specs) != 1 {
		t.Fatalf("runtime start count = %d", len(runtime.specs))
	}
	got := runtime.specs[0]
	wantKey := state.Key{Source: key.Source, Package: key.Package, Target: key.Target, Profile: key.Profile}
	wantMount := mcp.Mount{Source: svc.Store.AuthDir(wantKey), Destination: "/state", ReadOnly: false}
	if !reflect.DeepEqual(got.AdditionalPorts, []mcp.PortMapping{{HostPort: 17443, ContainerPort: 7443}}) {
		t.Fatalf("conditional mappings = %#v", got.AdditionalPorts)
	}
	if !reflect.DeepEqual(got.Mounts, []mcp.Mount{wantMount}) {
		t.Fatalf("state mounts = %#v, want %#v", got.Mounts, []mcp.Mount{wantMount})
	}
	runtime.specs = nil
	values["mode"] = "token" // the port input is hidden, although the port condition matches
	values["callback_enabled"] = false
	if _, err := svc.startConfigurationProfile(context.Background(), pkg, config.Profile{}, key, values, false); err != nil {
		t.Fatal(err)
	}
	if len(runtime.specs) != 1 || len(runtime.specs[0].AdditionalPorts) != 0 {
		t.Fatalf("disabled conditional mapping was resolved: %+v", runtime.specs)
	}
	runtime.specs = nil
	values["mode"] = "sso" // the input is visible, although the port condition does not match
	values["callback_enabled"] = true
	values["network_enabled"] = false
	if _, err := svc.startConfigurationProfile(context.Background(), pkg, config.Profile{}, key, values, false); err != nil {
		t.Fatal(err)
	}
	if len(runtime.specs) != 1 || len(runtime.specs[0].AdditionalPorts) != 0 {
		t.Fatalf("unmatched port condition was resolved: %+v", runtime.specs)
	}

	for _, invalid := range []map[string]any{
		{"mode": "sso", "network_enabled": true, "callback_enabled": true, "listen_port": int64(8765)},
		{"mode": "sso", "network_enabled": true, "callback_enabled": true, "callback_port": int64(70000), "listen_port": int64(8765)},
		{"mode": "sso", "network_enabled": true, "callback_enabled": true, "callback_port": json.Number("17443.5"), "listen_port": int64(8765)},
		{"mode": "sso", "network_enabled": true, "callback_enabled": true, "callback_port": "not-a-port", "listen_port": int64(8765)},
	} {
		runtime.specs = nil
		if _, err := svc.startConfigurationProfile(context.Background(), pkg, config.Profile{}, key, invalid, false); err == nil {
			t.Fatalf("invalid conditional port values accepted: %#v", invalid)
		}
		if len(runtime.specs) != 0 {
			t.Fatalf("runtime started before rejecting conditional port values: %#v", runtime.specs)
		}
	}
}

func TestConditionalSSOCallbackMapsSelectedPortOnHostAndContainer(t *testing.T) {
	svc, _, _ := fixture(t)
	runtime := &providerTestRuntime{}
	svc.Options.Runtime = runtime
	minPort, maxPort := float64(1024), float64(65535)
	pkg := catalog.Package{ID: "fixture", Dir: t.TempDir(), Inputs: []catalog.Input{
		{Name: "auth_mode", Type: "choice", Options: []catalog.Choice{{Value: "sso"}, {Value: "api_token"}}},
		{Name: "listen_port", Type: "integer", Min: &minPort, Max: &maxPort},
		{Name: "callback_port", Type: "integer", Min: &minPort, Max: &maxPort, VisibleWhen: map[string]any{"auth_mode": "sso"}},
	}, MCP: &catalog.MCP{
		Name: "fixture", Image: "fixture", Transport: "streamable-http", HostPortInput: "listen_port", ContainerPort: 8765,
		AdditionalPorts: []catalog.ConditionalPort{{HostPortInput: "callback_port", ContainerPortInput: "callback_port", When: map[string]any{"auth_mode": "sso"}}},
	}}
	key := state.Key{Source: "fixture", Package: "fixture", Target: "default", Profile: "sso-profile"}
	values := map[string]any{"auth_mode": "sso", "callback_port": int64(8090), "listen_port": int64(8765)}
	if _, err := svc.startConfigurationProfile(context.Background(), pkg, config.Profile{}, key, values, false); err != nil {
		t.Fatal(err)
	}
	if got := runtime.specs[0].AdditionalPorts; !reflect.DeepEqual(got, []mcp.PortMapping{{HostPort: 8090, ContainerPort: 8090}}) {
		t.Fatalf("selected SSO callback mapping = %#v, want host/container 8090", got)
	}
	runtime.specs = nil
	values["auth_mode"] = "api_token"
	if _, err := svc.startConfigurationProfile(context.Background(), pkg, config.Profile{}, key, values, false); err != nil {
		t.Fatal(err)
	}
	if len(runtime.specs) != 1 || len(runtime.specs[0].AdditionalPorts) != 0 {
		t.Fatalf("callback port was published outside SSO mode: %#v", runtime.specs)
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
