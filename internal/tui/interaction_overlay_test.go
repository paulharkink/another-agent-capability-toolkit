package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

// The route is deliberately driven by keys: the exact profile opens its target
// workspace, then the visible registration action opens the paired editor.
func openForeignRegistrationInteraction(t *testing.T) (*Model, *profileBackend) {
	t.Helper()
	return openRegistrationActionForUX(t)
}

func TestInteractionRegistrationEndpointIsOwnLayerThreeItem(t *testing.T) {
	m, _ := openForeignRegistrationInteraction(t)
	view := m.View().Content
	if strings.Contains(strings.ToLower(view), "simulation") {
		t.Fatalf("real registration overlay is mislabeled as a simulation:\n%s", view)
	}
	for _, want := range []string{"Endpoint URI", "http://127.0.0.1:8765/mcp", "Check connection", "Codex", "Claude"} {
		if !strings.Contains(strings.ToLower(view), strings.ToLower(want)) {
			t.Fatalf("registration overlay missing %q:\n%s", want, view)
		}
	}
	press(m, tea.KeyDown, "") // Codex row, with only Codex detail on the right.
	view = m.View().Content
	if !strings.Contains(view, "Codex") || !strings.Contains(view, "Config file") {
		t.Fatalf("selecting Codex did not show its agent detail:\n%s", view)
	}
	if strings.Contains(view, "Check connection") {
		t.Fatalf("endpoint check leaked into the selected agent's detail:\n%s", view)
	}
}

func TestInteractionRegistrationBackRestoresProfileActions(t *testing.T) {
	m, b := openForeignRegistrationInteraction(t)
	press(m, tea.KeyEscape, "")
	view := m.View().Content
	if m.registration != nil || m.workspace == nil || !m.workspace.Active || !strings.Contains(view, "Manage agent registrations") {
		t.Fatalf("Esc did not restore the originating target workspace:\n%s", view)
	}
	if b.request != nil {
		t.Fatal("Back applied a registration change")
	}
}

func TestInteractionRegistrationEscapeWalksRightThenLeft(t *testing.T) {
	m, _ := openForeignRegistrationInteraction(t)
	press(m, tea.KeyDown, "")
	press(m, tea.KeyRight, "")
	if m.registration == nil || m.registration.Area != 1 || m.registration.Row != 1 {
		t.Fatal("agent detail did not receive focus")
	}
	press(m, tea.KeyEscape, "")
	if m.registration == nil || m.registration.Area != 0 || m.registration.Row != 1 {
		t.Fatal("Esc from detail did not return to its selected agent row")
	}
	press(m, tea.KeyEscape, "")
	if m.registration != nil || m.workspace == nil || !m.workspace.Active {
		t.Fatal("second Esc did not restore the target workspace")
	}
}

func TestInteractionRegistrationLongListShowsScrollCues(t *testing.T) {
	m, _ := openForeignRegistrationInteraction(t)
	for i := 0; i < 24; i++ {
		m.registration.Agents = append(m.registration.Agents, "adapter")
	}
	if view := m.View().Content; !strings.Contains(view, "↓ More below") {
		t.Fatalf("hidden registration rows lack a downward cue:\n%s", view)
	}
	for i := 0; i < 24; i++ {
		press(m, tea.KeyDown, "")
	}
	if view := m.View().Content; !strings.Contains(view, "↑ More above") {
		t.Fatalf("scrolled registration rows lack an upward cue:\n%s", view)
	}
}

func TestInteractionRegistrationMouseWheelBrowsesOverflow(t *testing.T) {
	m, _ := openForeignRegistrationInteraction(t)
	for i := 0; i < 24; i++ {
		m.registration.Agents = append(m.registration.Agents, "adapter")
	}
	m.View() // establish the rendered overlay hit area
	x, y := m.registration.X+4, m.registration.Y+5
	m.Update(tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelDown})
	if m.registration.Offset == 0 {
		t.Fatal("mouse wheel over the registration list did not reveal lower agents")
	}
	if view := m.View().Content; !strings.Contains(view, "↑ More above") {
		t.Fatalf("wheel scrolling did not expose the upper continuation cue:\n%s", view)
	}
}

func TestInteractionRegistrationMouseOnlyActivatesVisibleDetailControls(t *testing.T) {
	m, b := openForeignRegistrationInteraction(t)
	m.View()
	r := m.registration
	blankX := r.X + r.W - 2
	_, cmd := m.Update(tea.MouseClickMsg{X: blankX, Y: r.Y + 6, Button: tea.MouseLeft})
	if cmd != nil || b.checkedURL != "" {
		t.Fatal("clicking empty space in the endpoint detail started a connection check")
	}
	press(m, tea.KeyLeft, "")
	press(m, tea.KeyDown, "") // Codex
	m.View()
	r = m.registration
	m.Update(tea.MouseClickMsg{X: blankX, Y: r.Y + 7, Button: tea.MouseLeft})
	if r.Marked["codex"] {
		t.Fatal("clicking empty space in the agent detail changed registration")
	}
	controlX := r.X + r.LeftW + 4
	m.Update(tea.MouseClickMsg{X: controlX, Y: r.Y + 7, Button: tea.MouseLeft})
	if !r.Marked["codex"] {
		t.Fatal("clicking the visible agent registration control did not toggle it")
	}
}

