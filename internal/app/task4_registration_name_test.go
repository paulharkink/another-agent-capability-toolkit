package app

import (
	"context"
	"reflect"
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
)

func TestStartConfigurationProfilePassesResolvedRegistrationNameToRuntime(t *testing.T) {
	tests := []struct {
		name       string
		definition catalog.MCP
		values     map[string]any
		wantName   string
	}{
		{
			name:       "entered name",
			definition: catalog.MCP{Name: "service", RegistrationNameInput: "public_label", Image: "fixture", HostPortInput: "port", ContainerPort: 80},
			values:     map[string]any{"public_label": "Operations API", "port": int64(8765)},
			wantName:   "Operations API",
		},
		{
			name:       "profile default",
			definition: catalog.MCP{Name: "service", Image: "fixture", HostPortInput: "port", ContainerPort: 80},
			values:     map[string]any{"port": int64(8765)},
			wantName:   "fixture-dev",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, _, _ := fixture(t)
			runtime := &providerTestRuntime{}
			svc.Options.Runtime = runtime
			pkg := catalog.Package{ID: "fixture", Dir: t.TempDir(), MCP: &tc.definition}
			key := state.Key{Source: "fixture", Package: "fixture", Environment: "dev", Target: "default"}
			_, err := svc.startConfigurationProfile(context.Background(), pkg, config.Profile{}, key, tc.values, false)
			if err != nil {
				t.Fatal(err)
			}
			if len(runtime.specs) != 1 || runtime.specs[0].RegistrationName != tc.wantName {
				t.Fatalf("runtime registration name = %#v, want %q", runtime.specs, tc.wantName)
			}
		})
	}
}

func TestStartConfigurationProfileRejectsEmptyDeclaredRegistrationName(t *testing.T) {
	svc, _, _ := fixture(t)
	runtime := &providerTestRuntime{}
	svc.Options.Runtime = runtime
	pkg := catalog.Package{ID: "fixture", Dir: t.TempDir(), MCP: &catalog.MCP{
		Name: "service", RegistrationNameInput: "public_label", Image: "fixture", HostPortInput: "port", ContainerPort: 80,
	}}
	key := state.Key{Source: "fixture", Package: "fixture", Environment: "dev", Target: "default"}
	if _, err := svc.startConfigurationProfile(context.Background(), pkg, config.Profile{}, key, map[string]any{"public_label": "", "port": int64(8765)}, false); err == nil {
		t.Fatal("empty declared registration name started a runtime")
	}
	if len(runtime.specs) != 0 {
		t.Fatalf("runtime started with invalid registration name: %#v", runtime.specs)
	}
}

func TestRunProfileMCPPassesSelectedMCPRegistrationNameToRuntime(t *testing.T) {
	svc, _, _ := fixture(t)
	runtime := &providerTestRuntime{}
	svc.Options.Runtime = runtime
	pkg := &svc.Source.Catalog[0]
	pkg.Skill = nil
	pkg.Skills = nil
	pkg.MCP = nil
	pkg.Inputs = []catalog.Input{
		{Name: "port", Type: "integer", Required: true},
		{Name: "first_label", Type: "string", Required: true},
		{Name: "second_label", Type: "string", Required: true},
	}
	pkg.MCPs = []catalog.MCP{
		{Name: "first", Image: "fixture", HostPortInput: "port", ContainerPort: 80, RegistrationNameInput: "first_label"},
		{Name: "second", Image: "fixture", HostPortInput: "port", ContainerPort: 80, RegistrationNameInput: "second_label"},
	}
	ref := writeProfileForTest(t, svc, pkg.ID, "selected-mcp", "")
	_, err := svc.RunProfileMCP(context.Background(), "start", ProfileRequest{Ref: ref, Inputs: map[string]any{
		"port": int64(8765), "first_label": "First MCP", "second_label": "Second MCP",
	}}, "second")
	if err != nil {
		t.Fatal(err)
	}
	if len(runtime.specs) != 1 || runtime.specs[0].RegistrationName != "Second MCP" {
		t.Fatalf("selected MCP registration name was not passed to runtime: %#v", runtime.specs)
	}
	if len(runtime.keys) != 1 || runtime.keys[0].MCP != "second" || runtime.keys[0].Profile != "" {
		t.Fatalf("selected MCP key changed: %#v", runtime.keys)
	}
	if !reflect.DeepEqual(runtime.specs[0].Args, []string(nil)) {
		t.Fatalf("unexpected runtime arguments: %#v", runtime.specs[0].Args)
	}
}
