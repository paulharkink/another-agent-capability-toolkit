package tui

import (
	"context"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

type setupBackendFixture struct {
	fixtureBackend
	previewRequest viewmodel.SetupRequest
	installRequest *viewmodel.SetupInstallRequest
	extraInputs    []viewmodel.SetupInput
}

func (b *setupBackendFixture) UISetupPreview(_ context.Context, q viewmodel.SetupRequest) (viewmodel.SetupPreview, error) {
	b.previewRequest = q
	preview := viewmodel.SetupPreview{
		Key:         state.Key{Source: "team-source", Package: "plain", Target: "default"},
		PackageName: "Plain",
		Inputs: []viewmodel.SetupInput{
			{Definition: catalog.Input{Name: "repo", Label: "Repository", Type: "string", Required: true}, Value: "/repos/team", HasValue: true, Provenance: "source", ProvenancePath: "/catalog/aact.toml", Editable: true},
			{Definition: catalog.Input{Name: "mode", Label: "Mode", Type: "choice", Required: true, Options: []catalog.Choice{{Value: "fast", Label: "Fast"}, {Value: "safe", Label: "Safe"}}}, Value: "safe", HasValue: true, Provenance: "package", ProvenancePath: "/catalog/plain/package.toml", Editable: true},
		},
		Destinations: []viewmodel.SetupDestination{{ID: "all", Path: "/home/test/.agents/skills", Selected: true}, {ID: "codex", Path: "/home/test/.agents/skills"}},
	}
	preview.Inputs = append(preview.Inputs, b.extraInputs...)
	return preview, nil
}

func TestInstallShortcutUsesUnifiedSetupForm(t *testing.T) {
	b := &setupBackendFixture{}
	m := NewContext(context.Background(), b)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m.Update(m.Init()())
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'i', Text: "i"})
	if cmd == nil || m.form != nil || !m.busy {
		t.Fatal("Install shortcut bypassed typed setup preview")
	}
}

func TestSetupDestinationFieldDoesNotOverwritePackageInput(t *testing.T) {
	b := &setupBackendFixture{extraInputs: []viewmodel.SetupInput{{Definition: catalog.Input{Name: "destination", Label: "Output destination", Type: "string"}, Value: "/output", HasValue: true, Editable: true}}}
	m := NewContext(context.Background(), b)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	m.Update(m.Init()())
	m.Update(m.homeOperation("parameters")())
	_, cmd := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("setup form did not save")
	}
	m.Update(cmd())
	if b.installRequest == nil || b.installRequest.Inputs["destination"] != "/output" || !reflect.DeepEqual(b.installRequest.DestinationIDs, []string{"all"}) {
		t.Fatalf("package destination collided with installer destination field: %+v", b.installRequest)
	}
}

func (b *setupBackendFixture) UIInstall(_ context.Context, q viewmodel.SetupInstallRequest) (viewmodel.OperationResult, error) {
	b.installRequest = &q
	return viewmodel.OperationResult{Message: "Installed", Changes: []state.Installation{{AgentID: "all", Component: "skill"}}}, nil
}

func TestCapabilitySetupUsesOneDeclaredInputAndDestinationForm(t *testing.T) {
	b := &setupBackendFixture{}
	m := NewContext(context.Background(), b)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	m.Update(m.Init()())
	cmd := m.homeOperation("parameters")
	if cmd == nil || !m.busy || m.form != nil {
		t.Fatalf("typed setup preview was not requested: busy=%v form=%v", m.busy, m.form)
	}
	m.Update(cmd())
	if m.form == nil || m.busy || b.previewRequest.PackageID != "plain" || b.previewRequest.SourceID != "team-source" {
		t.Fatalf("typed setup preview did not open: %+v, %v", b.previewRequest, m.form)
	}
	view := m.View().Content
	for _, want := range []string{"Repository", "Mode", "Destinations", "[all]", "aact.toml"} {
		if !strings.Contains(view, want) {
			t.Fatalf("single setup form missing %q:\n%s", want, view)
		}
	}
	_, cmd = m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if cmd == nil || !m.busy || m.form != nil {
		t.Fatal("Save did not apply the typed setup once")
	}
	m.Update(cmd())
	if b.installRequest == nil || !reflect.DeepEqual(b.installRequest.DestinationIDs, []string{"all"}) || b.installRequest.Inputs["repo"] != "/repos/team" || b.installRequest.Inputs["mode"] != "safe" {
		t.Fatalf("one-shot install received wrong form values: %+v", b.installRequest)
	}
	if !strings.Contains(m.output, "Installed") {
		t.Fatalf("install result hidden: %s", m.output)
	}
}

