package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

func TestUXCapabilityAndTargetDetailsContainRealInformation(t *testing.T) {
	m := NewContext(t.Context(), &setupBackendFixture{})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 28})
	m.pendingSetup = &viewmodel.SetupPreview{
		Key:         state.Key{Source: "team-source", Package: "grafana-inspector", Environment: "home", Target: "production"},
		PackageName: "Grafana Inspector", SourceRoot: "/sources/team", TargetPath: "/envs/home/production.toml",
		TargetTOML: "[grafana]\nurl = \"https://grafana.example.test\"\n",
		Inputs: []viewmodel.SetupInput{
			{Definition: catalog.Input{Name: "url", Label: "Grafana URL"}, Value: "https://grafana.example.test", HasValue: true, Provenance: "target", ProvenancePath: "/envs/home/production.toml", Editable: true},
			{Definition: catalog.Input{Name: "fixed_region", Label: "Region"}, Value: "eu-west", HasValue: true, Provenance: "target-fixed", ProvenancePath: "/envs/home/production.toml", Editable: false},
		},
		Destinations: []viewmodel.SetupDestination{{ID: "codex", Path: "/home/test/.codex", ConfigPath: "/home/test/.codex/config.toml", Selected: true}},
	}
	view := m.setupInformationView().Content
	for _, want := range []string{"Grafana Inspector", "package grafana-inspector", "team-source", "/envs/home/production.toml", "eu-west", "target-fixed", "/home/test/.codex/config.toml", "selected for apply"} {
		if !strings.Contains(view, want) {
			t.Errorf("target Information omitted %q:\n%s", want, view)
		}
	}
}

func TestUXProfileBackedConfiguredTargetEnterOpensItsExactWorkspace(t *testing.T) {
	m, _ := homeFixture()
	m.backend = chooserSetupBackend{}
	key := state.Key{Source: "one", Package: "inspect", Environment: "prod", Target: "live"}
	m.profileSnapshot = &viewmodel.ProfileSnapshot{Profiles: []viewmodel.Profile{{Key: key, Name: "Live runtime", RuntimeStatus: "running", Ownership: "local", URL: "http://localhost:8765/mcp"}}}
	m.reconcileHome()
	rows := m.contextRows()
	profileRow := -1
	for index, row := range rows {
		if row.Kind == "profile" && row.Key == key {
			profileRow = index
			break
		}
	}
	if profileRow < 0 {
		t.Fatalf("configured profile-backed target missing from L2: %#v", rows)
	}
	m.selectContext(profileRow)
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("Enter on configured target opened profile actions instead of loading its workspace: modal=%#v view=%s", m.home.Modal, ansi.Strip(m.View().Content))
	}
	m.Update(cmd())
	if m.form == nil || m.home.Modal != nil || m.pendingSetup == nil || m.pendingSetup.Key != key {
		t.Fatalf("configured target did not open its exact workspace: form=%v modal=%#v preview=%#v", m.form != nil, m.home.Modal, m.pendingSetup)
	}
	if m.form.SectionTitle() != "Overview" || !strings.Contains(m.form.View().Content, "Configure ·") {
		t.Fatalf("configured target did not start in its Configure Overview workspace: section=%q\n%s", m.form.SectionTitle(), ansi.Strip(m.form.View().Content))
	}
}

type workspaceRouteBackend struct{ setupBackendFixture }

func (b *workspaceRouteBackend) UISetupPreview(_ context.Context, request viewmodel.SetupRequest) (viewmodel.SetupPreview, error) {
	b.previewRequest = request
	return viewmodel.SetupPreview{
		Key:         state.Key{Source: request.SourceID, Package: request.PackageID, Environment: request.Environment, Target: request.Target},
		PackageName: "Inspector", Configured: true,
	}, nil
}

type profileWorkspaceBackend struct {
	*profileBackend
	*workspaceRouteBackend
}

