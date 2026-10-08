package app

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/agents"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

func isolateUXNativeConfigs(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("CLAUDE_CONFIG_DIR", home)
}

func uxField(v any, name string) string {
	value := reflect.ValueOf(v)
	if value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	field := value.FieldByName(name)
	if !field.IsValid() || field.Kind() != reflect.String {
		return ""
	}
	return field.String()
}

func TestUXMCPDestinationsExcludeAllAndGeneric(t *testing.T) {
	svc, _, _ := fixture(t)
	svc.Source.Catalog[0].Skill = &catalog.Skill{}
	svc.Source.Catalog[0].MCP = &catalog.MCP{Name: "demo", Transport: "streamable-http"}
	preview, err := svc.UISetupPreview(context.Background(), viewmodel.SetupRequest{PackageID: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range preview.Destinations {
		if d.ID == "all" || d.ID == "generic" {
			t.Fatalf("non-named MCP destination offered: %+v", d)
		}
	}
	options, err := svc.UIAgentDefaultOptions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range options {
		if id == "all" || id == "generic" {
			t.Fatalf("non-named MCP default option offered: %q", id)
		}
	}
	if err := svc.UISetDefaultAgents(context.Background(), []string{"generic"}); err == nil {
		t.Fatal("generic accepted as a default MCP destination")
	}
	runtime := &fakeRuntime{}
	svc.Options.Runtime = runtime
	for _, id := range []string{"all", "generic"} {
		_, err := svc.UIInstall(context.Background(), viewmodel.SetupInstallRequest{
			SetupRequest: viewmodel.SetupRequest{PackageID: "demo"}, DestinationIDs: []string{id},
		})
		if err == nil || runtime.starts != 0 {
			t.Errorf("UIInstall accepted or started runtime for MCP destination %q: err=%v starts=%d", id, err, runtime.starts)
		}
	}
}

func TestUXSkillOnlyDefaultsAll(t *testing.T) {
	svc, _, _ := fixture(t)
	home := t.TempDir()
	isolateUXNativeConfigs(t, home)
	preview, err := svc.UISetupPreview(context.Background(), viewmodel.SetupRequest{PackageID: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Destinations) == 0 || preview.Destinations[0].ID != "all" || !preview.Destinations[0].Selected {
		t.Fatalf("All not selected by default: %+v", preview.Destinations)
	}
	if got, want := preview.Destinations[0].Path, filepath.Join(home, ".agents", "skills"); got != want {
		t.Fatalf("All path = %q, want %q", got, want)
	}
	for _, d := range preview.Destinations {
		if d.ID == "codex" && d.SkillsPath == preview.Destinations[0].SkillsPath && !strings.Contains(d.Note, "Shares skill directory") {
			t.Fatalf("shared skill directory not explained: %+v", d)
		}
	}
}

func TestUXOpenCodePreviewMatchesActualJSONCWrite(t *testing.T) {
	svc, _, _ := fixture(t)
	home := t.TempDir()
	isolateUXNativeConfigs(t, home)
	svc.Source.Catalog[0].MCP = &catalog.MCP{Name: "demo", Transport: "streamable-http"}
	dir := filepath.Join(home, ".config", "opencode")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	jsonPath, jsoncPath := filepath.Join(dir, "opencode.json"), filepath.Join(dir, "opencode.jsonc")
	if err := os.WriteFile(jsonPath, []byte(`{"theme":"json","mcp":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(jsoncPath, []byte(`{"theme":"jsonc","mcp":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	preview, err := svc.UISetupPreview(context.Background(), viewmodel.SetupRequest{PackageID: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	var destination *viewmodel.SetupDestination
	for i := range preview.Destinations {
		if preview.Destinations[i].ID == "opencode" {
			destination = &preview.Destinations[i]
			break
		}
	}
	if destination == nil {
		t.Fatal("OpenCode destination missing")
	}
	if destination.Path != jsoncPath {
		t.Fatalf("preview path %q, want actual adapter path %q", destination.Path, jsoncPath)
	}
	env, err := agents.ResolveEnvironment("opencode", "opencode", home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := agents.ApplyNativeConfigOverrides(env); err != nil {
		t.Fatal(err)
	}
	adapter, err := agents.For("opencode", svc.Options.Runner)
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.Register(context.Background(), env, agents.Registration{Name: "fixture-demo", URL: "http://127.0.0.1:8765/mcp", Transport: "streamable-http", TimeoutMS: 30000}); err != nil {
		t.Fatal(err)
	}
	jsonBytes, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	jsoncBytes, err := os.ReadFile(jsoncPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(jsonBytes), `"theme":"json"`) || strings.Contains(string(jsonBytes), "fixture-demo") {
		t.Fatalf("lower precedence JSON unexpectedly changed: %s", jsonBytes)
	}
	if !strings.Contains(string(jsoncBytes), `"theme":"jsonc"`) || !strings.Contains(string(jsoncBytes), "fixture-demo") {
		t.Fatalf("effective JSONC content or unrelated field lost: %s", jsoncBytes)
	}
}

func TestUXUIInstallRecordsTheOpenCodeConfigFileItWrites(t *testing.T) {
	svc, _, store := fixture(t)
	home := t.TempDir()
	isolateUXNativeConfigs(t, home)
	svc.Source.Catalog[0].MCP = &catalog.MCP{Name: "demo", Transport: "streamable-http"}
	svc.Options.Runtime = &fakeRuntime{}

	_, err := svc.UIInstall(context.Background(), viewmodel.SetupInstallRequest{
		SetupRequest: viewmodel.SetupRequest{PackageID: "demo"}, DestinationIDs: []string{"opencode"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".config", "opencode", "opencode.jsonc")
	rows, err := store.Installations()
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.AgentID != "opencode" || row.Component != "mcp" {
			continue
		}
		if row.Destination != want {
			t.Fatalf("installation ledger destination = %q, want actual OpenCode write path %q", row.Destination, want)
		}
		if _, err := os.Stat(row.Destination); err != nil {
			t.Fatalf("recorded OpenCode configuration was not written: %v", err)
		}
		return
	}
	t.Fatal("UIInstall did not record an OpenCode MCP registration")
}

func TestUXProfileOverrideIsExplained(t *testing.T) {
	svc, _, store := fixture(t)
	nativeHome, customHome := t.TempDir(), t.TempDir()
	isolateUXNativeConfigs(t, nativeHome)
	key := state.Key{Source: "fixture", Package: "demo", Target: "default"}
	customPath := filepath.Join(customHome, ".config", "opencode", "opencode.json")
	if err := store.Record(state.Installation{Key: key, AgentID: "opencode", AgentKind: "opencode", AgentHome: customHome, Component: "mcp", Destination: customPath}); err != nil {
		t.Fatal(err)
	}
	svc.Source.Catalog[0].MCP = &catalog.MCP{Name: "demo", Transport: "streamable-http"}
	preview, err := svc.UISetupPreview(context.Background(), viewmodel.SetupRequest{PackageID: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	var dest *viewmodel.SetupDestination
	for i := range preview.Destinations {
		if preview.Destinations[i].ID == "opencode" {
			dest = &preview.Destinations[i]
			break
		}
	}
	if dest == nil || dest.Path != strings.TrimSuffix(customPath, ".json")+".jsonc" || uxField(dest, "Home") != customHome || uxField(dest, "Note") == "" {
		t.Fatalf("custom profile destination not explained: %+v", dest)
	}
	rows, err := svc.UIAgentManagement(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.ID == "opencode" {
			if uxField(row, "EffectiveConfigPath") == "" || uxField(row, "EffectiveConfigPath") == customPath || uxField(row, "WriteConfigPath") != customPath || uxField(row, "Home") != customHome || row.Note == "" {
				t.Fatalf("effective and recorded paths not distinguished: %+v", row)
			}
			recorded := false
			for _, file := range row.ConfigFiles {
				if file.Path == customPath && file.Scope == "AACT registration" && file.Profile == "fixture / demo /  / default" {
					recorded = true
				}
			}
			if !recorded {
				t.Fatalf("profile-specific recorded config path missing: %+v", row.ConfigFiles)
			}
			return
		}
	}
	t.Fatal("OpenCode row missing")
}

func TestUXRecordedConfigOverrideExplainsSameHomeNativePath(t *testing.T) {
	svc, _, store := fixture(t)
	nativeHome := t.TempDir()
	isolateUXNativeConfigs(t, nativeHome)
	key := state.Key{Source: "fixture", Package: "demo", Target: "default"}
	recordedPath := filepath.Join(nativeHome, "profile-config", "opencode.json")
	if err := store.Record(state.Installation{Key: key, AgentID: "opencode", AgentKind: "opencode", AgentHome: nativeHome, Component: "mcp", Destination: recordedPath}); err != nil {
		t.Fatal(err)
	}
	svc.Source.Catalog[0].MCP = &catalog.MCP{Name: "demo", Transport: "streamable-http"}
	preview, err := svc.UISetupPreview(context.Background(), viewmodel.SetupRequest{PackageID: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	nativePath := filepath.Join(nativeHome, ".config", "opencode", "opencode.jsonc")
	var dest *viewmodel.SetupDestination
	for i := range preview.Destinations {
		if preview.Destinations[i].ID == "opencode" {
			dest = &preview.Destinations[i]
			break
		}
	}
	if dest == nil || dest.Path != strings.TrimSuffix(recordedPath, ".json")+".jsonc" {
		t.Fatalf("recorded profile path not retained: %+v", dest)
	}
	if !strings.Contains(strings.ToLower(dest.Note), "record") || !strings.Contains(dest.Note, recordedPath) || !strings.Contains(dest.Note, nativePath) {
		t.Fatalf("same-home recorded path override lacks both path provenance: %+v", dest)
	}
}

func TestUXMultipleRecordedHomesRemainProfileSpecific(t *testing.T) {
	svc, _, store := fixture(t)
	nativeHome := t.TempDir()
	isolateUXNativeConfigs(t, nativeHome)
	profiles := []struct {
		key  state.Key
		home string
		path string
	}{
		{state.Key{Source: "fixture", Package: "one", Target: "default"}, t.TempDir(), "first.json"},
		{state.Key{Source: "fixture", Package: "two", Target: "default"}, t.TempDir(), "second.json"},
	}
	for _, profile := range profiles {
		path := filepath.Join(profile.home, "opencode", profile.path)
		if err := store.Record(state.Installation{Key: profile.key, AgentID: "opencode", AgentKind: "opencode", AgentHome: profile.home, Component: "mcp", Destination: path}); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := svc.UIAgentManagement(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.ID != "opencode" {
			continue
		}
		if !strings.Contains(strings.ToLower(row.Note), "multiple") || !strings.Contains(row.Note, "profile") {
			t.Fatalf("multiple profile homes not explained: %+v", row)
		}
		for _, profile := range profiles {
			wantPath := filepath.Join(profile.home, "opencode", profile.path)
			wantProfile := profile.key.Source + " / " + profile.key.Package + " /  / default"
			found := false
			for _, file := range row.ConfigFiles {
				if file.Path == wantPath && file.Profile == wantProfile && uxField(file, "Home") == profile.home {
					found = true
				}
			}
			if !found {
				t.Fatalf("profile-specific home/path missing for %s: %+v", wantProfile, row.ConfigFiles)
			}
		}
		return
	}
	t.Fatal("OpenCode row missing")
}

func TestUXMissingConfigDoesNotHideDetectedCLI(t *testing.T) {
	home, bin := t.TempDir(), t.TempDir()
	isolateUXNativeConfigs(t, home)
	command := "opencode"
	contents := []byte("#!/bin/sh\nexit 0\n")
	if runtime.GOOS == "windows" {
		command = "opencode.exe"
		contents = []byte("fixture executable marker")
	}
	if err := os.WriteFile(filepath.Join(bin, command), contents, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	svc, _, _ := fixture(t)
	rows, err := svc.UIAgentManagement(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.ID == "opencode" {
			if row.Detection != "installed" || uxField(row, "EffectiveConfigPath") == "" || len(row.ConfigFiles) == 0 {
				t.Fatalf("missing config hid CLI detection or candidates: %+v", row)
			}
			for _, file := range row.ConfigFiles {
				if file.Exists {
					t.Fatalf("fixture unexpectedly has config: %+v", file)
				}
			}
			return
		}
	}
	t.Fatal("OpenCode row missing")
}
