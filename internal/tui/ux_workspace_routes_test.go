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
		Key:         state.Key{Source: "team-source", Package: "grafana-inspector", Environment: "sample-env", Target: "production"},
		MCP:         true,
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
		PackageName: "Inspector", Configured: true, MCP: true,
		HasManifestUI: true, Sections: []catalog.Section{{ID: "connection", Title: "Connection", Fields: []string{"endpoint"}}},
		MCPDefinitions: []catalog.MCP{{Name: "inspector"}},
		Inputs:         []viewmodel.SetupInput{{Definition: catalog.Input{Name: "endpoint", Label: "Endpoint", Type: "string"}, Value: "https://fixture.example/mcp", HasValue: true, Editable: true}},
	}, nil
}

type workspaceApplyBackend struct {
	setupBackendFixture
	withMCP      bool
	installCalls int
}

func (b *workspaceApplyBackend) UISetupPreview(_ context.Context, request viewmodel.SetupRequest) (viewmodel.SetupPreview, error) {
	b.previewRequest = request
	return viewmodel.SetupPreview{
		Key:         state.Key{Source: request.SourceID, Package: request.PackageID, Environment: request.Environment, Target: request.Target},
		PackageName: "Inspector", Configured: true, MCP: b.withMCP,
		MCPDefinitions: func() []catalog.MCP {
			if b.withMCP {
				return []catalog.MCP{{Name: "inspector"}}
			}
			return nil
		}(),
		Inputs:       []viewmodel.SetupInput{{Definition: catalog.Input{Name: "repo", Label: "Repository", Type: "string", Required: true}, Value: "/repos/team", HasValue: true, Editable: true}},
		Destinations: []viewmodel.SetupDestination{{ID: "codex", Path: "/tmp/codex/config.toml", Selected: true}},
	}, nil
}

func (b *workspaceApplyBackend) UIInstall(ctx context.Context, request viewmodel.SetupInstallRequest) (viewmodel.OperationResult, error) {
	b.installCalls++
	return b.setupBackendFixture.UIInstall(ctx, request)
}

type profileWorkspaceBackend struct {
	*profileBackend
	*workspaceRouteBackend
}

func TestUXProfileConfigurationOpensSharedGenericWorkspace(t *testing.T) {
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
	cmd := m.homeOperation("parameters")
	if cmd == nil {
		t.Fatal("profile configuration did not load the shared target workspace")
	}
	m.Update(cmd())
	section := ""
	if m.form != nil {
		section = m.form.SectionTitle()
	}
	if setup.previewRequest.Target != "foreign" || section != "Overview" || m.workspace == nil || m.workspace.SectionID != sectionOverviewID {
		t.Fatalf("profile configuration did not open the generic workspace on the exact target: request=%+v section=%q form=%v workspace=%+v", setup.previewRequest, section, m.form != nil, m.workspace)
	}
}

func TestUXOverviewAgentActionSelectsTheSharedAgentsSection(t *testing.T) {
	m, _ := typedProfileFixture()
	setup := &workspaceRouteBackend{}
	m.backend = profileWorkspaceBackend{profileBackend: m.backend.(*profileBackend), workspaceRouteBackend: setup}
	key := state.Key{Source: "team-source", Package: "plain", Environment: "dev", Target: "foreign"}
	cmd := m.openTargetWorkspace(viewmodel.SetupRequest{SourceID: key.Source, PackageID: key.Package, Environment: key.Environment, Target: key.Target}, "Overview")
	m.Update(cmd())
	if m.form == nil || !strings.Contains(ansi.Strip(m.form.View().Content), "Configure agent destinations") {
		t.Fatalf("Overview does not offer the shared Agents action:\n%s", ansi.Strip(m.View().Content))
	}
	handled, _ := m.workspaceOverviewAction("g")
	if !handled || m.workspace == nil || m.workspace.SectionID != sectionAgentsID || m.form == nil || !m.workspace.Active || m.registration != nil {
		t.Fatalf("Agents action did not select the one complete-binding workspace: registration=%+v form=%v workspace=%+v", m.registration, m.form != nil, m.workspace)
	}
	view := ansi.Strip(m.View().Content)
	for _, want := range []string{"Capabilities", "Selected capability", "Agents", "No named agent destinations"} {
		if !strings.Contains(view, want) {
			t.Errorf("shared Agents workspace lacks %q:\n%s", want, view)
		}
	}
	if m.workspace.Section != "Agents" {
		t.Fatalf("Agents action did not retain the selected section: %q", m.workspace.Section)
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
	preview := viewmodel.SetupPreview{Key: state.Key{Source: "team", Package: "forgejo-inspector", Target: "prod"}, Configured: true, MCP: true}
	profile := &viewmodel.Profile{
		Key: preview.Key, RuntimeStatus: "running", Ownership: "unknown", URL: "https://mcp.example.test/mcp",
		ObservedAt: time.Date(2026, 10, 6, 10, 0, 0, 0, time.UTC), ObservationStale: true,
		RegisteredAgents: []string{"codex"},
	}
	lines := workspaceOverviewLines(preview, true, profile)
	if strings.Contains(strings.Join(lines, "\n"), "Actions: [c]") {
		t.Error("Overview still presents section navigation as runtime actions")
	}
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"Configuration: saved · package installation: recorded", "MCP runtime: running", "Ownership: unknown", "Endpoint: https://mcp.example.test/mcp"} {
		if !strings.Contains(joined, want) {
			t.Errorf("Overview omitted distinct state %q:\n%s", want, joined)
		}
	}
	profileFacts := strings.Join(workspaceProfileInformationLines(profile), "\n")
	for _, want := range []string{"Local registrations: codex", "Live observation: stale"} {
		if !strings.Contains(profileFacts, want) {
			t.Errorf("Information omitted observed profile fact %q:\n%s", want, profileFacts)
		}
	}
}

