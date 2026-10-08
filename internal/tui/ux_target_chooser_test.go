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
	return viewmodel.SetupPreview{Key: state.Key{Source: q.SourceID, Package: q.PackageID, Environment: q.Environment, Target: q.Target}, PackageName: "Inspector", MCP: true, HasManifestUI: true, Sections: []catalog.Section{{ID: "connection", Title: "Connection", Fields: []string{"value"}}}, Inputs: []viewmodel.SetupInput{{Definition: catalog.Input{Name: "value", Label: "Value", Type: "string"}, Editable: true}}}, nil
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
	m.homeOperation("choose-preset")
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

func TestMCPConfigureUsesPresetChooserWhenTargetsAreAvailable(t *testing.T) {
	for _, action := range []string{"parameters", "i"} {
		t.Run(action, func(t *testing.T) {
			m, _ := homeFixture()
			m.backend = chooserSetupBackend{}
			m.environmentSnapshot = &viewmodel.EnvironmentSnapshot{SourceID: "one", Targets: []viewmodel.EnvironmentTarget{
				{SourceID: "one", Environment: "sample-env", PackageID: "inspect", Name: "target-a", Path: "/environments/sample-env/inspect/target-a.toml"},
			}}
			m.home.Modal = &modalState{Kind: "actions"}
			for _, item := range m.homeMenuItems() {
				if item.Action == "parameters" && item.Label != "Set up another target…" {
					t.Fatalf("MCP primary setup label hid target selection: %q", item.Label)
				}
			}
			m.home.Modal = nil
			if cmd := m.homeOperation(action); cmd != nil {
				t.Fatal("MCP Configure bypassed the available target chooser")
			}
			if m.home.Modal == nil || m.home.Modal.Kind != "target-chooser" {
				t.Fatalf("MCP Configure did not open preset chooser: modal=%+v output=%q", m.home.Modal, m.output)
			}
			view := ansi.Strip(m.View().Content)
			if !strings.Contains(view, "sample-env / target-a") || !strings.Contains(view, "Without an environment preset") {
				t.Fatalf("preset chooser omitted the named target or explicit no-preset choice:\n%s", view)
			}
			cmd := m.chooseTarget(0)
			if cmd == nil {
				t.Fatal("selecting the named preset did not start setup")
			}
			m.Update(cmd())
			want := state.Key{Source: "one", Package: "inspect", Environment: "sample-env", Target: "target-a"}
			if m.pendingSetup == nil || m.pendingSetup.Key != want {
				t.Fatalf("named target was not retained through setup: %+v want %+v", m.pendingSetup, want)
			}
			if !strings.Contains(ansi.Strip(m.View().Content), "Inspector · sample-env / target-a") {
				t.Fatalf("workspace title omitted selected MCP target identity:\n%s", ansi.Strip(m.View().Content))
			}
		})
	}
}

func TestMCPConfigureWithoutNamedPresetsOpensFullInputsDirectly(t *testing.T) {
	m, _ := homeFixture()
	m.backend = chooserSetupBackend{}
	cmd := m.homeOperation("parameters")
	if cmd == nil {
		t.Fatal("MCP setup without presets did not start")
	}
	if m.home.TargetChooser != nil || m.home.Modal != nil {
		t.Fatalf("MCP setup without a named preset received an empty chooser: chooser=%+v modal=%+v", m.home.TargetChooser, m.home.Modal)
	}
	m.Update(cmd())
	if m.pendingSetup == nil || m.pendingSetup.Key.Environment != "" || m.pendingSetup.Key.Target != "" || m.form == nil {
		t.Fatalf("preset-free setup did not open full inputs for the no-preset target: preview=%+v form=%v", m.pendingSetup, m.form != nil)
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
	m.homeOperation("choose-preset")
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
	for _, want := range []string{"Package unavailable", "Locate source", "View saved information"} {
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
	if !strings.Contains(ansi.Strip(m.View().Content), "F3 Information") {
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
