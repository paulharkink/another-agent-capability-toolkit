package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/render"
)

type expressionActionExecutor struct {
	argv    []string
	request map[string]any
}

func (e *expressionActionExecutor) Run(_ context.Context, argv []string, _ string, data []byte, _ map[string]string, _ func([]byte)) ([]byte, error) {
	e.argv = append([]string(nil), argv...)
	if err := json.Unmarshal(data, &e.request); err != nil {
		return nil, err
	}
	return []byte(`{}`), nil
}

func TestProfileValuesResolveRuntimeHCLFromFinalInputMap(t *testing.T) {
	s, _, _ := fixture(t)
	dir := s.Source.Catalog[0].Dir
	manifest := `schema_version = 1
id = "demo"
name = "Demo"
[[inputs]]
name = "handle"
type = "string"
[[inputs]]
name = "display"
type = "string"
default = "${ lower(inputs.username) }"
[[inputs]]
name = "username"
type = "string"
regex = "^[A-Z]+$"
default = "${ upper(inputs.handle) }"
[[inputs]]
name = "token"
type = "secret"
[skill]
name = "demo"
[mcp]
name = "service"
token_input = "token"
token_header = "${ base64encode(\"${inputs.username}:${inputs.token}\") }"
`
	if err := os.WriteFile(filepath.Join(dir, "package.toml"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := catalog.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	s.Source.Catalog[0] = p
	key, err := s.Store.ResolveProfileKey(s.Source.ID, p.ID, "default")
	if err != nil {
		t.Fatal(err)
	}
	profile := config.Profile{Ref: config.ProfileRef{PackID: s.Source.ID, CapabilityID: p.ID, Name: "default"}}
	request := ProfileRequest{Inputs: map[string]any{"handle": "Ada", "token": "key"}}
	resolved, _, values, _, err := s.profileValues(p, profile, key, request)
	if err != nil {
		t.Fatal(err)
	}
	if values["username"] != "ADA" {
		t.Fatalf("derived default: %#v", values)
	}
	if values["display"] != "ada" {
		t.Fatalf("chained derived default: %#v", values)
	}
	if got, want := resolved.MCP.TokenHeader, "QURBOmtleQ=="; got != want {
		t.Fatalf("resolved header = %q, want %q", got, want)
	}
}

func TestProfileValuesEvaluateNestedConfigKeyExpressions(t *testing.T) {
	s, _, _ := fixture(t)
	p := &s.Source.Catalog[0]
	p.Inputs = []catalog.Input{
		{Name: "enabled", Type: "boolean", ConfigKey: "service.enabled"},
		{Name: "port", Type: "integer", ConfigKey: "service.port"},
		{Name: "config", Type: "file", ConfigKey: "service.config"},
		{Name: "selection", Type: "boolean"},
		{Name: "port_value", Type: "integer"},
		{Name: "folder", Type: "directory"},
	}
	key, err := s.Store.ResolveProfileKey(s.Source.ID, p.ID, "default")
	if err != nil {
		t.Fatal(err)
	}
	folder := t.TempDir()
	profilePath := filepath.Join(t.TempDir(), "profiles", "default.toml")
	raw := map[string]any{"service": map[string]any{
		"enabled": "${ inputs.selection }", "port": "${ inputs.port_value }",
		"config": "${ inputs.folder }/settings.toml", "label": "kept",
	}}
	profile := config.Profile{Ref: config.ProfileRef{PackID: s.Source.ID, CapabilityID: p.ID, Name: "default"}, Path: profilePath, Raw: raw, ExpressionSource: raw}
	_, resolvedProfile, values, _, err := s.profileValues(*p, profile, key, ProfileRequest{Inputs: map[string]any{"selection": true, "port_value": int64(8123), "folder": folder}})
	if err != nil {
		t.Fatal(err)
	}
	if values["enabled"] != true || values["port"] != int64(8123) || values["config"] != filepath.Join(folder, "settings.toml") {
		t.Fatalf("resolved nested config_key inputs = %#v", values)
	}
	service := resolvedProfile.Raw["service"].(map[string]any)
	if service["enabled"] != true || service["port"] != int64(8123) || service["config"] != filepath.Join(folder, "settings.toml") || service["label"] != "kept" {
		t.Fatalf("nested profile values/siblings = %#v", service)
	}
}

func TestRunProfileMCPDispatchesResolvedHCLCommandArguments(t *testing.T) {
	s, _, _ := fixture(t)
	dir := s.Source.Catalog[0].Dir
	manifest := `schema_version = 1
id = "demo"
name = "Demo"
[[inputs]]
name = "handle"
type = "string"
[[inputs]]
name = "display"
type = "string"
default = "${ upper(inputs.handle) }"
[[inputs]]
name = "token"
type = "secret"
[skill]
name = "demo"
[mcp]
name = "service"
token_input = "token"
token_header = "${ base64encode(inputs.token) }"
[mcp.actions.authenticate]
command = ["helper", "${ base64encode(\"${inputs.display}:${inputs.token}\") }"]
`
	if err := os.WriteFile(filepath.Join(dir, "package.toml"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := catalog.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	s.Source.Catalog[0] = p
	s.Source.ProfileRoot = filepath.Join(t.TempDir(), "profiles")
	s.Source.EnvironmentRoot = s.Source.ProfileRoot
	profilePath := filepath.Join(s.Source.ProfileRoot, "demo", "default.toml")
	if err := os.MkdirAll(filepath.Dir(profilePath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(profilePath, []byte("[inputs]\nhandle = \"Ada\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	executor := &expressionActionExecutor{}
	s.Options.Runner = executor
	key, err := s.Store.ResolveProfileKey(s.Source.ID, "demo", "default")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		saved bool
	}{{"submitted literal", false}, {"saved literal", true}} {
		t.Run(tc.name, func(t *testing.T) {
			literal := "${not_expression}"
			request := ProfileRequest{Ref: config.ProfileRef{PackID: s.Source.ID, CapabilityID: "demo", Name: "default"}}
			if tc.saved {
				if err := s.Store.SaveAnswers(key, map[string]any{"handle": "Ada", "token": literal}); err != nil {
					t.Fatal(err)
				}
			} else {
				request.Inputs = map[string]any{"handle": "Ada", "token": literal}
			}
			executor.argv, executor.request = nil, nil
			_, err := s.RunProfileMCP(context.Background(), "authenticate", request, "service")
			if err != nil {
				t.Fatal(err)
			}
			want := base64.StdEncoding.EncodeToString([]byte("ADA:" + literal))
			if len(executor.argv) != 2 || executor.argv[1] != want {
				t.Fatalf("runtime args = %#v, want %q", executor.argv, want)
			}
			inputs, ok := executor.request["inputs"].(map[string]any)
			if !ok || inputs["display"] != "ADA" || inputs["token"] != literal {
				t.Fatalf("action reinterpreted a form value: %#v", executor.request)
			}
			if executor.request["action"] != "authenticate" {
				t.Fatalf("action request: %#v", executor.request)
			}
		})
	}
}

func TestProfileValuesRenderHCLComputedInputInMustacheTemplate(t *testing.T) {
	s, _, _ := fixture(t)
	dir := s.Source.Catalog[0].Dir
	manifest := `schema_version = 1
id = "computed-template"
name = "Computed Template"
[[inputs]]
name = "email"
type = "string"
[[inputs]]
name = "token"
type = "secret"
[[inputs]]
name = "derived_value"
type = "string"
default = "${ base64encode(\"${inputs.email}:${inputs.token}\") }"
[skill]
name = "computed-template"
[[templates]]
source = "SKILL.md.mustache"
destination = "SKILL.md"
`
	if err := os.WriteFile(filepath.Join(dir, "package.toml"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	template := "value={{inputs.derived_value}}"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md.mustache"), []byte(template), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := catalog.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	s.Source.Catalog[0] = p
	key, err := s.Store.ResolveProfileKey(s.Source.ID, p.ID, "default")
	if err != nil {
		t.Fatal(err)
	}
	profile := config.Profile{Ref: config.ProfileRef{PackID: s.Source.ID, CapabilityID: p.ID, Name: "default"}}
	resolved, _, values, _, err := s.profileValues(p, profile, key, ProfileRequest{
		Inputs: map[string]any{"email": "alice", "token": "secret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	stage, err := render.Stage(context.Background(), resolved, values, config.Target{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(stage, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "value=YWxpY2U6c2VjcmV0" {
		t.Fatalf("rendered template = %q, want computed input in Mustache context", got)
	}
}
