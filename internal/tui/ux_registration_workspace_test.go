package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
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
	setupSection(m, 4) // Overview → Agents → right-hand detail.
	if m.form.SectionTitle() != "Agents" || !strings.Contains(m.View().Content, "/home/test/.claude") {
		t.Fatalf("foreign runtime blocked named local agent destinations in Agents: section=%q\n%s", m.form.SectionTitle(), m.View().Content)
	}
	press(m, tea.KeyRight, "")
	press(m, tea.KeySpace, " ")
	if strings.Contains(m.View().Content, "Start") || strings.Contains(m.View().Content, "Stop") {
		t.Fatalf("workspace offered local runtime control for a foreign runtime:\n%s", m.View().Content)
	}
	_, save := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if save == nil {
		t.Fatalf("Agents Save did not invoke the registration install path: %s", m.View().Content)
	}
	m.Update(save())
	if backend.installRequest == nil || backend.installRequest.SetupRequest.Target != "foreign" || !containsString(backend.installRequest.DestinationIDs, "codex") {
		t.Fatalf("Agents Save did not register named destinations on the selected foreign target: %+v", backend.installRequest)
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

func openRegistrationActionForUX(t *testing.T, action ...string) (*Model, *profileBackend) {
	t.Helper()
	m, backend := typedProfileFixture()
	m.backend = &registrationWorkspaceBackend{Backend: backend, profileBackend: backend, setupBackendFixture: &setupBackendFixture{}}
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
		t.Fatalf("profile Enter did not open the shared workspace: %s", m.View().Content)
	}
	m.Update(cmd())
	stroke := "g"
	if len(action) > 0 && action[0] == "remove" {
		stroke = "r"
	}
	_, _ = m.Update(tea.KeyPressMsg{Code: rune(stroke[0]), Text: stroke})
	if m.registration == nil {
		t.Fatalf("workspace %s action did not open registration editor: %s", stroke, m.output)
	}
	return m, backend
}

func (b *registrationWorkspaceBackend) UISetupPreview(_ context.Context, request viewmodel.SetupRequest) (viewmodel.SetupPreview, error) {
	b.previewRequest = request
	return viewmodel.SetupPreview{
		Key:         state.Key{Source: request.SourceID, Package: request.PackageID, Environment: request.Environment, Target: request.Target},
		PackageName: "Plain",
		Destinations: []viewmodel.SetupDestination{
			{ID: "codex", Path: "/home/test/.codex/skills", ConfigPath: "/home/test/.codex/config.toml", Detection: "installed"},
			{ID: "claude", Path: "/home/test/.claude/skills", ConfigPath: "/home/test/.claude.json", Detection: "installed"},
		},
	}, nil
}

func (b *registrationWorkspaceBackend) UIInstall(ctx context.Context, request viewmodel.SetupInstallRequest) (viewmodel.OperationResult, error) {
	return b.setupBackendFixture.UIInstall(ctx, request)
}

func TestUXRegistrationEndpointAndAgentDetailsAreIndependent(t *testing.T) {
	m, _ := openRegistrationActionForUX(t)
	m.agentManagement = []viewmodel.AgentManagementRow{{
		ID: "codex", Name: "Codex", Detection: "installed", Home: "/tmp/codex-home",
		EffectiveConfigPath: "/tmp/codex-home/.codex/config.toml",
		WriteConfigPath:     "/tmp/codex-home/.codex/config.toml",
	}}
	endpoint := m.View().Content
	for _, want := range []string{"Endpoint URI", "http://127.0.0.1:8765/mcp", "Check connection"} {
		if !strings.Contains(endpoint, want) {
			t.Fatalf("endpoint pane lacks %q:\n%s", want, endpoint)
		}
	}
	press(m, tea.KeyDown, "")
	detail := m.View().Content
	for _, want := range []string{"Agent detection: installed", "Config file to change: /tmp/codex-home/.codex/config.toml", "Desired registration", "Achieved registration: not registered", "Agent home: /tmp/codex-home"} {
		if !strings.Contains(strings.ToLower(detail), strings.ToLower(want)) {
			t.Fatalf("selected agent detail lacks %q:\n%s", want, detail)
		}
	}
	if strings.Contains(detail, "Check connection") {
		t.Fatalf("agent detail contains endpoint check control:\n%s", detail)
	}
}

