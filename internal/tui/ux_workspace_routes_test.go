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
	"github.com/paulharkink/another-agent-capability-toolkit/internal/forms"
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
	for _, want := range []string{"Grafana Inspector", "package grafana-inspector", "team-source", "/envs/home/production.toml", "eu-west", "target-fixed", "/home/test/.codex/config.toml", "selected for apply", "Instance information"} {
		if !strings.Contains(view, want) {
			t.Errorf("target Information omitted %q:\n%s", want, view)
		}
	}
}

func TestUXProfileBackedConfiguredTargetEnterOpensItsExactWorkspace(t *testing.T) {
	m, _ := homeFixture()
	m.backend = &workspaceRouteBackend{}
	key := state.Key{Source: "one", Package: "inspect", Target: "live"}
	m.instanceSnapshot = &viewmodel.InstanceSnapshot{Profiles: []viewmodel.Instance{{Key: key, Name: "Live runtime", RuntimeStatus: "running", Ownership: "local", URL: "http://localhost:8765/mcp"}}}
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
	if m.form.SectionID() != sectionAgentsID || m.workspace == nil || m.workspace.InstanceRef.Name != key.Target {
		t.Fatalf("configured target did not advance to Agent access for the exact instance: section=%q workspace=%+v\n%s", m.form.SectionID(), m.workspace, ansi.Strip(m.form.View().Content))
	}
}

type workspaceRouteBackend struct{ setupBackendFixture }

func (b *workspaceRouteBackend) UISetupPreview(_ context.Context, request viewmodel.SetupRequest) (viewmodel.SetupPreview, error) {
	b.previewRequest = request
	return viewmodel.SetupPreview{
		Key:         setupProfileKey(request),
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
		Key:         setupProfileKey(request),
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

func (b *workspaceApplyBackend) UIInstallAgents(ctx context.Context, request viewmodel.SetupInstallRequest) (viewmodel.OperationResult, error) {
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
	if setup.previewRequest.Ref.Name != "foreign" || setup.previewRequest.Ref.PackID != "team-source" || section != "Agent access" || m.workspace == nil || m.workspace.SectionID != sectionAgentsID {
		t.Fatalf("profile configuration did not open the generic workspace on the exact target: request=%+v section=%q form=%v workspace=%+v", setup.previewRequest, section, m.form != nil, m.workspace)
	}
}

func TestUXWorkspaceOpensExactTargetInInputsWithoutGlobalAgentsAction(t *testing.T) {
	m, _ := typedProfileFixture()
	setup := &workspaceRouteBackend{}
	m.backend = profileWorkspaceBackend{profileBackend: m.backend.(*profileBackend), workspaceRouteBackend: setup}
	key := state.Key{Source: "team-source", Package: "plain", Environment: "dev", Target: "foreign"}
	cmd := m.openTargetWorkspace(m.packInstanceRequest(key), "Overview")
	m.Update(cmd())
	if m.form == nil || m.workspace == nil || m.workspace.InstanceRef.Name != key.Target || setup.previewRequest.Ref.Name != key.Target || setup.previewRequest.Ref.PackID != key.Source || setup.previewRequest.Ref.CapabilityID != key.Package {
		t.Fatalf("exact target did not open its workspace: form=%v workspace=%+v", m.form != nil, m.workspace)
	}
	if m.form.SectionID() != sectionAgentsID || m.workspace.SectionID != sectionAgentsID {
		t.Fatalf("configured workspace did not advance to Agent access: form section=%q workspace section=%q", m.form.SectionID(), m.workspace.SectionID)
	}
	view := ansi.Strip(m.View().Content)
	if strings.Contains(view, "Configure agent destinations") || strings.Contains(view, "Overview · Agents") {
		t.Fatalf("workspace still exposes a duplicate global Agents action:\n%s", view)
	}
}

func TestUXEnvironmentTargetRequiresInstanceReference(t *testing.T) {
	backend := &workspaceRouteBackend{}
	m := NewContext(t.Context(), backend)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 28})
	request := viewmodel.SetupRequest{SourceID: "team", PackageID: "inspect", Environment: "prod", Target: "live"}
	if cmd := m.openTargetWorkspace(request, "Overview"); cmd != nil {
		t.Fatal("environment-shaped request reached instance setup")
	}
	if m.form != nil || m.workspace != nil || !strings.Contains(strings.ToLower(m.output), "instance") {
		t.Fatalf("missing InstanceRef was not rejected with a clear message: workspace=%+v form=%v output=%q", m.workspace, m.form != nil, m.output)
	}
}

