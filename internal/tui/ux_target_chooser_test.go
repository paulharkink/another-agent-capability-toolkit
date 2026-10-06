package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

type chooserSetupBackend struct{ fixtureBackend }

func (chooserSetupBackend) UISetupPreview(_ context.Context, q viewmodel.SetupRequest) (viewmodel.SetupPreview, error) {
	return viewmodel.SetupPreview{Key: state.Key{Source: q.SourceID, Package: q.PackageID, Environment: q.Environment, Target: q.Target}, Inputs: []viewmodel.SetupInput{{Definition: catalog.Input{Name: "value", Label: "Value", Type: "string"}, Editable: true}}}, nil
}
func (chooserSetupBackend) UIInstall(context.Context, viewmodel.SetupInstallRequest) (viewmodel.OperationResult, error) {
	return viewmodel.OperationResult{}, nil
}

func TestUXMultipleTargetsOpenLocalChooserAndSelectionOpensCorrectKey(t *testing.T) {
	m, _ := homeFixture()
	m.backend = chooserSetupBackend{}
	m.environmentSnapshot = &viewmodel.EnvironmentSnapshot{SourceID: "one", Targets: []viewmodel.EnvironmentTarget{
		{SourceID: "one", Environment: "dev", PackageID: "inspect", Name: "local", Path: "/tmp/dev/inspect/local.toml"},
		{SourceID: "one", Environment: "prod", PackageID: "inspect", Name: "live", Path: "/tmp/prod/inspect/live.toml"},
	}}
	m.homeOperation("parameters")
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "dev / local") || !strings.Contains(view, "prod / live") {
		t.Fatalf("chooser does not list applicable targets in place:\n%s", view)
	}
	if strings.Contains(view, "Multiple environment targets are available") {
		t.Fatalf("chooser remains a footer prerequisite:\n%s", view)
	}
	_, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil {
		m.Update(cmd())
	}
	if m.pendingSetup == nil || m.pendingSetup.Key.Environment != "prod" || m.pendingSetup.Key.Target != "live" {
		t.Fatalf("selected target did not open its exact setup key: %#v", m.pendingSetup)
	}
}

func TestUXExistingSavedTargetRowOpensItsExactSetupKey(t *testing.T) {
	m, _ := homeFixture()
	m.backend = chooserSetupBackend{}
	m.inventory = []state.Installation{{Key: state.Key{Source: "one", Package: "inspect", Environment: "staging", Target: "canary"}, AgentID: "codex", Component: "mcp"}}
	rows := m.contextRows()
	found := -1
	for i, row := range rows {
		if row.Kind == "target" && strings.Contains(row.Label, "staging / canary") {
			found = i
			break
		}
	}
	if found < 0 {
		t.Fatalf("existing target was not listed by environment / target: %#v", rows)
	}
	m.home.Focus = ProfilesPane
	m.home.Context.Index = found
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("opening existing saved target did not start its workspace")
	}
	m.Update(cmd())
	if m.pendingSetup == nil || m.pendingSetup.Key.Environment != "staging" || m.pendingSetup.Key.Target != "canary" {
		t.Fatalf("existing target opened wrong setup key: %#v", m.pendingSetup)
	}
}

func TestUXNoPresetIsExplicitChoice(t *testing.T) {
	m, _ := homeFixture()
	m.backend = chooserSetupBackend{}
	m.environmentSnapshot = &viewmodel.EnvironmentSnapshot{SourceID: "one", Targets: []viewmodel.EnvironmentTarget{
		{SourceID: "one", Environment: "dev", PackageID: "inspect", Name: "local", Path: "/tmp/dev/inspect/local.toml"},
	}}
	m.homeOperation("parameters")
	if !strings.Contains(ansi.Strip(m.View().Content), "Without an environment preset") {
		t.Fatalf("chooser has no explicit no-preset option:\n%s", ansi.Strip(m.View().Content))
	}
}

func TestUXUnavailableSourceHasRecoveryInsteadOfDeadConfigure(t *testing.T) {
	m, _ := homeFixture()
	m.backend = chooserSetupBackend{}
	m.profileSnapshot = &viewmodel.ProfileSnapshot{Profiles: []viewmodel.Profile{{Key: state.Key{Source: "gone", Package: "legacy", Target: "default"}, Name: "Old capability"}}}
	rows := m.capabilities()
	if len(rows) < 2 || rows[len(rows)-1].CatalogIndex >= 0 {
		t.Fatalf("unavailable saved capability disappeared: %#v", rows)
	}
	m.home.Capabilities.ID = rows[len(rows)-1].ID
	m.reconcileHome()
	view := ansi.Strip(m.View().Content)
	for _, want := range []string{"Package unavailable", "Locate source", "View saved information", "Remove local registration"} {
		if !strings.Contains(view, want) {
			t.Fatalf("unavailable source lacks %q recovery:\n%s", want, view)
		}
	}
}