func TestTargetChoiceFormCanRemovePreviouslySavedEmptyEntry(t *testing.T) {
	m := NewContext(context.Background(), &setupBackendFixture{})
	m.Update(tea.WindowSizeMsg{Width: 110, Height: 24})
	m.openSetupForm(viewmodel.SetupPreview{
		Key: state.Key{Source: "team-source", Package: "cluster-inspector", Target: "pms15"},
		Inputs: []viewmodel.SetupInput{{
			Definition: catalog.Input{Name: "connections", Label: "Read-only database queries (optional)", Type: "multichoice", OptionsFrom: "dbms.*.tenants.*", Options: []catalog.Choice{{Value: "shared_postgres/plane", Label: "Plane — shared_postgres/plane"}}},
			Value:      []string{""}, HasValue: true, Provenance: "saved", Editable: true,
		}},
		Destinations: []viewmodel.SetupDestination{{ID: "codex", Path: "/tmp/codex/config.toml", Selected: true}},
	})
	if !strings.Contains(m.View().Content, "[x] Empty saved entry — deselect to remove") {
		t.Fatalf("saved empty entry cannot be identified or removed: %s", m.View().Content)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if strings.Contains(m.View().Content, "[x] Empty saved entry") {
		t.Fatalf("saved empty entry stayed selected after Enter: %s", m.View().Content)
	}
}
func TestFixedTargetInputIsHiddenAndNotSubmitted(t *testing.T) {
	b := &setupBackendFixture{extraInputs: []viewmodel.SetupInput{{Definition: catalog.Input{Name: "api_server", Label: "API server", Type: "string", Required: true}, Value: "https://fixed.example", HasValue: true, Provenance: "target", Editable: false}}}
	m := NewContext(context.Background(), b)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	m.Update(m.Init()())
	m.Update(m.homeOperation("parameters")())
	if strings.Contains(m.View().Content, "API server") {
		t.Fatal("fixed target input appeared in form")
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("Save unavailable")
	}
	m.Update(cmd())
	if b.installRequest == nil {
		t.Fatal("Save did not submit")
	}
	if _, ok := b.installRequest.Inputs["api_server"]; ok {
		t.Fatalf("fixed target input was submitted: %+v", b.installRequest.Inputs)
	}
}

func TestLongProvenanceKeepsInputValueVisible(t *testing.T) {
	m := NewContext(context.Background(), &setupBackendFixture{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.openSetupForm(viewmodel.SetupPreview{
		Key: state.Key{Source: "team-source", Package: "plain", Target: "default"},
		Inputs: []viewmodel.SetupInput{{Definition: catalog.Input{Name: "repo", Label: "Repository", Type: "string"},
			Value: "/repos/team", HasValue: true, Provenance: "source",
			ProvenancePath: "/a/very/long/checkout/path/for/a/company/private/capabilities/repository/that/exceeds/the/terminal/width/aact.toml", Editable: true}},
		Destinations: []viewmodel.SetupDestination{{ID: "all", Path: "/home/test/.agents/skills", Selected: true}},
	})
	view := m.View().Content
	if !strings.Contains(view, "Repository [source]: /repos/team") || !strings.Contains(view, "aact.toml") {
		t.Fatalf("provenance hid the field's value: %s", view)
	}
}

func TestManagedProfileParametersOpenItsExactSetupTarget(t *testing.T) {
	b := &setupEnvironmentBackend{}
	m := NewContext(context.Background(), b)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m.Update(m.Init()())
	m.catalog[0].MCP = &catalog.MCP{Transport: "streamable-http"}
	m.profileSnapshot = &viewmodel.ProfileSnapshot{Profiles: []viewmodel.Profile{{Key: state.Key{Source: "team-source", Package: "plain", Environment: "company", Target: "production"}, RuntimeStatus: "never-started", Ownership: "local"}}}
	m.reconcileHome()
	m.focusPane(ProfilesPane)
	m.home.Modal = &modalState{Kind: "actions"}
	if !strings.Contains(m.menuEntries()[3], "Edit parameters") || strings.Contains(m.menuEntries()[3], "disabled") {
		t.Fatalf("managed profile parameters unavailable: %s", m.menuEntries()[3])
	}
	cmd := m.homeOperation("parameters")
	if cmd == nil || !m.busy {
		t.Fatal("Parameters did not request the setup form")
	}
	m.Update(cmd())
	if b.previewRequest != (viewmodel.SetupRequest{SourceID: "team-source", PackageID: "plain", Environment: "company", Target: "production"}) || m.form == nil {
		t.Fatalf("wrong profile target or missing form: %+v form=%v", b.previewRequest, m.form)
	}
}

func TestCapabilitySetupUsesItsOnlyEnvironmentTarget(t *testing.T) {
	b := &setupBackendFixture{}
	m := NewContext(context.Background(), b)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m.Update(m.Init()())
	m.environmentSnapshot = &viewmodel.EnvironmentSnapshot{Targets: []viewmodel.EnvironmentTarget{
		{SourceID: "team-source", Environment: "home", PackageID: "plain", Name: "pms15", Path: "/environments/home/plain/pms15.toml"},
	}}
	m.reconcileHome()
	cmd := m.homeOperation("parameters")
	if cmd == nil {
		t.Fatal("setup did not request preview")
	}
	m.Update(cmd())
	want := viewmodel.SetupRequest{SourceID: "team-source", PackageID: "plain", Environment: "home", Target: "pms15"}
	if b.previewRequest != want {
		t.Fatalf("capability setup discarded local target: got %+v, want %+v", b.previewRequest, want)
	}
}

func TestSetupFormNamesCapabilityAndEnvironmentTarget(t *testing.T) {
	m := NewContext(context.Background(), &setupBackendFixture{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m.openSetupForm(viewmodel.SetupPreview{
		Key:          state.Key{Source: "team-source", Package: "cluster-inspector", Environment: "home", Target: "pms15"},
		PackageName:  "Cluster Inspector",
		Inputs:       []viewmodel.SetupInput{{Definition: catalog.Input{Name: "token", Label: "Token", Type: "secret"}, Editable: true}},
		Destinations: []viewmodel.SetupDestination{{ID: "codex", Path: "/home/test/.codex", Selected: true}},
	})
	view := m.View().Content
	for _, want := range []string{"Install · Cluster Inspector", "home / pms15", "Destinations"} {
		if !strings.Contains(view, want) {
			t.Fatalf("setup form does not show %q:\n%s", want, view)
		}
	}
}

func TestExclusiveCredentialsShowMethodAndInactiveBranch(t *testing.T) {
	m := NewContext(context.Background(), &setupBackendFixture{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m.openSetupForm(viewmodel.SetupPreview{
		Key:         state.Key{Source: "team-source", Package: "inspect", Environment: "company", Target: "production"},
		PackageName: "Inspector",
		Inputs: []viewmodel.SetupInput{
			{Definition: catalog.Input{Name: "token", Label: "Token", Type: "secret", ExclusiveGroup: "credential"}, Editable: true},
			{Definition: catalog.Input{Name: "kubeconfig", Label: "Source kubeconfig", Type: "file", ExclusiveGroup: "credential"}, Editable: true},
		},
		Destinations: []viewmodel.SetupDestination{{ID: "codex", Path: "/home/test/.codex", Selected: true}},
	})
	view := m.View().Content
	for _, want := range []string{"Environment: company", "Target: production", "Authentication", "Token", "Source kubeconfig", "inactive", "Destinations", "Codex"} {
		if !strings.Contains(view, want) {
			t.Fatalf("setup missing %q:\n%s", want, view)
		}
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	view = m.View().Content
	if !strings.Contains(view, "Token: ") || !strings.Contains(view, "inactive while Source kubeconfig") {
		t.Fatalf("credential selector did not disable Token:\n%s", view)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if strings.Contains(m.View().Content, "Edit:") || !strings.Contains(m.View().Content, "inactive while Source kubeconfig") {
		t.Fatal("inactive field opened an editor or hid its reason")
	}
}

func TestNoEnvironmentSetupTitleHasNoBlankSegment(t *testing.T) {
	m := NewContext(context.Background(), &setupBackendFixture{})
	m.openSetupForm(viewmodel.SetupPreview{Key: state.Key{Source: "one", Package: "inspect", Target: "default"}, PackageName: "Inspector", Destinations: []viewmodel.SetupDestination{{ID: "codex", Selected: true}}})
	view := m.View().Content
	if strings.Contains(view, "·  / default") || !strings.Contains(view, "Environment: No environment file") {
		t.Fatalf("no-environment title/context is unclear: %s", view)
	}
}

func TestSwitchingAuthenticationClearsPreviouslyPrefilledCredential(t *testing.T) {
	b := &setupBackendFixture{}
	m := NewContext(context.Background(), b)
	m.openSetupForm(viewmodel.SetupPreview{
		Key: state.Key{Source: "team-source", Package: "inspect", Target: "default"},
		Inputs: []viewmodel.SetupInput{
			{Definition: catalog.Input{Name: "token", Type: "secret", ExclusiveGroup: "credential"}, Value: "old-token", HasValue: true, Editable: true},
			{Definition: catalog.Input{Name: "kubeconfig", Type: "file", ExclusiveGroup: "credential"}, Editable: true},
		},
		Destinations: []viewmodel.SetupDestination{{ID: "codex", Selected: true}},
	})
	m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	_, cmd := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatalf("switching authentication blocked Save: %s", m.View().Content)
	}
	m.Update(cmd())
	if b.installRequest == nil || b.installRequest.Inputs["token"] != "" {
		t.Fatalf("inactive prefilled token was submitted: %+v", b.installRequest)
	}
}

func TestBothPrefilledCredentialsKeepOnlySelectedMethod(t *testing.T) {
	b := &setupBackendFixture{}
	m := NewContext(context.Background(), b)
	m.openSetupForm(viewmodel.SetupPreview{
		Key: state.Key{Source: "team-source", Package: "inspect", Target: "default"},
		Inputs: []viewmodel.SetupInput{
			{Definition: catalog.Input{Name: "token", Type: "secret", ExclusiveGroup: "credential"}, Value: "old-token", HasValue: true, Editable: true},
			{Definition: catalog.Input{Name: "kubeconfig", Type: "file", ExclusiveGroup: "credential"}, Value: "/tmp/existing-kubeconfig", HasValue: true, Editable: true},
		},
		Destinations: []viewmodel.SetupDestination{{ID: "codex", Selected: true}},
	})
	_, cmd := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatalf("prefilled exclusive credentials blocked Save: %s", m.View().Content)
	}
	m.Update(cmd())
	if b.installRequest == nil || b.installRequest.Inputs["token"] != "" || b.installRequest.Inputs["kubeconfig"] != "/tmp/existing-kubeconfig" {
		t.Fatalf("inactive prefilled credential was submitted: %+v", b.installRequest)
	}
}

func TestFixedTargetCredentialDisablesOtherMethod(t *testing.T) {
	b := &setupBackendFixture{}
	m := NewContext(context.Background(), b)
	m.openSetupForm(viewmodel.SetupPreview{
		Key: state.Key{Source: "team-source", Package: "inspect", Target: "default"},
		Inputs: []viewmodel.SetupInput{
			{Definition: catalog.Input{Name: "token", Label: "Token", Type: "secret", ExclusiveGroup: "credential"}, Value: "fixed-token", HasValue: true, Provenance: "target", Editable: false},
			{Definition: catalog.Input{Name: "kubeconfig", Label: "Source kubeconfig", Type: "file", ExclusiveGroup: "credential"}, Value: "/tmp/old-kubeconfig", HasValue: true, Editable: true},
		},
		Destinations: []viewmodel.SetupDestination{{ID: "codex", Selected: true}},
	})
	view := m.View().Content
	if strings.Contains(view, "Authentication") || !strings.Contains(view, "fixed by target") {
		t.Fatalf("fixed credential offered another method: %s", view)
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatalf("fixed credential blocked Save: %s", m.View().Content)
	}
	m.Update(cmd())
	if b.installRequest == nil || b.installRequest.Inputs["kubeconfig"] != "" {
		t.Fatalf("fixed credential's sibling was submitted: %+v", b.installRequest)
	}
	if _, submitted := b.installRequest.Inputs["token"]; submitted {
		t.Fatalf("fixed target credential was submitted: %+v", b.installRequest.Inputs)
	}
}
