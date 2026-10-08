package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

func TestUXForeignEndpointCanRegisterWithoutRuntimeControl(t *testing.T) {
	m, profileBackend := typedProfileFixture()
	backend := &registrationWorkspaceBackend{Backend: profileBackend, profileBackend: profileBackend, setupBackendFixture: &setupBackendFixture{}}
	m.backend = backend
	m.focusPane(ProfilesPane)
	foreign := state.Key{Source: "team-source", Package: "plain", Environment: "dev", Target: "foreign"}
	for index, row := range m.contextRows() {
		if row.Kind == "profile" && row.Key == foreign {
			m.selectContext(index)
			break
		}
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter on the foreign target did not open its workspace")
	}
	m.Update(cmd())
	if m.form == nil || m.workspace == nil || m.workspace.Profile == nil {
		t.Fatalf("foreign target workspace did not load: %s", m.View().Content)
	}
	if m.workspace.Profile.Ownership != "other-aact" || !strings.Contains(m.View().Content, "http://127.0.0.1:8765/mcp") {
		t.Fatalf("foreign endpoint/ownership facts missing from Overview: %s", m.View().Content)
	}
	m.form.SelectSectionID(sectionAgentsID)
	m.form.FocusSection()
	if m.form.SectionTitle() != "Agents" || !strings.Contains(m.View().Content, "/home/test/.claude") {
		t.Fatalf("foreign runtime blocked local destinations in Agents: %s", m.View().Content)
	}
	press(m, tea.KeyRight, " ")
	press(m, tea.KeySpace, " ")
	if strings.Contains(m.View().Content, "Start") || strings.Contains(m.View().Content, "Stop") {
		t.Fatalf("foreign runtime showed local runtime control: %s", m.View().Content)
	}
	_, save := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if save == nil {
		t.Fatalf("Agents Save did not invoke setup install: %s", m.View().Content)
	}
	m.Update(runTeaCmd(t, m, save))
	if backend.installRequest == nil || backend.installRequest.SetupRequest.Target != "foreign" || !containsString(backend.installRequest.DestinationIDs, "codex") {
		t.Fatalf("Agents Save did not bind destinations on selected target: %+v", backend.installRequest)
	}
}

type registrationWorkspaceBackend struct {
	Backend
	profileBackend *profileBackend
	*setupBackendFixture
}

func (b *registrationWorkspaceBackend) UIProfileSnapshot(ctx context.Context) (viewmodel.ProfileSnapshot, error) {
	return b.profileBackend.UIProfileSnapshot(ctx)
}
func (b *registrationWorkspaceBackend) UIConfigureRegistrations(ctx context.Context, request viewmodel.RegistrationRequest) (viewmodel.OperationResult, error) {
	return b.profileBackend.UIConfigureRegistrations(ctx, request)
}
func (b *registrationWorkspaceBackend) CheckConnection(ctx context.Context, url, transport string) viewmodel.ConnectionObservation {
	return b.profileBackend.CheckConnection(ctx, url, transport)
}
func (b *registrationWorkspaceBackend) UISetupPreview(_ context.Context, request viewmodel.SetupRequest) (viewmodel.SetupPreview, error) {
	b.previewRequest = request
	return viewmodel.SetupPreview{Key: state.Key{Source: request.SourceID, Package: request.PackageID, Environment: request.Environment, Target: request.Target}, PackageName: "Plain", MCP: true, HasManifestUI: true,
		Sections: []catalog.Section{{ID: "connection", Title: "Connection", Fields: []string{"endpoint"}}},
		Inputs:   []viewmodel.SetupInput{{Definition: catalog.Input{Name: "endpoint", Label: "Endpoint URI", Type: "string"}, Value: "http://127.0.0.1:8765/mcp", HasValue: true, Editable: true}}, MCPDefinitions: []catalog.MCP{{Name: "plain", Transport: "streamable-http"}},
		Destinations: []viewmodel.SetupDestination{{ID: "codex", Path: "/home/test/.codex/skills", ConfigPath: "/home/test/.codex/config.toml", Detection: "installed"}, {ID: "claude", Path: "/home/test/.claude/skills", ConfigPath: "/home/test/.claude.json", Detection: "installed"}}}, nil
}
func (b *registrationWorkspaceBackend) UIInstall(ctx context.Context, request viewmodel.SetupInstallRequest) (viewmodel.OperationResult, error) {
	return b.setupBackendFixture.UIInstall(ctx, request)
}

func openRegistrationActionForUX(t *testing.T) (*Model, *registrationWorkspaceBackend) {
	t.Helper()
	m, profile := typedProfileFixture()
	backend := &registrationWorkspaceBackend{Backend: profile, profileBackend: profile, setupBackendFixture: &setupBackendFixture{}}
	m.backend = backend
	m.focusPane(ProfilesPane)
	foreign := state.Key{Source: "team-source", Package: "plain", Environment: "dev", Target: "foreign"}
	for i, row := range m.contextRows() {
		if row.Kind == "profile" && row.Key == foreign {
			m.selectContext(i)
			break
		}
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("profile Enter did not open workspace")
	}
	m.Update(cmd())
	m.form.SelectSectionID(sectionAgentsID)
	m.form.FocusSection()
	return m, backend
}