func TestUXEnterL1OnlyFocusesL2(t *testing.T) {
	m, _ := homeFixture()
	m.backend = chooserSetupBackend{}
	press(m, tea.KeyEnter, "")
	if m.home.Focus != ProfilesPane || m.home.Modal != nil || m.form != nil {
		t.Fatalf("Enter at L1 opened an action instead of focusing L2: focus=%v modal=%#v form=%#v", m.home.Focus, m.home.Modal, m.form)
	}
}

func TestUXEnvironmentPresetDoesNotImplyInstalledAndNoSavedDuplicateRows(t *testing.T) {
	m, _ := homeFixture()
	m.environmentSnapshot = &viewmodel.EnvironmentSnapshot{SourceID: "one", Environments: []string{"prod"}, Targets: []viewmodel.EnvironmentTarget{
		{SourceID: "one", Environment: "prod", PackageID: "inspect", Name: "live", Path: "/env/prod/inspect/live.toml"},
	}}
	m.profileSnapshot = &viewmodel.ProfileSnapshot{Profiles: []viewmodel.Profile{{Key: state.Key{Source: "one", Package: "inspect", Environment: "prod", Target: "live"}, Name: "Inspector"}}}
	entries := m.environmentEntries()
	if len(entries) < 2 || !strings.Contains(entries[1].Name, "TOML files") || strings.Contains(entries[1].Name, "Saved profile") {
		t.Fatalf("preset and saved installation were conflated: %#v", entries)
	}
	if len(entries[1].Targets) != 1 || !strings.Contains(entries[1].Targets[0], "inspect / live") {
		t.Fatalf("saved installation duplicated a TOML target: %#v", entries[1].Targets)
	}
	capability := CapabilityRow{ID: "one\x00inspect", Source: "one", Package: "inspect", Name: "Inspector", MCP: true, CatalogIndex: 0}
	status, _ := m.installationDetails(capability)
	if status != "Not installed" {
		t.Fatalf("a TOML preset implied installation: %s", status)
	}
}

func TestUXSetupReturnPreservesEnvironmentOrigin(t *testing.T) {
	m, _ := homeFixture()
	m.backend = chooserSetupBackend{}
	m.navigate("Environments")
	m.management.EnvironmentIndex = 1
	m.management.TargetIndex = 0
	m.environmentSnapshot = &viewmodel.EnvironmentSnapshot{SourceID: "one", Environments: []string{"prod"}, Targets: []viewmodel.EnvironmentTarget{{SourceID: "one", Environment: "prod", PackageID: "inspect", Name: "live", Path: "/env/prod/inspect/live.toml"}}}
	if got := m.environmentEntries(); len(got) < 2 || got[1].NoFile {
		t.Fatalf("environment origin fixture missing: %#v", got)
	}
	m.management.Focus = ProfilesPane
	m.management.TargetIndex = 0
	m.management.Modal = "actions"
	m.management.ModalSelected = 0
	cmd := m.managementModalKey("enter")
	if cmd != nil {
		m.Update(cmd())
	}
	if m.pendingSetup == nil || m.pendingSetup.Key.Environment != "prod" || m.pendingSetup.Key.Target != "live" || m.view != "Environments" || m.management.EnvironmentIndex != 1 || m.management.TargetIndex != 0 {
		t.Fatalf("opening setup lost invoking parent selection: view=%s environment=%d target=%d", m.view, m.management.EnvironmentIndex, m.management.TargetIndex)
	}
}

func TestUXTargetInformationShowsRawAndResolvedPath(t *testing.T) {
	m := NewContext(context.Background(), chooserSetupBackend{})
	m.Update(tea.WindowSizeMsg{Width: 90, Height: 18})
	preview := viewmodel.SetupPreview{
		Key:        state.Key{Source: "one", Package: "inspect", Environment: "prod", Target: "live"},
		SourceRoot: "/sources/inspect", TargetPath: "/env/prod/inspect/live.toml", TargetTOML: "[inputs]\ncertificate = '../cert/client.pem'\n",
		Inputs: []viewmodel.SetupInput{{Definition: catalog.Input{Name: "certificate", Label: "Certificate", Type: "file"}, Value: "/env/prod/cert/client.pem", HasValue: true, Provenance: "target", ProvenancePath: "/env/prod/inspect/live.toml", Editable: true}},
	}
	m.Update(setupPreviewMsg{preview: preview})
	if !strings.Contains(ansi.Strip(m.View().Content), "F3 Target information") {
		t.Fatal("setup offers no target information action")
	}
	press(m, tea.KeyF3, "")
	view := ansi.Strip(m.View().Content)
	for _, want := range []string{"Raw TOML", "certificate = '../cert/client.pem'", "/env/prod/cert/client.pem", "/env/prod/inspect/live.toml"} {
		if !strings.Contains(view, want) {
			t.Fatalf("target information lacks %q:\n%s", want, view)
		}
	}
	press(m, tea.KeyEscape, "")
	if m.form == nil || m.setupInformation {
		t.Fatal("return from target information did not preserve setup form")
	}
}