func TestUXOverviewSeparatesConfiguredInstalledRunningAndReachable(t *testing.T) {
	preview := viewmodel.SetupPreview{Key: state.Key{Source: "team", Package: "git-provider", Target: "prod"}, Configured: true, MCP: true}
	profile := &viewmodel.Instance{
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
	if strings.Contains(strings.ToLower(profileFacts), "profile") {
		t.Errorf("workspace information exposes reserved profile terminology:\n%s", profileFacts)
	}
	for _, want := range []string{"Local registrations: codex", "Live observation: stale"} {
		if !strings.Contains(profileFacts, want) {
			t.Errorf("Information omitted observed profile fact %q:\n%s", want, profileFacts)
		}
	}
}

func TestUXInputsSaveIsSeparateFromAgentInstallAndRuntimeStart(t *testing.T) {
	setup := &workspaceApplyBackend{withMCP: true}
	m := NewContext(t.Context(), setup)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 32})
	key := state.Key{Source: "team-source", Package: "plain", Environment: "dev", Target: "prod"}
	cmd := m.openTargetWorkspace(m.packInstanceRequest(key), "Overview")
	m.Update(cmd())
	observedKey := m.workspace.Key
	m.capabilityInstances = map[string]viewmodel.CapabilityInstanceSnapshot{}
	m.capabilityInstances[observedKey.Package] = viewmodel.CapabilityInstanceSnapshot{Instances: []viewmodel.CapabilityInstance{{
		Key: observedKey,
		Components: []viewmodel.ComponentStatus{{AgentID: "codex", Kind: "mcp", Name: "inspector", Status: "installed"}},
	}}}
	m.workspace.Installed = m.targetHasInstallRecord(observedKey)
	m.workspace.RegisteredMCPs = m.targetRegisteredMCPs(observedKey)
	if !m.workspace.Installed || !m.workspaceMCPRegistered("inspector") {
		t.Fatal("fixture must begin with an observed installed MCP registration")
	}
	m.form.FocusSection()
	m.Update(tea.KeyPressMsg{Code: tea.KeyUp})

	inputs := ansi.Strip(m.form.View().Content)
	if !strings.Contains(inputs, "[ Save ]") || !strings.Contains(inputs, "[ Cancel ]") || strings.Contains(inputs, "Save and apply") {
		t.Fatalf("Inputs must expose Save/Cancel without a combined apply action:\n%s", inputs)
	}
	if err := m.form.ApplyValues(map[string]any{"repo": "/repos/updated"}); err != nil {
		t.Fatal(err)
	}
	_, save := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if save == nil {
		t.Fatal("Ctrl-S did not submit Inputs Save")
	}
	m.Update(save())
	if m.form.SectionID() != sectionInputsID || m.workspace.SectionID != sectionInputsID || !m.workspace.InputsSaved || !strings.Contains(m.output, "Inputs saved") {
		t.Fatalf("Inputs Save must show saved state and remain on Inputs: section=%q workspace=%q saved=%t output=%q", m.form.SectionID(), m.workspace.SectionID, m.workspace.InputsSaved, m.output)
	}
	if setup.savedInputRequest == nil || setup.installRequest != nil || setup.installCalls != 0 {
		t.Fatalf("Inputs Save did not persist answers independently: saved=%+v install=%+v calls=%d", setup.savedInputRequest, setup.installRequest, setup.installCalls)
	}
	if !m.workspace.Installed || !m.workspaceMCPRegistered("inspector") {
		t.Fatalf("saving changed capability inputs incorrectly invalidated the observed MCP registration: installed=%t registered=%v", m.workspace.Installed, m.workspace.RegisteredMCPs)
	}

	m.form.FocusSection()
	if !m.form.SelectSectionID(sectionAgentsID) {
		t.Fatal("Agent access section is unavailable")
	}
	m.form.FocusSection()
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.form.SectionID() != sectionAgentsID {
		t.Fatalf("Down did not select Agent access: %q", m.form.SectionID())
	}
	agents := ansi.Strip(m.form.View().Content)
	if !strings.Contains(agents, "[ Install selected ]") || !strings.Contains(agents, "[ Uninstall selected ]") ||
		strings.Contains(agents, "[ Save ]") || strings.Contains(agents, "[ Cancel ]") || strings.Contains(agents, "Save and apply") {
		t.Fatalf("Agent access must expose install controls without global input actions:\n%s", agents)
	}
	_, install := m.Update(forms.ActionMsg{SectionID: sectionAgentsID, ID: "install-selected"})
	if install == nil {
		t.Fatal("Agent access Install selected did not submit UIInstall")
	}
	m.Update(runTeaCmd(t, m, install))
	if setup.installCalls != 1 || setup.installRequest == nil {
		t.Fatal("Agent access Install selected did not call the agent-only install operation")
	}
	if setup.installRequest.SetupRequest.Ref.Name != key.Target || setup.installRequest.Inputs["repo"] != "/repos/updated" {
		t.Fatalf("install did not submit the exact target and saved inputs: %+v", setup.installRequest)
	}

	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.form == nil {
		cmd = m.openTargetWorkspace(m.packInstanceRequest(key), "Overview")
		m.Update(cmd())
	}
	m.form.FocusSection()
	if !m.form.SelectSectionID(sectionRuntimeID) {
		t.Fatal("Runtime section is unavailable")
	}
	m.form.FocusSection()
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.form.SectionID() != sectionRuntimeID {
		t.Fatalf("navigation did not reach Runtime: %q", m.form.SectionID())
	}
	runtime := ansi.Strip(m.form.View().Content)
	if !strings.Contains(runtime, "Start inspector") || strings.Contains(runtime, "[ Save ]") || strings.Contains(runtime, "[ Cancel ]") || strings.Contains(runtime, "Save and apply") {
		t.Fatalf("Runtime must expose Start without global input actions:\n%s", runtime)
	}
}