func TestUXAuthenticateShortcutSelectsSharedAuthentication(t *testing.T) {
	m, _ := typedProfileFixture()
	setup := &workspaceRouteBackend{}
	m.backend = profileWorkspaceBackend{profileBackend: m.backend.(*profileBackend), workspaceRouteBackend: setup}
	m.focusPane(ProfilesPane)
	for index, row := range m.contextRows() {
		if row.Kind == "profile" && row.Key.Target == "foreign" {
			m.selectContext(index)
			break
		}
	}
	cmd := m.homeOperation("a")
	if cmd == nil {
		t.Fatal("Authenticate did not load the shared target workspace")
	}
	m.Update(cmd())
	section := ""
	if m.form != nil {
		section = m.form.SectionTitle()
	}
	if setup.previewRequest.Target != "foreign" || section != "Authentication" {
		t.Fatalf("Authenticate did not select Authentication on the exact target: request=%+v section=%q form=%v", setup.previewRequest, section, m.form != nil)
	}
}

func TestUXOverviewRegistrationActionOpensExactPairedEditor(t *testing.T) {
	m, _ := typedProfileFixture()
	setup := &workspaceRouteBackend{}
	m.backend = profileWorkspaceBackend{profileBackend: m.backend.(*profileBackend), workspaceRouteBackend: setup}
	key := state.Key{Source: "team-source", Package: "plain", Environment: "dev", Target: "foreign"}
	cmd := m.openTargetWorkspace(viewmodel.SetupRequest{SourceID: key.Source, PackageID: key.Package, Environment: key.Environment, Target: key.Target}, "Overview")
	m.Update(cmd())
	if m.form == nil || !strings.Contains(ansi.Strip(m.form.View().Content), "Manage agent registrations") {
		t.Fatalf("Overview does not offer the named registration action:\n%s", ansi.Strip(m.View().Content))
	}
	m.Update(tea.KeyPressMsg{Code: 'g'})
	if m.registration == nil || m.registration.Profile.Key != key || m.form == nil || !m.workspace.Active {
		t.Fatalf("registration editor did not retain the exact paired workspace: registration=%+v form=%v workspace=%+v", m.registration, m.form != nil, m.workspace)
	}
	view := ansi.Strip(m.View().Content)
	for _, want := range []string{"Capabilities", "Selected capability", "Endpoint URI", "Claude"} {
		if !strings.Contains(view, want) {
			t.Errorf("paired registration editor lacks %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "Workspace sections") || strings.Count(view, "Overview") > 0 {
		t.Fatalf("registration editor stacked a second L3/L4 workspace over the parent:\n%s", view)
	}
	// Click Claude's visible left row using its rendered global cell position.
	registration := m.registration
	_, _ = m.Update(tea.MouseClickMsg{X: registration.X + 3, Y: registration.Y + 6, Button: tea.MouseLeft})
	if m.registration.Row != 2 || m.registration.Area != 0 {
		t.Fatalf("rendered global click did not select Claude's row: row=%d area=%d box=%+v", m.registration.Row, m.registration.Area, m.registration)
	}
	if m.workspace.Section != "Overview" {
		t.Fatalf("registration action lost the invoking Overview section: %q", m.workspace.Section)
	}
}

func TestUXEnvironmentTargetUsesSharedInsetEditorAndBackRestoresParent(t *testing.T) {
	backend := &workspaceRouteBackend{}
	m := NewContext(t.Context(), backend)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 28})
	m.view = "Environments"
	m.management.TargetIndex = 2
	request := viewmodel.SetupRequest{SourceID: "team", PackageID: "inspect", Environment: "prod", Target: "live"}
	cmd := m.openTargetWorkspace(request, "Overview")
	if cmd == nil {
		t.Fatal("Environment target did not request the shared setup preview")
	}
	m.Update(cmd())
	if m.form == nil || m.workspace == nil || m.workspace.InvokingView != "Environments" || m.workspace.InvokingSelection != 2 {
		t.Fatalf("Environment target did not open the shared workspace with parent selection: workspace=%+v form=%v", m.workspace, m.form != nil)
	}
	if m.form.SectionTitle() != "Overview" || !strings.Contains(ansi.Strip(m.View().Content), "Configure · Inspector") {
		t.Fatalf("Environment target did not use the shared inset editor: section=%q\n%s", m.form.SectionTitle(), ansi.Strip(m.View().Content))
	}
	_, back := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if back == nil {
		t.Fatal("Escape at Overview did not produce a parent Back message")
	}
	m.Update(back())
	if m.form != nil || m.view != "Environments" || m.workspace == nil || m.workspace.Active || m.management.TargetIndex != 2 {
		t.Fatalf("Back did not restore Environment parent selection: view=%s index=%d form=%v workspace=%+v", m.view, m.management.TargetIndex, m.form != nil, m.workspace)
	}
	if len(m.workspace.cachedDraft()) == 0 {
		t.Fatal("Back did not cache the current target draft")
	}
	cmd = m.openTargetWorkspace(request, "Overview")
	m.Update(cmd())
	if m.form == nil || m.form.SectionTitle() != "Overview" {
		t.Fatal("reopening the exact Environment target did not restore its shared workspace")
	}
	_, _ = m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}) // Explicit Cancel discards the workspace.
	if m.form != nil || m.workspace != nil {
		t.Fatal("explicit Cancel did not discard the target workspace")
	}
}

