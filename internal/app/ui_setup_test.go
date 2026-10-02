package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	svc.Source.Catalog[0].MCP = &catalog.MCP{Name: "demo", Transport: "streamable-http"}
	if err := state.WriteJSON(filepath.Join(store.Root(), "manager", "settings.json"), map[string]any{"agents": []string{"codex"}}); err != nil {
		t.Fatal(err)
	}
	key := state.Key{Source: "fixture", Package: "demo", Target: "default"}
	if err := store.RecordProfile(state.ProfileRecord{Key: key}); err != nil {
		t.Fatal(err)
	}
	if err := store.Record(state.Installation{Key: key, AgentID: "opencode", Component: "mcp", Destination: "/tmp/opencode.json", URL: "http://127.0.0.1:8765/mcp"}); err != nil {
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
}

func TestUISetupPreviewDoesNotOfferUnsupportedMCPAdapter(t *testing.T) {
	svc, _, _ := fixture(t)
	svc.Source.Catalog[0].MCP = &catalog.MCP{Name: "demo", Transport: "streamable-http"}
	got, err := svc.UISetupPreview(context.Background(), viewmodel.SetupRequest{PackageID: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	for _, destination := range got.Destinations {
		if destination.ID == "intellij" {
			t.Fatal("unsupported JetBrains adapter offered as an MCP destination")
		}
	}
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
