package tui

import (
	"encoding/json"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
)

// This exercises the actual removal overlay and submitted backend request.
// JSON inspection keeps the RED test behavioral while the additive request
// identity type is still awaiting implementation.
func TestUXExactRegistrationRemovalRequestIsPathScoped(t *testing.T) {
	m, backend := typedProfileFixture()
	key := state.Key{Source: "team-source", Package: "plain", Environment: "dev", Target: "foreign"}
	pathA := "/tmp/aact-home-a/.claude.json"
	pathB := "/tmp/aact-home-b/.claude.json"
	m.inventory = []state.Installation{
		{Key: key, AgentID: "claude", AgentHome: "/tmp/aact-home-a", AgentKind: "claude", Component: "mcp", Destination: pathA},
		{Key: key, AgentID: "claude", AgentHome: "/tmp/aact-home-b", AgentKind: "claude", Component: "mcp", Destination: pathB},
	}
	profile := backend.snapshot.Profiles[1]
	profile.RegisteredAgents = []string{"claude", "claude"}
	m.openRegistrationOverlay(ProfileRow{Key: key, URL: profile.URL, Profile: &profile}, true)
	if m.registration == nil {
		t.Fatal("removal overlay did not open")
	}
	if len(m.registration.Choices) != 2 {
		t.Errorf("two actual config registrations collapsed into agent-level choice: state=%+v", m.registration)
	}
	press(m, tea.KeyRight, "") // Inspect the first actual registration.
	if view := m.View().Content; !strings.Contains(view, pathA) {
		t.Errorf("first removal choice does not show its exact config path %s:\n%s", pathA, view)
	}
	press(m, tea.KeyLeft, "")
	press(m, tea.KeyDown, "")
	press(m, tea.KeyRight, "") // Inspect the second actual registration.
	if view := m.View().Content; !strings.Contains(view, pathB) {
		t.Errorf("second removal choice does not show its exact config path %s:\n%s", pathB, view)
	}
	press(m, tea.KeySpace, " ")
	cmd := m.applyRegistrationOverlay()
	if cmd == nil {
		t.Fatal("selecting one path-specific registration did not submit removal")
	}
	m.Update(cmd())
	if backend.request == nil {
		t.Fatal("removal overlay did not submit a request")
	}
	body, err := json.Marshal(backend.request)
	if err != nil {
		t.Fatal(err)
	}
	var request map[string]any
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatal(err)
	}
	identities, ok := request["RemoveRegistrations"].([]any)
	if !ok || len(identities) != 1 {
		t.Fatalf("UI request did not identify exactly one recorded registration: %s", body)
	}
	identity, ok := identities[0].(map[string]any)
	if !ok || identity["AgentID"] != "claude" || identity["Destination"] != pathB {
		t.Fatalf("UI request selected the wrong actual registration: %s", body)
	}
}