func TestUXAgentInstallCanBeRepeatedAfterSuccessfulResult(t *testing.T) {
	setup := &workspaceApplyBackend{withMCP: true}
	m := NewContext(t.Context(), setup)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 32})
	key := state.Key{Source: "team-source", Package: "plain", Environment: "dev", Target: "prod"}

	install := func(dismiss bool) {
		t.Helper()
		if m.form == nil {
			cmd := m.openTargetWorkspace(m.packInstanceRequest(key), "Overview")
			if cmd == nil {
				t.Fatal("workspace could not be reopened after the previous successful result")
			}
			m.Update(cmd())
		}
		m.form.FocusSection()
		if !m.form.SelectSectionID(sectionAgentsID) {
			t.Fatal("Agent access section is unavailable")
		}
		m.form.FocusSection()
		m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		_, cmd := m.Update(forms.ActionMsg{SectionID: sectionAgentsID, ID: "install-selected"})
		if cmd == nil {
			t.Fatal("Install selected did not submit UIInstall")
		}
		m.Update(runTeaCmd(t, m, cmd))
		if m.result == nil || m.result.Failed {
			t.Fatalf("successful explicit install did not show its result: %+v", m.result)
		}
		if dismiss {
			m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		}
	}

	install(true)
	if setup.installCalls != 1 || setup.installRequest == nil {
		t.Fatal("first Install selected did not call UIInstall")
	}
	install(false)
	if setup.installCalls != 2 || setup.installRequest == nil || m.busy || m.result == nil {
		t.Fatalf("second explicit Install selected did not run through progress and result: request=%+v busy=%t result=%+v", setup.installRequest, m.busy, m.result)
	}
}

func TestUXSkillOnlyTargetDoesNotOfferMCPRuntimeActions(t *testing.T) {
	setup := &workspaceApplyBackend{}
	m := NewContext(t.Context(), setup)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 32})
	key := state.Key{Source: "team-source", Package: "skill-only", Environment: "dev", Target: "default"}
	cmd := m.openTargetWorkspace(m.packInstanceRequest(key), "Overview")
	m.Update(cmd())
	m.form.FocusSection()
	m.Update(tea.KeyPressMsg{Code: tea.KeyUp})

	inputs := ansi.Strip(m.form.View().Content)
	if !strings.Contains(inputs, "[ Save ]") || !strings.Contains(inputs, "[ Cancel ]") || strings.Contains(inputs, "Save and apply") {
		t.Fatalf("skill-only Inputs must expose Save/Cancel without a combined apply action:\n%s", inputs)
	}
	m.form.FocusSection()
	if !m.form.SelectSectionID(sectionAgentsID) {
		t.Fatal("skill-only Agent access section is unavailable")
	}
	m.form.FocusSection()
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.form.SectionID() != sectionAgentsID {
		t.Fatalf("skill-only capability lost Agent access: %q", m.form.SectionID())
	}
	agents := ansi.Strip(m.form.View().Content)
	for _, forbidden := range []string{"MCP runtime", "Start ", "Stop ", "Save and apply", "[ Save ]", "[ Cancel ]"} {
		if strings.Contains(agents, forbidden) {
			t.Fatalf("skill-only Agent access exposed runtime/global action %q:\n%s", forbidden, agents)
		}
	}
	if !strings.Contains(agents, "[ Install selected ]") || !strings.Contains(agents, "[ Uninstall selected ]") {
		t.Fatalf("skill-only capability lost targeted install/uninstall actions:\n%s", agents)
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