func TestInteractionEndpointCheckIsIndependentOfAgentSelection(t *testing.T) {
	m, b := openForeignRegistrationInteraction(t)
	press(m, tea.KeyRight, "") // Endpoint detail.
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter on Endpoint URI detail did not start a connection check")
	}
	m.Update(cmd())
	if b.checkedURL != "http://127.0.0.1:8765/mcp" || b.request != nil {
		t.Fatalf("endpoint check touched an agent registration or wrong endpoint: url=%q request=%#v", b.checkedURL, b.request)
	}
	if view := m.View().Content; !strings.Contains(view, "Last check: reachable") {
		t.Fatalf("endpoint result missing from its detail:\n%s", view)
	}
	press(m, tea.KeyLeft, "")
	press(m, tea.KeyDown, "")
	if view := m.View().Content; strings.Contains(view, "Check connection") {
		t.Fatalf("agent detail still displays the endpoint check:\n%s", view)
	}
}

func TestInteractionRegistrationActionsAreKeyboardReachable(t *testing.T) {
	m, b := openForeignRegistrationInteraction(t)
	press(m, tea.KeyTab, "")   // Detail.
	press(m, tea.KeyTab, "")   // Fixed action bar.
	press(m, tea.KeyEnter, "") // Cancel, the first action.
	if m.registration != nil || b.request != nil || m.workspace == nil || !m.workspace.Active {
		t.Fatal("Tab/Enter did not activate Cancel and restore the target workspace")
	}
}

func TestInteractionProfileInformationKeepsLiveAndLocalFactsInWorkspace(t *testing.T) {
	m, _ := typedProfileFixture()
	profile := &m.backend.(*profileBackend).snapshot.Profiles[1]
	profile.RuntimeStatus = "conflict"
	profile.LocalLastAction = "stop"
	profile.LocalLastActionAt = time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)
	profile.ObservedAt = time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC)
	profile.RegistrationDisabledReason = "Multiple runtime endpoints"
	m.Update(m.load()())
	base := m.backend.(*profileBackend)
	m.backend = &registrationWorkspaceBackend{Backend: base, profileBackend: base, setupBackendFixture: &setupBackendFixture{}}
	m.selectPane(ProfilesPane, 1)
	_, open := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if open == nil {
		t.Fatal("selected profile did not open its target workspace")
	}
	m.Update(open())
	m.Update(tea.KeyPressMsg{Code: 'i', Text: "i"})
	view := m.View().Content
	for _, want := range []string{"Endpoint URI: http://127.0.0.1:8765/mcp", "Live runtime status: conflict", "Local last action: stop", "2026-10-02", "Multiple runtime endpoints"} {
		if !strings.Contains(view, want) {
			t.Errorf("workspace Information omitted %q:\n%s", want, view)
		}
	}
	_, back := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if back == nil {
		t.Fatal("Escape from workspace Information did not dispatch parent Back")
	}
	m.Update(back())
	if m.workspace == nil || m.workspace.Active || m.workspace.Key != profile.Key || m.form != nil || m.home.Context.ID == "" {
		t.Fatalf("Back from profile Information did not restore its exact parent selection:\n%s", m.View().Content)
	}
}

func TestInteractionRegistrationKeyboardCanApplySelectedAgent(t *testing.T) {
	m, b := openForeignRegistrationInteraction(t)
	press(m, tea.KeyDown, "")  // Codex
	press(m, tea.KeyRight, "") // Codex's detail controls
	press(m, tea.KeySpace, " ")
	_, cmd := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatalf("Ctrl-S did not submit registration changes:\n%s", m.View().Content)
	}
	m.Update(cmd())
	if b.request == nil || !containsString(b.request.AgentIDs, "codex") || !containsString(b.request.AgentIDs, "claude") {
		t.Fatalf("keyboard selection did not apply immediately: %#v", b.request)
	}
}

func TestInteractionUnknownOwnerStillOffersDiagnosisAndLocalRegistration(t *testing.T) {
	m, b := typedProfileFixture()
	b.snapshot.Profiles[1].Ownership = "unknown"
	m.Update(m.load()())
	setup := &workspaceRouteBackend{}
	m.backend = profileWorkspaceBackend{profileBackend: b, workspaceRouteBackend: setup}
	key := b.snapshot.Profiles[1].Key
	cmd := m.openTargetWorkspace(viewmodel.SetupRequest{SourceID: key.Source, PackageID: key.Package, Environment: key.Environment, Target: key.Target}, "Overview")
	if cmd == nil {
		t.Fatal("unknown-owner target did not load its Overview")
	}
	m.Update(cmd())
	if m.workspace == nil || m.workspace.Profile == nil || m.workspace.Profile.Ownership != "unknown" {
		t.Fatalf("Overview lost the unknown ownership observation: %+v", m.workspace)
	}
	row := ProfileRow{Key: key, URL: b.snapshot.Profiles[1].URL, Profile: &b.snapshot.Profiles[1], Status: "foreign"}
	for _, action := range []string{"registrations", "check-connection"} {
		if reason := m.profileActionReason(row, action); reason != "" {
			t.Fatalf("unknown owner blocked local diagnosis %q: %s", action, reason)
		}
	}
	startReason := m.profileActionReason(row, "s")
	if startReason == "" {
		t.Fatal("unknown owner unexpectedly permits Start")
	}
	if handled, cmd := m.workspaceOverviewAction("s"); !handled || cmd != nil || !strings.Contains(m.output, startReason) {
		t.Fatalf("unknown-owner Start was available or lost its reason: handled=%t cmd=%v output=%q", handled, cmd != nil, m.output)
	}
	if handled, _ := m.workspaceOverviewAction("g"); !handled || m.registration == nil || m.registration.Profile.Key != key {
		t.Fatalf("unknown-owner Overview did not offer local agent registration: handled=%t registration=%+v", handled, m.registration)
	}
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