func TestUXOverviewHasNoApplyActionAndBottomSaveSubmitsCurrentConfiguration(t *testing.T) {
	setup := &workspaceApplyBackend{withMCP: true}
	m := NewContext(t.Context(), setup)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 32})
	key := state.Key{Source: "team-source", Package: "plain", Environment: "dev", Target: "prod"}
	cmd := m.openTargetWorkspace(viewmodel.SetupRequest{SourceID: key.Source, PackageID: key.Package, Environment: key.Environment, Target: key.Target}, "Overview")
	m.Update(cmd())
	view := ansi.Strip(m.form.View().Content)
	if strings.Contains(view, "Save and apply · configure agents") || strings.Contains(strings.ToLower(view), "build, start, register") || !strings.Contains(view, "Save and apply") {
		t.Fatalf("Overview should contain facts/navigation and the form one bottom Save and apply control:\n%s", view)
	}
	if strings.Contains(view, "Save and apply ·") {
		t.Fatal("Overview content contains a second Save and apply action")
	}
	_, cmd = m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("bottom form Save and apply did not invoke UIInstall")
	}
	m.Update(runTeaCmd(t, m, cmd))
	if setup.installRequest == nil {
		t.Fatal("bottom Save and apply bypassed the unified setup service")
	}
	if setup.installRequest.SetupRequest.Target != key.Target || setup.installRequest.Inputs["repo"] != "/repos/team" {
		t.Fatalf("bottom Save and apply did not submit the current target draft: %+v", setup.installRequest)
	}
}

func TestUXOverviewApplyCanBeRepeatedAfterSuccessfulResult(t *testing.T) {
	setup := &workspaceApplyBackend{withMCP: true}
	m := NewContext(t.Context(), setup)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 32})
	key := state.Key{Source: "team-source", Package: "plain", Environment: "dev", Target: "prod"}
	cmd := m.openTargetWorkspace(viewmodel.SetupRequest{SourceID: key.Source, PackageID: key.Package, Environment: key.Environment, Target: key.Target}, "Overview")
	m.Update(cmd())

	activate := func(dismiss bool) {
		t.Helper()
		if m.form == nil {
			cmd := m.openTargetWorkspace(viewmodel.SetupRequest{SourceID: key.Source, PackageID: key.Package, Environment: key.Environment, Target: key.Target}, "Overview")
			if cmd == nil {
				t.Fatal("workspace could not be reopened after the previous successful result")
			}
			m.Update(cmd())
		}
		_, install := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
		if install == nil {
			t.Fatalf("Save and apply did not submit UIInstall: active=%t pending=%v form=%t busy=%t", m.workspace != nil && m.workspace.Active, m.pendingSetup != nil, m.form != nil, m.busy)
		}
		m.Update(runTeaCmd(t, m, install))
		if m.result == nil || m.result.Failed {
			t.Fatalf("successful apply did not show its result: %+v", m.result)
		}
		if dismiss {
			m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		}
	}

	activate(true)
	if setup.installCalls != 1 || setup.installRequest == nil {
		t.Fatal("first Save and apply did not call UIInstall")
	}
	activate(false)
	if setup.installCalls != 2 || setup.installRequest == nil || m.busy || m.result == nil {
		t.Fatalf("second Overview apply did not run through progress and result: request=%+v busy=%t result=%+v", setup.installRequest, m.busy, m.result)
	}
}

func TestUXSkillOnlyTargetDoesNotOfferMCPRuntimeActions(t *testing.T) {
	setup := &workspaceApplyBackend{}
	m := NewContext(t.Context(), setup)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 32})
	key := state.Key{Source: "team-source", Package: "skill-only", Environment: "dev", Target: "default"}
	cmd := m.openTargetWorkspace(viewmodel.SetupRequest{SourceID: key.Source, PackageID: key.Package, Environment: key.Environment, Target: key.Target}, "Overview")
	m.Update(cmd())
	view := ansi.Strip(m.form.View().Content)
	for _, forbidden := range []string{"MCP runtime", "Build and start MCP", "Stop MCP", "build, start, register"} {
		if strings.Contains(view, forbidden) {
			t.Fatalf("skill-only target exposed runtime action %q:\n%s", forbidden, view)
		}
	}
	if !strings.Contains(view, "[ Save and apply ]") {
		t.Fatalf("skill-only target lost its Save and Apply action:\n%s", view)
	}
}

func TestUXWorkspaceDraftCacheRetainsDraftAndParentSelection(t *testing.T) {
	key := state.Key{Source: "team", Package: "grafana-inspector", Environment: "sample-env", Target: "prod"}
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