func TestUXRegistrationOptionsExcludeGenericAndAll(t *testing.T) {
	m, _ := openRegistrationActionForUX(t)
	m.agents = []string{"codex", "all", "generic", "generic:work", "generic-mcp:work"}
	profile := m.profileSnapshot.Profiles[1]
	m.openRegistrationOverlay(ProfileRow{Key: profile.Key, URL: profile.URL, Profile: &profile}, false)
	if got := strings.Join(m.registration.Agents, ","); got != "codex,claude" {
		t.Fatalf("registration choices are not named agents only: %s", got)
	}
}

func TestUXRemoveIsNarrowAndDoesNotOfferEndpointCheck(t *testing.T) {
	m, _ := openRegistrationActionForUX(t, "remove")
	view := m.View().Content
	for _, forbidden := range []string{"Endpoint URI", "Check connection", "Apply changes"} {
		if strings.Contains(view, forbidden) {
			t.Fatalf("removal dialog still contains unrelated control %q:\n%s", forbidden, view)
		}
	}
	for _, want := range []string{"Remove local registrations", "Claude", "Remove selected registrations", "Cancel"} {
		if !strings.Contains(view, want) {
			t.Fatalf("removal dialog lacks %q:\n%s", want, view)
		}
	}
}

func TestUXRemoveShowsLegacyRecordedMCPAndSendsExplicitIDs(t *testing.T) {
	m, backend := typedProfileFixture()
	profile := backend.snapshot.Profiles[1]
	profile.RegisteredAgents = append(profile.RegisteredAgents, "generic:legacy")
	m.openRegistrationOverlay(ProfileRow{Key: profile.Key, URL: profile.URL, Profile: &profile}, true)
	if !strings.Contains(m.View().Content, "generic:legacy") {
		t.Fatalf("actual legacy MCP registration was hidden from focused removal:\n%s", m.View().Content)
	}
	m.registration.Marked["generic:legacy"] = true
	cmd := m.applyRegistrationOverlay()
	if cmd == nil {
		t.Fatal("selected legacy MCP row did not submit a narrow removal")
	}
	m.Update(cmd())
	if backend.request == nil || len(backend.request.RemoveAgentIDs) != 1 || backend.request.RemoveAgentIDs[0] != "generic:legacy" {
		t.Fatalf("removal did not identify only the selected recorded row: %+v", backend.request)
	}
}

func TestUXRemoveDoesNotStopServer(t *testing.T) {
	m, b := openRegistrationActionForUX(t, "remove")
	m.registration.Marked["claude"] = true
	cmd := m.applyRegistrationOverlay()
	if cmd == nil {
		t.Fatal("removing a selected registration did not invoke the registration service")
	}
	m.Update(cmd())
	if b.request == nil || len(b.request.AgentIDs) != 0 {
		t.Fatalf("removal did not submit the remaining named registrations: %+v", b.request)
	}
}

func TestUXPartialRegistrationFailureShowsErrorAndUnchecksFailedAgent(t *testing.T) {
	m, b := openRegistrationActionForUX(t)
	b.result = viewmodel.OperationResult{
		Message: "Registration update completed with errors",
		Changes: []state.Installation{{AgentID: "codex", Component: "mcp"}},
		Errors:  []string{`claude: refusing foreign MCP registration "plain-dev"`},
	}
	m.registration.Marked["codex"] = true
	m.registration.Marked["claude"] = true
	_, cmd := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("Ctrl-S did not submit selected registrations")
	}
	m.Update(cmd())
	view := m.View().Content
	if !strings.Contains(view, "codex: mcp registration configured") || strings.Contains(view, "claude: mcp registration configured") || !strings.Contains(view, "refusing foreign MCP registration") {
		t.Fatalf("result does not report actual per-agent effects:\n%s", view)
	}
	if len(b.request.AgentIDs) != 2 {
		t.Fatalf("selected draft was not submitted once: %+v", b.request)
	}
}

func TestUXRegistrationButtonsReachableAndOneSave(t *testing.T) {
	m, b := openRegistrationActionForUX(t)
	for i := 0; i < 2; i++ {
		press(m, tea.KeyTab, "")
	}
	view := m.View().Content
	if !strings.Contains(view, "[Cancel]") || !strings.Contains(view, "Save") {
		t.Fatalf("Save/Cancel actions are not visible while action focus is active:\n%s", view)
	}
	press(m, tea.KeyRight, "")
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil {
		m.Update(cmd())
	}
	if b.request == nil {
		t.Fatal("focused Save action did not submit")
	}
	if cmd := m.registrationKey("ctrl+s"); cmd != nil {
		t.Fatal("completed save submitted a second operation")
	}
}
