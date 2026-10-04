package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// The route is deliberately driven by keys: a profile belongs to a capability's
// layer-2 list, and its actions are one layer deeper.
func openForeignRegistrationInteraction(t *testing.T) (*Model, *profileBackend) {
	t.Helper()
	m, b := typedProfileFixture()
	press(m, tea.KeyRight, "")
	for i := 0; i < 3; i++ { // details, saved profile, foreign profile
		press(m, tea.KeyDown, "")
	}
	press(m, tea.KeyEnter, "")
	if !strings.Contains(m.View().Content, "Configure agent registrations") {
		t.Fatalf("profile Enter did not open its action layer:\n%s", m.View().Content)
	}
	if item := m.homeMenuItems()[m.home.Modal.Selected]; item.Action != "registrations" {
		t.Fatalf("first enabled action should configure registrations, got %q", item.Label)
	}
	press(m, tea.KeyEnter, "")
	return m, b
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
	if !strings.Contains(view, "Configure agent registrations") || !strings.Contains(view, "View details") {
		t.Fatalf("Esc did not restore the selected profile action layer:\n%s", view)
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
	if m.registration != nil || m.home.Modal == nil || m.home.Modal.Kind != "actions" {
		t.Fatal("second Esc did not restore the parent action menu")
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
	if m.registration != nil || b.request != nil || m.home.Modal == nil || m.home.Modal.Kind != "actions" {
		t.Fatal("Tab/Enter did not activate Cancel and restore profile actions")
	}
}

func TestInteractionProfileDetailsBackRestoresActions(t *testing.T) {
	m, _ := typedProfileFixture()
	press(m, tea.KeyRight, "")
	for i := 0; i < 3; i++ {
		press(m, tea.KeyDown, "")
	}
	press(m, tea.KeyEnter, "")
	press(m, tea.KeyEnd, "") // Back.
	press(m, tea.KeyUp, "")  // View details.
	press(m, tea.KeyEnter, "")
	if m.home.Modal == nil || m.home.Modal.Kind != "details" {
		t.Fatalf("profile details did not open:\n%s", m.View().Content)
	}
	press(m, tea.KeyEscape, "")
	if m.home.Modal == nil || m.home.Modal.Kind != "actions" {
		t.Fatalf("Esc from profile details skipped its parent action layer:\n%s", m.View().Content)
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
	press(m, tea.KeyRight, "")
	for i := 0; i < 3; i++ {
		press(m, tea.KeyDown, "")
	}
	press(m, tea.KeyEnter, "")
	items := m.homeMenuItems()
	refresh := -1
	for i, item := range items {
		if item.Action == "refresh" {
			refresh = i
		}
		if item.Action == "registrations" || item.Action == "check-connection" {
			if item.Reason != "" {
				t.Fatalf("unknown runtime owner blocked local diagnosis %q: %s", item.Action, item.Reason)
			}
		}
	}
	if refresh < 0 {
		t.Fatalf("unknown-owner actions offer no Refresh observation: %#v", items)
	}
	for m.home.Modal.Selected < refresh {
		press(m, tea.KeyDown, "")
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Refresh observation action did not request a new snapshot")
	}
	m.Update(cmd())
	if m.home.Modal != nil {
		t.Fatal("Refresh left a stale profile action menu covering the observation")
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
