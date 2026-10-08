package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/agents"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

func TestUISetupPreviewShowsEveryInputWithWinningProvenance(t *testing.T) {
	svc, _, store := fixture(t)
	svc.Source.Catalog[0].Inputs = []catalog.Input{
		{Name: "public", Type: "string", Default: "public-default"},
		{Name: "saved", Type: "string", Default: "old-default"},
		{Name: "source", Type: "string", Default: "old-default"},
		{Name: "target", Type: "file", ConfigKey: "cluster.certificate", Required: true},
		{Name: "missing", Type: "directory", Required: true},
	}
	svc.Source.PackageDefaults["demo"] = map[string]any{"source": "source-value"}
	svc.Source.EnvironmentRoot = filepath.Join(t.TempDir(), "environments")
	targetPath := filepath.Join(svc.Source.EnvironmentRoot, "company", "demo", "production.toml")
	if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetPath, []byte("[cluster]\ncertificate = './certs/ca.pem'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	key := state.Key{Source: "fixture", Package: "demo", Environment: "company", Target: "production"}
	if err := store.SaveAnswers(key, map[string]any{"saved": "saved-value", "source": "stale-saved"}); err != nil {
		t.Fatal(err)
	}
	got, err := svc.UISetupPreview(context.Background(), viewmodel.SetupRequest{SourceID: "fixture", PackageID: "demo", Environment: "company", Target: "production"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Key != key || got.TargetPath != targetPath || got.SourceRoot != svc.Source.Root || len(got.Inputs) != 5 {
		t.Fatalf("preview identity or fields: %#v", got)
	}
	wants := []struct {
		name, origin string
		value        any
		present      bool
	}{
		{"public", "package", "public-default", true},
		{"saved", "saved", "saved-value", true},
		{"source", "saved", "stale-saved", true},
		{"target", "target", filepath.Join(filepath.Dir(targetPath), "certs", "ca.pem"), true},
		{"missing", "unset", nil, false},
	}
	for i, want := range wants {
		field := got.Inputs[i]
		if field.Definition.Name != want.name || field.Provenance != want.origin || field.Value != want.value || field.HasValue != want.present || !field.Editable {
			t.Errorf("field %d: got %#v; want %#v", i, field, want)
		}
	}
	if got.Inputs[3].ProvenancePath != targetPath {
		t.Fatalf("target provenance path: %#v", got.Inputs[3])
	}
}

func TestUISetupReadinessAllowsDetectedJSONAgentWithoutConfigAndDisablesCodexDesktopWithoutCLI(t *testing.T) {
	home := t.TempDir()
	isolateUXUserHome(t, home)
	t.Setenv("CODEX_HOME", "")
	svc, _, _ := fixture(t)
	svc.Source.Catalog[0].MCP = &catalog.MCP{Name: "demo", Transport: "streamable-http"}
	apps := filepath.Join(home, "Applications")
	if err := os.MkdirAll(filepath.Join(apps, "Codex.app"), 0700); err != nil {
		t.Fatal(err)
	}
	probe := agents.DiscoveryProbe{
		GOOS: "darwin", Home: home, AppRoots: []string{apps}, Getenv: func(string) string { return "" },
		LookPath: func(name string) (string, error) {
			if name == "opencode" {
				return "/usr/local/bin/opencode", nil
			}
			return "", exec.ErrNotFound
		},
	}
	svc.Options.DiscoveryProbe = &probe
	preview, err := svc.UISetupPreview(context.Background(), viewmodel.SetupRequest{PackageID: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]viewmodel.SetupDestination{}
	for _, destination := range preview.Destinations {
		byID[destination.ID] = destination
	}
	if got := byID["opencode"]; got.ID == "" || got.DisabledReason != "" || !strings.Contains(got.Note, "will be created") {
		t.Fatalf("detected JSON client with absent config should remain selectable: %+v", got)
	}
	if got := byID["codex"]; got.DisabledReason != "Codex desktop detected; this adapter requires the codex CLI, which is unavailable" {
		t.Fatalf("Codex CLI prerequisite should be precise: %+v", got)
	}
}

func TestUISetupPreviewCarriesCapabilityPresentationAndMCPDefinitions(t *testing.T) {
	svc, _, _ := fixture(t)
	section := catalog.Section{ID: "advanced", Title: "Advanced", Fields: []string{"token"}}
	primary := catalog.MCP{Name: "primary", Transport: "streamable-http"}
	secondary := catalog.MCP{Name: "secondary", Transport: "stdio"}
	svc.Source.Catalog[0].UI = &catalog.Presentation{Sections: []catalog.Section{section}}
	svc.Source.Catalog[0].MCPs = []catalog.MCP{primary, secondary}

	preview, err := svc.UISetupPreview(context.Background(), viewmodel.SetupRequest{PackageID: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if !preview.HasManifestUI || !reflect.DeepEqual(preview.Sections, []catalog.Section{section}) {
		t.Fatalf("presentation missing from preview: %#v", preview)
	}
	if !reflect.DeepEqual(preview.MCPDefinitions, []catalog.MCP{primary, secondary}) {
		t.Fatalf("MCP definitions missing from preview: %#v", preview.MCPDefinitions)
	}
}

func TestUIInstallEmptyDestinationsRemovesPreviouslyInstalledCapability(t *testing.T) {
	svc, _, store := fixture(t)
	home := filepath.Join(t.TempDir(), "home")
	isolateUXUserHome(t, home)
	request := viewmodel.SetupInstallRequest{SetupRequest: viewmodel.SetupRequest{PackageID: "demo"}, DestinationIDs: []string{"codex"}}
	if _, err := svc.UIInstall(context.Background(), request); err != nil {
		t.Fatalf("initial install: %v", err)
	}
	request.DestinationIDs = nil
	_, err := svc.UIInstall(context.Background(), request)
	if err != nil {
		t.Fatalf("empty desired destinations should remove the binding: %v", err)
	}
	rows, err := store.Installations()
	if err != nil || len(rows) != 0 {
		t.Fatalf("installation ledger after removal = %#v, %v", rows, err)
	}
}

func TestUIInstallUncheckingCapabilityRemovesSkillAndMCPWithoutRuntimeLifecycle(t *testing.T) {
	home := t.TempDir()
	isolateUXUserHome(t, home)
	svc, _, store := fixture(t)
	svc.Source.Catalog[0].MCP = &catalog.MCP{Name: "demo", Transport: "streamable-http"}
	runtime := &changedRuntime{}
	svc.Options.Runtime = runtime
	request := viewmodel.SetupInstallRequest{
		SetupRequest: viewmodel.SetupRequest{PackageID: "demo"}, DestinationIDs: []string{"claude"},
		ExternalURL: "http://foreign.example/mcp",
	}
	if _, err := svc.UIInstall(context.Background(), request); err != nil {
		t.Fatalf("initial capability attach: %v", err)
	}
	rows, err := store.Installations()
	if err != nil || len(rows) != 2 {
		t.Fatalf("initial attach was not complete: %+v, %v", rows, err)
	}
	request.DestinationIDs = nil
	request.Inputs = map[string]any{"required_but_invalid": nil}
	if _, err := svc.UIInstall(context.Background(), request); err != nil {
		t.Fatalf("unchecking binding should not resolve install inputs: %v", err)
	}
	rows, err = store.Installations()
	if err != nil || len(rows) != 0 {
		t.Fatalf("binding rows remain after removal: %+v, %v", rows, err)
	}
	if runtime.starts != 0 || runtime.stops != 0 {
		t.Fatalf("unchecking a binding changed shared runtime lifecycle: starts=%d stops=%d", runtime.starts, runtime.stops)
	}
}

func TestUIInstallEmptyDesiredDestinationsSavesPartialAnswers(t *testing.T) {
	isolateUXUserHome(t, t.TempDir())
	svc, _, store := fixture(t)
	svc.Source.Catalog[0].Inputs = []catalog.Input{{Name: "note", Type: "string", Required: false}}
	request := viewmodel.SetupInstallRequest{
		SetupRequest:   viewmodel.SetupRequest{PackageID: "demo"},
		DestinationIDs: []string{"codex"}, Inputs: map[string]any{"note": "before"},
	}
	if _, err := svc.UIInstall(context.Background(), request); err != nil {
		t.Fatalf("initial install: %v", err)
	}
	request.DestinationIDs = nil
	request.Inputs = map[string]any{"note": "after"}
	result, err := svc.UIInstall(context.Background(), request)
	if err != nil || !result.Saved {
		t.Fatalf("empty desired apply did not save partial answers: %+v %v", result, err)
	}
	preview, err := svc.UISetupPreview(context.Background(), viewmodel.SetupRequest{PackageID: "demo"})
	if err != nil || len(preview.Inputs) != 1 || preview.Inputs[0].Value != "after" || preview.Inputs[0].Provenance != "saved" {
		t.Fatalf("saved partial answer missing from preview: %+v %v", preview.Inputs, err)
	}
	rows, err := store.Installations()
	if err != nil || len(rows) != 0 {
		t.Fatalf("answer save recreated removed installation state: %+v %v", rows, err)
	}
}

func TestUISetupPreviewListsDatabaseChoicesFromTarget(t *testing.T) {
	svc, _, _ := fixture(t)
	svc.Source.Catalog[0].Inputs = []catalog.Input{{
		Name: "connections", Type: "multichoice", Label: "Read-only database queries (optional)", OptionsFrom: "dbms.*.tenants.*",
	}}
	svc.Source.EnvironmentRoot = filepath.Join(t.TempDir(), "environments")
	targetPath := filepath.Join(svc.Source.EnvironmentRoot, "sample-env", "demo", "target-a.toml")
	if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
		t.Fatal(err)
	}
	data := "[dbms.shared_postgres.tenants.plane]\nlabel='Plane'\n" +
		"[dbms.shared_postgres.tenants.openwebui]\nlabel='OpenWebUI'\n" +
		"[dbms.homeassistant_postgres.tenants.homeassistant]\nlabel='Home Assistant'\n"
	if err := os.WriteFile(targetPath, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	preview, err := svc.UISetupPreview(context.Background(), viewmodel.SetupRequest{PackageID: "demo", Environment: "sample-env", Target: "target-a"})
	if err != nil {
		t.Fatal(err)
	}
	want := []catalog.Choice{
		{Value: "homeassistant_postgres/homeassistant", Label: "Home Assistant — homeassistant_postgres/homeassistant"},
		{Value: "shared_postgres/openwebui", Label: "OpenWebUI — shared_postgres/openwebui"},
		{Value: "shared_postgres/plane", Label: "Plane — shared_postgres/plane"},
	}
	if len(preview.Inputs) != 1 || !reflect.DeepEqual(preview.Inputs[0].Definition.Options, want) {
		t.Fatalf("database choices: %#v; want %#v", preview.Inputs, want)
	}
}
func TestFixedTargetInputWinsSavedAnswersAndRejectsOverride(t *testing.T) {
	svc, _, store := fixture(t)
	svc.Source.Catalog[0].Inputs = []catalog.Input{{Name: "api_server", Type: "string", ConfigKey: "cluster.api_server", Required: true}}
	svc.Source.EnvironmentRoot = filepath.Join(t.TempDir(), "environments")
	targetPath := filepath.Join(svc.Source.EnvironmentRoot, "company", "demo", "production.toml")
	if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetPath, []byte("[cluster]\napi_server='https://fixed.example'\n[aact.input_policy]\napi_server='fixed'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	key := state.Key{Source: "fixture", Package: "demo", Environment: "company", Target: "production"}
	if err := store.SaveAnswers(key, map[string]any{"api_server": "https://stale.example"}); err != nil {
		t.Fatal(err)
	}
	q := viewmodel.SetupRequest{PackageID: "demo", Environment: "company", Target: "production"}
	preview, err := svc.UISetupPreview(context.Background(), q)
	if err != nil || len(preview.Inputs) != 1 || preview.Inputs[0].Editable || preview.Inputs[0].Value != "https://fixed.example" || preview.Inputs[0].Provenance != "target" {
		t.Fatalf("fixed preview: %+v %v", preview, err)
	}
	values, _, _, err := svc.resolve(context.Background(), svc.Source.Catalog[0], "company", "production", nil, false, false, false)
	if err != nil || values["api_server"] != "https://fixed.example" {
		t.Fatalf("resolved: %+v %v", values, err)
	}
	if _, _, _, err := svc.resolve(context.Background(), svc.Source.Catalog[0], "company", "production", map[string]any{"api_server": "https://other.example"}, false, false, false); err == nil || !strings.Contains(err.Error(), "fixed") {
		t.Fatalf("override accepted: %v", err)
	}
}
func TestFixedTargetPolicyRequiresDeclaredTargetValue(t *testing.T) {
	svc, _, _ := fixture(t)
	svc.Source.Catalog[0].Inputs = []catalog.Input{{Name: "api_server", Type: "string", Required: true}}
	svc.Source.EnvironmentRoot = filepath.Join(t.TempDir(), "environments")
	targetPath := filepath.Join(svc.Source.EnvironmentRoot, "company", "demo", "production.toml")
	if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetPath, []byte("[aact.input_policy]\napi_server='fixed'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UISetupPreview(context.Background(), viewmodel.SetupRequest{PackageID: "demo", Environment: "company", Target: "production"}); err == nil || !strings.Contains(err.Error(), "requires") {
		t.Fatalf("missing fixed value accepted: %v", err)
	}
}
func TestTargetOnlyPolicyDoesNotLockSourceDefault(t *testing.T) {
	svc, _, _ := fixture(t)
	svc.Source.Catalog[0].Inputs = []catalog.Input{{Name: "api_server", Type: "string", Required: true}}
	svc.Source.PackageDefaults["demo"] = map[string]any{"api_server": "https://source.example", "aact": map[string]any{"input_policy": map[string]any{"api_server": "fixed"}}}
	preview, err := svc.UISetupPreview(context.Background(), viewmodel.SetupRequest{PackageID: "demo"})
	if err != nil || len(preview.Inputs) != 1 || !preview.Inputs[0].Editable {
		t.Fatalf("source default locked input: %+v %v", preview, err)
	}
	values, _, _, err := svc.resolve(context.Background(), svc.Source.Catalog[0], "", "", map[string]any{"api_server": "https://override.example"}, false, false, false)
	if err != nil || values["api_server"] != "https://override.example" {
		t.Fatalf("source default was fixed: %+v %v", values, err)
	}
}
func TestTargetPolicyRejectsUnknownInputAndKeepsDefaultEditable(t *testing.T) {
	svc, _, _ := fixture(t)
	svc.Source.Catalog[0].Inputs = []catalog.Input{{Name: "api_server", Type: "string", Required: true}}
	svc.Source.EnvironmentRoot = filepath.Join(t.TempDir(), "environments")
	targetPath := filepath.Join(svc.Source.EnvironmentRoot, "company", "demo", "production.toml")
	if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetPath, []byte("api_server='https://target.example'\n[aact.input_policy]\nunknown='fixed'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	q := viewmodel.SetupRequest{PackageID: "demo", Environment: "company", Target: "production"}
	if _, err := svc.UISetupPreview(context.Background(), q); err == nil || !strings.Contains(err.Error(), "undeclared") {
		t.Fatalf("unknown policy accepted: %v", err)
	}
	if err := os.WriteFile(targetPath, []byte("api_server='https://target.example'\n[aact.input_policy]\napi_server='default'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	preview, err := svc.UISetupPreview(context.Background(), q)
	if err != nil || !preview.Inputs[0].Editable || preview.Inputs[0].Value != "https://target.example" {
		t.Fatalf("editable default: %+v %v", preview, err)
	}
}
func TestInteractiveEditorOmitFixedTargetInput(t *testing.T) {
	svc, _, _ := fixture(t)
	svc.Source.Catalog[0].Inputs = []catalog.Input{
		{Name: "api_server", Type: "string", Required: true},
		{Name: "local_port", Type: "integer", Required: true},
		{Name: "credential", Type: "string", Required: true, VisibleWhen: map[string]any{"api_server": "https://fixed.example"}},
	}
	svc.Source.EnvironmentRoot = filepath.Join(t.TempDir(), "environments")
	targetPath := filepath.Join(svc.Source.EnvironmentRoot, "company", "demo", "production.toml")
	if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetPath, []byte("api_server='https://fixed.example'\nlocal_port=8765\n[aact.input_policy]\napi_server='fixed'\nlocal_port='default'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	called := false
	svc.Options.Editor = func(_ context.Context, defs []catalog.Input, values map[string]any) (map[string]any, error) {
		called = true
		if len(defs) != 2 || defs[0].Name != "local_port" || defs[1].Name != "credential" || len(defs[1].VisibleWhen) != 0 {
			t.Fatalf("fixed input reached editor: %+v", defs)
		}
		if _, ok := values["api_server"]; ok {
			t.Fatalf("fixed value reached editor: %+v", values)
		}
		values["local_port"] = int64(9000)
		values["credential"] = "visible-because-fixed-controller-matches"
		return values, nil
	}
	values, _, _, err := svc.resolve(context.Background(), svc.Source.Catalog[0], "company", "production", nil, true, false, false)
	if err != nil || !called || values["api_server"] != "https://fixed.example" || values["local_port"] != int64(9000) || values["credential"] != "visible-because-fixed-controller-matches" {
		t.Fatalf("interactive fixed values: %+v %v", values, err)
	}
}

func TestInteractiveInstallPreservesStoredInputHiddenByFixedTarget(t *testing.T) {
	svc, env, store := fixture(t)
	svc.Source.Catalog[0].Inputs = []catalog.Input{
		{Name: "mode", Type: "string", Required: true},
		{Name: "local_label", Type: "string"},
		{Name: "advanced_token", Type: "string", Required: true, VisibleWhen: map[string]any{"mode": "remote"}},
	}
	svc.Source.EnvironmentRoot = filepath.Join(t.TempDir(), "environments")
	targetPath := filepath.Join(svc.Source.EnvironmentRoot, "company", "demo", "production.toml")
	if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetPath, []byte("mode='local'\n[aact.input_policy]\nmode='fixed'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	key := state.Key{Source: "fixture", Package: "demo", Environment: "company", Target: "production"}
	if err := store.SaveAnswers(key, map[string]any{"advanced_token": "retain-me", "local_label": "before"}); err != nil {
		t.Fatal(err)
	}
	svc.Options.Editor = func(_ context.Context, defs []catalog.Input, values map[string]any) (map[string]any, error) {
		for _, def := range defs {
			if def.Name == "advanced_token" {
				t.Fatalf("field controlled by fixed local mode remained visible: %+v", defs)
			}
		}
		return map[string]any{"local_label": "after"}, nil
	}
	if _, err := svc.Install(context.Background(), InstallRequest{Package: "demo", Environment: "company", Target: "production", Agents: []agents.Environment{env}, Interactive: true}); err != nil {
		t.Fatal(err)
	}
	answers, err := store.Answers(key)
	if err != nil || answers["advanced_token"] != "retain-me" || answers["local_label"] != "after" || answers["mode"] != "local" {
		t.Fatalf("interactive save lost inactive value or fixed controller: %#v %v", answers, err)
	}
}
func TestInteractiveResolveSkipsEmptyFormWhenEveryInputFixed(t *testing.T) {
	svc, _, _ := fixture(t)
	svc.Source.Catalog[0].Inputs = []catalog.Input{{Name: "api_server", Type: "string", Required: true}}
	svc.Source.EnvironmentRoot = filepath.Join(t.TempDir(), "environments")
	targetPath := filepath.Join(svc.Source.EnvironmentRoot, "company", "demo", "production.toml")
	if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetPath, []byte("api_server='https://fixed.example'\n[aact.input_policy]\napi_server='fixed'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	svc.Options.Editor = func(context.Context, []catalog.Input, map[string]any) (map[string]any, error) {
		t.Fatal("empty interactive form opened")
		return nil, nil
	}
	values, _, _, err := svc.resolve(context.Background(), svc.Source.Catalog[0], "company", "production", nil, true, false, false)
	if err != nil || values["api_server"] != "https://fixed.example" {
		t.Fatalf("fixed-only resolve: %+v %v", values, err)
	}
}

func TestUISetupPreviewDoesNotWriteStateOrRequireCompletedAnswers(t *testing.T) {
	svc, _, store := fixture(t)
	svc.Source.Catalog[0].Inputs = []catalog.Input{{Name: "required", Type: "string", Required: true}}
	key := state.Key{Source: "fixture", Package: "demo", Target: "default"}
	got, err := svc.UISetupPreview(context.Background(), viewmodel.SetupRequest{SourceID: "fixture", PackageID: "demo"})
	if err != nil || len(got.Inputs) != 1 || got.Inputs[0].HasValue {
		t.Fatalf("missing required input should remain editable: %#v %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(store.Root(), "answers", key.ID()+".json")); !os.IsNotExist(err) {
		t.Fatalf("preview wrote answers: %v", err)
	}
}

func TestUISetupPreviewOffersGlobalOnlyForSkillOnlyPackage(t *testing.T) {
	svc, _, _ := fixture(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if err := state.WriteJSON(filepath.Join(svc.Store.Root(), "manager", "settings.json"), map[string]any{"agents": []string{"codex", "opencode"}}); err != nil {
		t.Fatal(err)
	}
	skill, err := svc.UISetupPreview(context.Background(), viewmodel.SetupRequest{PackageID: "demo"})
	if err != nil || len(skill.Destinations) == 0 || skill.Destinations[0].ID != "all" || !skill.Destinations[0].Selected {
		t.Fatalf("skill destinations: %#v %v", skill.Destinations, err)
	}
	if skill.Destinations[0].Path != filepath.Join(home, ".agents", "skills") {
		t.Fatalf("global skill path: %#v", skill.Destinations[0])
	}
	for _, destination := range skill.Destinations[1:] {
		if destination.Selected {
			t.Fatalf("skill-only setup selected named default: %#v", destination)
		}
	}
	svc.Source.Catalog[0].MCP = &catalog.MCP{Name: "demo", Transport: "streamable-http"}
	mcpPreview, err := svc.UISetupPreview(context.Background(), viewmodel.SetupRequest{PackageID: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	for _, destination := range mcpPreview.Destinations {
		if destination.ID == "all" {
			t.Fatalf("global destination offered for MCP: %#v", mcpPreview.Destinations)
		}
		if destination.ID == "codex" || destination.ID == "opencode" {
			if !destination.Selected {
				t.Fatalf("configured named default not selected: %#v", destination)
			}
		} else if destination.Selected {
			t.Fatalf("unconfigured named destination selected: %#v", destination)
		}
	}
}

func TestUISetupPreviewUsesActualRegistrationsAfterAnAttempt(t *testing.T) {
	svc, _, store := fixture(t)
	svc.Source.Catalog[0].Skill = nil
	svc.Source.Catalog[0].Inputs = []catalog.Input{{Name: "registration_name", Label: "MCP registration name", Type: "string"}}
	svc.Source.Catalog[0].MCP = &catalog.MCP{Name: "demo", Transport: "streamable-http", RegistrationNameInput: "registration_name"}
	if err := state.WriteJSON(filepath.Join(store.Root(), "manager", "settings.json"), map[string]any{"agents": []string{"codex"}}); err != nil {
		t.Fatal(err)
	}
	key := state.Key{Source: "fixture", Package: "demo", Target: "default"}
	if err := store.RecordProfile(state.ProfileRecord{Key: key}); err != nil {
		t.Fatal(err)
	}
	if err := store.Record(state.Installation{Key: key, AgentID: "opencode", Component: "mcp", Destination: "/tmp/opencode.json", RegistrationName: "demo-home-production-current", URL: "http://127.0.0.1:8765/mcp"}); err != nil {
		t.Fatal(err)
	}
	preview, err := svc.UISetupPreview(context.Background(), viewmodel.SetupRequest{PackageID: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	selected := map[string]bool{}
	for _, destination := range preview.Destinations {
		selected[destination.ID] = destination.Selected
	}
	if selected["codex"] || !selected["opencode"] {
		t.Fatalf("saved defaults replaced actual registration state: %#v", selected)
	}
	if got := preview.Inputs[0]; !got.HasValue || got.Value != "demo-home-production-current" || got.Provenance != "registration" {
		t.Fatalf("unset editable name did not show the currently registered MCP name: %#v", got)
	}
	svc.Source.PackageDefaults["demo"] = map[string]any{"registration_name": "maintainer-default"}
	preview, err = svc.UISetupPreview(context.Background(), viewmodel.SetupRequest{PackageID: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if got := preview.Inputs[0]; got.Value != "maintainer-default" || got.Provenance != "source" || !strings.Contains(got.Definition.Hint, "demo-home-production-current") {
		t.Fatalf("configured name must remain the edit value while identifying the active registration: %#v", got)
	}
}

func TestUISetupPreviewRegistrationNameFallbackRequiresMatchingUnambiguousRows(t *testing.T) {
	for _, tc := range []struct {
		name      string
		rows      []state.Installation
		wantValue string
		wantSet   bool
	}{
		{
			name: "divergent matching registration names are suppressed",
			rows: []state.Installation{
				{Key: state.Key{Source: "fixture", Package: "demo", Target: "default"}, AgentID: "codex", Component: "mcp", RegistrationName: "demo-codex"},
				{Key: state.Key{Source: "fixture", Package: "demo", Target: "default"}, AgentID: "opencode", Component: "mcp", RegistrationName: "demo-opencode"},
			},
		},
		{
			name: "sibling MCP registration is ignored",
			rows: []state.Installation{
				{Key: state.Key{Source: "fixture", Package: "demo", Target: "default"}, AgentID: "codex", Component: "mcp", RegistrationName: "demo-current"},
				{Key: state.Key{Source: "fixture", Package: "demo", Target: "default", MCP: "sibling"}, AgentID: "opencode", Component: "mcp", RegistrationName: "sibling-current"},
			},
			wantValue: "demo-current",
			wantSet:   true,
		},
		{
			name: "sibling-only row does not populate this MCP name",
			rows: []state.Installation{
				{Key: state.Key{Source: "fixture", Package: "demo", Target: "default", MCP: "sibling"}, AgentID: "opencode", Component: "mcp", RegistrationName: "sibling-current"},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, _, store := fixture(t)
			svc.Source.Catalog[0].Skill = nil
			svc.Source.Catalog[0].Inputs = []catalog.Input{{Name: "registration_name", Label: "MCP registration name", Type: "string"}}
			svc.Source.Catalog[0].MCP = &catalog.MCP{Name: "demo", Transport: "streamable-http", RegistrationNameInput: "registration_name"}
			for _, row := range tc.rows {
				if err := store.Record(row); err != nil {
					t.Fatal(err)
				}
			}
			preview, err := svc.UISetupPreview(context.Background(), viewmodel.SetupRequest{PackageID: "demo"})
			if err != nil {
				t.Fatal(err)
			}
			var field viewmodel.SetupInput
			for _, input := range preview.Inputs {
				if input.Definition.Name == "registration_name" {
					field = input
					break
				}
			}
			if field.Definition.Name == "" || field.HasValue != tc.wantSet || (tc.wantSet && field.Value != tc.wantValue) {
				t.Fatalf("registration name fallback = %#v; want set=%t value=%q", field, tc.wantSet, tc.wantValue)
			}
		})
	}
}

func TestUISetupPreviewDoesNotCheckFailedSkillDestination(t *testing.T) {
	svc, _, store := fixture(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	key := state.Key{Source: "fixture", Package: "demo", Target: "default"}
	if err := store.SaveAnswers(key, map[string]any{}); err != nil {
		t.Fatal(err)
	}
	preview, err := svc.UISetupPreview(context.Background(), viewmodel.SetupRequest{PackageID: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	for _, destination := range preview.Destinations {
		if destination.Selected {
			t.Fatalf("failed install appeared checked: %#v", preview.Destinations)
		}
	}
}

func TestUISetupPreviewDoesNotCheckUninstalledSavedMCPProfile(t *testing.T) {
	svc, _, store := fixture(t)
	svc.Source.Catalog[0].Skill = nil
	svc.Source.Catalog[0].MCP = &catalog.MCP{Name: "demo", Transport: "streamable-http"}
	if err := state.WriteJSON(filepath.Join(store.Root(), "manager", "settings.json"), map[string]any{"agents": []string{"codex"}}); err != nil {
		t.Fatal(err)
	}
	key := state.Key{Source: "fixture", Package: "demo", Target: "default"}
	if err := store.RecordProfile(state.ProfileRecord{Key: key}); err != nil {
		t.Fatal(err)
	}
	preview, err := svc.UISetupPreview(context.Background(), viewmodel.SetupRequest{PackageID: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	for _, destination := range preview.Destinations {
		if destination.Selected {
			t.Fatalf("saved profile without registration appeared checked: %#v", preview.Destinations)
		}
	}
}

func TestSavedCredentialClearOverridesEditableTargetPrefillOnReopen(t *testing.T) {
	svc, _, store := fixture(t)
	svc.Source.Catalog[0].Inputs = []catalog.Input{{Name: "token", Type: "secret", ExclusiveGroup: "cluster_credentials"}, {Name: "kubeconfig", Type: "file", ExclusiveGroup: "cluster_credentials"}}
	svc.Source.EnvironmentRoot = filepath.Join(t.TempDir(), "environments")
	path := filepath.Join(svc.Source.EnvironmentRoot, "company", "demo", "production.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("kubeconfig = './source.yaml'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	key := state.Key{Source: "fixture", Package: "demo", Environment: "company", Target: "production"}
	if err := store.SaveAnswers(key, map[string]any{"kubeconfig": ""}); err != nil {
		t.Fatal(err)
	}
	preview, err := svc.UISetupPreview(context.Background(), viewmodel.SetupRequest{PackageID: "demo", Environment: "company", Target: "production"})
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Inputs) != 2 || preview.Inputs[1].Value != "" || preview.Inputs[1].Provenance != "saved" {
		t.Fatalf("cleared credential was restored by target prefill: %#v", preview.Inputs)
	}
	if preview.Inputs[1].InheritedValue != filepath.Join(filepath.Dir(path), "source.yaml") || !preview.Inputs[1].HasInheritedValue {
		t.Fatalf("saved override did not retain its resolved lower-precedence value: %#v", preview.Inputs[1])
	}
}

func TestUIInstallResetRemovesOnlySelectedSavedAnswerAndRevealsPackageDefault(t *testing.T) {
	svc, _, store := fixture(t)
	svc.Source.Catalog[0].Inputs = []catalog.Input{
		{Name: "endpoint", Label: "Endpoint", Type: "string", Default: "https://inherited.example"},
		{Name: "team", Label: "Team", Type: "string"},
	}
	key := state.Key{Source: "fixture", Package: "demo", Target: "default"}
	if err := store.SaveAnswers(key, map[string]any{"endpoint": "https://saved.example", "team": "keep-me"}); err != nil {
		t.Fatal(err)
	}

	preview, err := svc.UISetupPreview(context.Background(), viewmodel.SetupRequest{PackageID: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Inputs[0].Provenance != "saved" || preview.Inputs[0].InheritedValue != "https://inherited.example" || !preview.Inputs[0].HasInheritedValue {
		t.Fatalf("preview did not expose the value below the saved override: %+v", preview.Inputs[0])
	}

	if _, err := svc.UIInstall(context.Background(), viewmodel.SetupInstallRequest{
		SetupRequest: viewmodel.SetupRequest{PackageID: "demo"},
		Inputs:       map[string]any{"endpoint": "https://inherited.example", "team": "keep-me"},
		ResetInputs:  []string{"endpoint"},
	}); err != nil {
		t.Fatal(err)
	}

	answers, err := store.Answers(key)
	if err != nil || answers["team"] != "keep-me" {
		t.Fatalf("reset removed another field or failed to preserve it: %#v %v", answers, err)
	}
	if _, exists := answers["endpoint"]; exists {
		t.Fatalf("inherited value was re-saved as an override: %#v", answers)
	}
	after, err := svc.UISetupPreview(context.Background(), viewmodel.SetupRequest{PackageID: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if after.Inputs[0].Value != "https://inherited.example" || after.Inputs[0].Provenance != "package" {
		t.Fatalf("removing the override did not reveal the package default: %+v", after.Inputs[0])
	}
}

func TestUIInstallResetDoesNotResaveInheritedValueAfterInstallingSkill(t *testing.T) {
	svc, _, store := fixture(t)
	home := t.TempDir()
	isolateUXUserHome(t, home)
	svc.Source.Catalog[0].Inputs = []catalog.Input{{Name: "endpoint", Label: "Endpoint", Type: "string", Default: "https://inherited.example"}}
	key := state.Key{Source: "fixture", Package: "demo", Target: "default"}
	if err := store.SaveAnswers(key, map[string]any{"endpoint": "https://saved.example"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UIInstall(context.Background(), viewmodel.SetupInstallRequest{
		SetupRequest:   viewmodel.SetupRequest{PackageID: "demo"},
		Inputs:         map[string]any{"endpoint": "https://inherited.example"},
		ResetInputs:    []string{"endpoint"},
		DestinationIDs: []string{"all"},
	}); err != nil {
		t.Fatal(err)
	}
	answers, err := store.Answers(key)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := answers["endpoint"]; exists {
		t.Fatalf("install path persisted the inherited value as an override: %#v", answers)
	}
	preview, err := svc.UISetupPreview(context.Background(), viewmodel.SetupRequest{PackageID: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Inputs[0].Value != "https://inherited.example" || preview.Inputs[0].Provenance != "package" {
		t.Fatalf("install path did not leave the package default as effective value: %+v", preview.Inputs[0])
	}
}

func TestUISetupPreviewDisablesUndetectedJetBrainsMCPAdapterWithReason(t *testing.T) {
	svc, _, _ := fixture(t)
	svc.Source.Catalog[0].MCP = &catalog.MCP{Name: "demo", Transport: "streamable-http"}
	got, err := svc.UISetupPreview(context.Background(), viewmodel.SetupRequest{PackageID: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	for _, destination := range got.Destinations {
		if destination.ID == "intellij" {
			if _, adapterErr := agents.For("intellij", nil); adapterErr != nil {
				t.Fatalf("JetBrains adapter should be supported: %v", adapterErr)
			}
			if destination.DisabledReason != "JetBrains AI Assistant was not detected" {
				t.Fatalf("undetected JetBrains destination should be disabled with a detection reason: %+v", destination)
			}
			if strings.Contains(destination.DisabledReason, "not implemented") {
				t.Fatalf("JetBrains adapter incorrectly reported unsupported: %+v", destination)
			}
			return
		}
	}
	t.Fatal("JetBrains destination should remain visible while its application is undetected")
}

func TestUIInstallUsesExplicitAnswersAndGlobalDestinationWithoutEditor(t *testing.T) {
	svc, _, store := fixture(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	svc.Source.Catalog[0].Inputs = []catalog.Input{{Name: "label", Type: "string", Required: true}}
	svc.Source.Catalog[0].Templates = []catalog.Template{{Source: "SKILL.md.mustache", Destination: "SKILL.md"}}
	if err := os.WriteFile(filepath.Join(svc.Source.Catalog[0].Dir, "SKILL.md.mustache"), []byte("hello {{{inputs.label}}}"), 0644); err != nil {
		t.Fatal(err)
	}
	svc.Options.Editor = func(context.Context, []catalog.Input, map[string]any) (map[string]any, error) {
		t.Fatal("UIInstall reopened the legacy editor")
		return nil, nil
	}
	got, err := svc.UIInstall(context.Background(), viewmodel.SetupInstallRequest{
		SetupRequest: viewmodel.SetupRequest{SourceID: "fixture", PackageID: "demo"},
		Inputs:       map[string]any{"label": "world"}, DestinationIDs: []string{"all"},
	})
	if err != nil || len(got.Changes) != 1 {
		t.Fatalf("install: %#v %v", got, err)
	}
	data, err := os.ReadFile(filepath.Join(home, ".agents", "skills", "demo", "SKILL.md"))
	if err != nil || string(data) != "hello world" {
		t.Fatalf("installed content %q: %v", data, err)
	}
	answers, err := store.Answers(state.Key{Source: "fixture", Package: "demo", Target: "default"})
	if err != nil || answers["label"] != "world" {
		t.Fatalf("answers %#v %v", answers, err)
	}
}

func TestUIInstallRejectsGlobalDestinationForMCPBeforeStartingIt(t *testing.T) {
	svc, _, _ := fixture(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	svc.Source.Catalog[0].MCP = &catalog.MCP{Name: "demo", Transport: "streamable-http"}
	runtime := &fakeRuntime{}
	svc.Options.Runtime = runtime
	_, err := svc.UIInstall(context.Background(), viewmodel.SetupInstallRequest{
		SetupRequest:   viewmodel.SetupRequest{SourceID: "fixture", PackageID: "demo"},
		DestinationIDs: []string{"all"},
	})
	if err == nil || !strings.Contains(err.Error(), "skill-only") || runtime.starts != 0 {
		t.Fatalf("invalid global MCP install: %v, starts=%d", err, runtime.starts)
	}
}
