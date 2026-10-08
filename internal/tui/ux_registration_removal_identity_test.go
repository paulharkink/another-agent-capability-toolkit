package tui

import (
	"encoding/json"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
)

// This exercises the actual removal overlay and submitted backend request.
func TestUXExactRegistrationRemovalRequestIsPathScoped(t *testing.T) {
	m, backend := typedProfileFixture()
	key := state.Key{Source: "team-source", Package: "plain", Target: "foreign"}
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
	if ref, ok := request["Ref"].(map[string]any); !ok || ref["pack_id"] != "team-source" || ref["capability_id"] != "plain" || ref["name"] != "foreign" {
		t.Fatalf("registration removal did not preserve profile reference: %s", body)
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

func TestUXMouseRemovalSelectsExactRegistrationPath(t *testing.T) {
	m, backend := typedProfileFixture()
	key := state.Key{Source: "team-source", Package: "plain", Target: "foreign"}
	pathA := "/tmp/aact-home-a/.claude.json"
	pathB := "/tmp/aact-home-b/.claude.json"
	m.inventory = []state.Installation{
		{Key: key, AgentID: "claude", AgentHome: "/tmp/aact-home-a", AgentKind: "claude", Component: "mcp", Destination: pathA},
		{Key: key, AgentID: "claude", AgentHome: "/tmp/aact-home-b", AgentKind: "claude", Component: "mcp", Destination: pathB},
	}
	profile := backend.snapshot.Profiles[1]
	profile.RegisteredAgents = []string{"claude", "claude"}
	m.openRegistrationOverlay(ProfileRow{Key: key, URL: profile.URL, Profile: &profile}, true)
	view := ansi.Strip(m.View().Content)
	var checkboxY int
	for index, row := range strings.Split(view, "\n") {
		if strings.Contains(row, "Selected removal: [ ] Claude Code") {
			checkboxY = index
			break
		}
	}
	if checkboxY == 0 {
		t.Fatalf("selected-record checkbox is not visible in removal details:\n%s", view)
	}
	choice := m.registration.Choices[0]
	controlX := m.registration.X + m.registration.LeftW + 4
	m.Update(tea.MouseClickMsg{X: controlX, Y: checkboxY, Button: tea.MouseLeft})
	if !m.registration.Marked[choice.key()] {
		t.Fatalf("mouse click did not select exact record %s; marked=%v", choice.Destination, m.registration.Marked)
	}
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "Selected removal: [x] Claude Code") {
		t.Fatalf("mouse click changed the internal mark without updating the visible exact-record checkbox:\n%s", view)
	}
	cmd := m.applyRegistrationOverlay()
	if cmd == nil {
		t.Fatal("mouse-selected exact record did not submit removal")
	}
	m.Update(cmd())
	if backend.request == nil {
		t.Fatal("mouse-selected removal did not reach the backend")
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
		t.Fatalf("mouse request did not select exactly one path identity: %s", body)
	}
	identity, ok := identities[0].(map[string]any)
	if !ok || identity["AgentID"] != "claude" || identity["Destination"] != pathA {
		t.Fatalf("mouse request selected the wrong actual registration: %s", body)
	}
	legacyIDs, _ := request["RemoveAgentIDs"].([]any)
	if len(legacyIDs) != 0 {
		t.Fatalf("mouse request also carried the ambiguous legacy agent ID: %s", body)
	}
}

func TestUXRemovalShowsLongSelectedConfigPathAtMinimumSupportedSize(t *testing.T) {
	m, backend := typedProfileFixture()
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 16})
	key := state.Key{Source: "team-source", Package: "plain", Target: "foreign"}
	longPath := "/tmp/aact-home-" + strings.Repeat("nested-config-directory-", 3) + "settings/.claude.json"
	m.inventory = []state.Installation{{
		Key: key, AgentID: "claude", AgentHome: "/tmp/aact-home", AgentKind: "claude", Component: "mcp", Destination: longPath,
	}}
	profile := backend.snapshot.Profiles[1]
	profile.RegisteredAgents = []string{"claude"}
	m.openRegistrationOverlay(ProfileRow{Key: key, URL: profile.URL, Profile: &profile}, true)
	press(m, tea.KeyRight, "")
	view := ansi.Strip(m.View().Content)
	rightW := m.registration.W - m.registration.LeftW - 3
	chunks := wrapRegistrationPath(longPath, rightW-3)
	if strings.Join(chunks, "") != longPath {
		t.Fatalf("fixture path wrapping does not reconstruct input path: chunks=%q path=%q", chunks, longPath)
	}
	for _, chunk := range chunks {
		if !strings.Contains(view, chunk) {
			t.Fatalf("selected config path is clipped at 80×16; missing wrapped segment %q:\n%s", chunk, view)
		}
	}
}