func TestUXRegistrationEndpointAndAgentDetailsAreIndependent(t *testing.T) {
	m, b := openRegistrationActionForUX(t)
	m.agentManagement = []viewmodel.AgentManagementRow{{ID: "codex", Name: "Codex", Detection: "installed", Home: "/tmp/codex-home", EffectiveConfigPath: "/tmp/codex-home/.codex/config.toml", WriteConfigPath: "/tmp/codex-home/.codex/config.toml"}}
	if !strings.Contains(m.View().Content, "Detection: installed") {
		t.Fatalf("agent destination details missing: %s", m.View().Content)
	}
	m.form.SelectSectionID(sectionRuntimeID)
	m.form.FocusSection()
	v := m.View().Content
	for _, want := range []string{"Runtime", "plain", "Check plain connection"} {
		if !strings.Contains(v, want) {
			t.Fatalf("runtime child action lacks %q: %s", want, v)
		}
	}
	if b.profileBackend.checkedURL != "" {
		t.Fatal("opening capability performed endpoint check")
	}
}
func TestUXRegistrationOptionsExcludeGenericAndAll(t *testing.T) {
	m, _ := openRegistrationActionForUX(t)
	v := m.View().Content
	for _, bad := range []string{"generic:legacy", "generic-mcp", "[all]"} {
		if strings.Contains(v, bad) {
			t.Fatalf("named destination UI exposed %q: %s", bad, v)
		}
	}
	if !strings.Contains(v, "Codex") || !strings.Contains(v, "Claude") {
		t.Fatalf("named local destinations missing: %s", v)
	}
}
func TestUXAgentsSectionUsesUnifiedCapabilityBinding(t *testing.T) {
	m, _ := openRegistrationActionForUX(t)
	v := m.View().Content
	if !strings.Contains(v, "Agents") {
		t.Fatalf("complete capability binding form missing: %s", v)
	}
	for _, bad := range []string{"Remove local registrations", "Remove selected registrations"} {
		if strings.Contains(v, bad) {
			t.Fatalf("obsolete overlay remains: %s", v)
		}
	}
}
func TestUXEmptyDesiredSetRemovesRecordedBindings(t *testing.T) {
	m, b := openRegistrationActionForUX(t)
	_, cmd := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("empty desired bindings were not submitted")
	}
	m.Update(runTeaCmd(t, m, cmd))
	if b.installRequest == nil || len(b.installRequest.DestinationIDs) != 0 {
		t.Fatalf("empty complete desired set not submitted: %+v", b.installRequest)
	}
	if b.profileBackend.request != nil {
		t.Fatal("legacy registration API was used")
	}
}
func TestUXAgentsBindingDoesNotStopRuntime(t *testing.T) {
	m, b := openRegistrationActionForUX(t)
	v := m.View().Content
	for _, bad := range []string{"Start MCP", "Stop MCP"} {
		if strings.Contains(v, bad) {
			t.Fatalf("Agents form offers runtime control %q", bad)
		}
	}
	if b.installRequest != nil {
		t.Fatal("opening form altered binding state")
	}
}
func TestUXPartialRegistrationFailureShowsErrorAndUnchecksFailedAgent(t *testing.T) {
	m, b := openRegistrationActionForUX(t)
	b.installResult = &viewmodel.OperationResult{Message: "Binding update completed with errors", Changes: []state.Installation{{AgentID: "codex", Component: "mcp"}}, Errors: []string{`claude: refusing foreign MCP registration "plain-dev"`}}
	press(m, tea.KeyRight, "")
	press(m, tea.KeySpace, " ")
	press(m, tea.KeyDown, "")
	press(m, tea.KeyDown, "")
	press(m, tea.KeySpace, " ")
	_, cmd := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("Ctrl-S did not submit capability")
	}
	m.Update(runTeaCmd(t, m, cmd))
	v := m.View().Content
	if !strings.Contains(v, "codex: mcp configured") || !strings.Contains(v, "refusing foreign MCP registration") {
		t.Fatalf("result omitted effects/error: %s", v)
	}
	if b.installRequest == nil || len(b.installRequest.DestinationIDs) != 2 {
		t.Fatalf("binding draft not submitted once: %+v", b.installRequest)
	}
}
func TestUXRegistrationButtonsReachableAndOneSave(t *testing.T) {
	m, b := openRegistrationActionForUX(t)
	press(m, tea.KeyTab, "")
	press(m, tea.KeyTab, "")
	v := m.View().Content
	if !strings.Contains(v, "[ Save and apply ]") || strings.Contains(v, "[ Cancel ]") {
		t.Fatalf("single Save and apply action not visible: %s", v)
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("Ctrl-S did not submit")
	}
	m.Update(runTeaCmd(t, m, cmd))
	if b.installRequest == nil {
		t.Fatal("save not submitted")
	}
	if m.workspace == nil || !m.workspace.Active {
		t.Fatal("save left the capability workspace")
	}
}