func TestUXOverviewSeparatesConfiguredInstalledRunningAndReachable(t *testing.T) {
	preview := viewmodel.SetupPreview{Key: state.Key{Source: "team", Package: "forgejo-inspector", Target: "prod"}, Configured: true}
	profile := &viewmodel.Profile{
		Key: preview.Key, RuntimeStatus: "running", Ownership: "unknown", URL: "https://mcp.example.test/mcp",
		ObservedAt: time.Date(2026, 10, 6, 10, 0, 0, 0, time.UTC), ObservationStale: true,
		RegisteredAgents: []string{"codex"},
	}
	lines := workspaceOverviewLines(preview, true, profile)
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"Target configuration: saved", "Package installation: recorded", "MCP runtime: running", "Ownership: unknown", "Reachability: not checked", "Agent registration: codex", "Observation: stale"} {
		if !strings.Contains(joined, want) {
			t.Errorf("Overview omitted distinct state %q:\n%s", want, joined)
		}
	}
}

func TestUXWorkspaceDraftCacheRetainsDraftAndParentSelection(t *testing.T) {
	key := state.Key{Source: "team", Package: "grafana-inspector", Environment: "home", Target: "prod"}
	workspace := workspaceState{Key: key, Section: "Authentication", InvokingView: "Environments", InvokingSelection: 3}
	workspace.cacheDraft(map[string]any{"token": "draft-secret", "rows": []string{"one"}})
	draft := workspace.cachedDraft()
	draft["token"] = "mutated-copy"
	if workspace.Key != key || workspace.Section != "Authentication" || workspace.InvokingView != "Environments" || workspace.InvokingSelection != 3 {
		t.Fatalf("workspace did not retain invoking state: %+v", workspace)
	}
	if got := workspace.cachedDraft()["token"]; got != "draft-secret" {
		t.Fatalf("cached draft was aliased by caller: %v", got)
	}
}

func TestUXInformationWrapUsesTerminalCellWidth(t *testing.T) {
	lines := splitDisplayLine("界界界界", 5)
	if len(lines) < 2 {
		t.Fatalf("wide Unicode information was wrapped by rune count: %#v", lines)
	}
	for _, line := range lines {
		if width := lipgloss.Width(line); width > 5 {
			t.Errorf("wrapped line has terminal width %d, want <= 5: %q", width, line)
		}
	}
	if got := strings.Join(lines, ""); got != "界界界界" {
		t.Fatalf("wrapping changed information text: %q", got)
	}
}
