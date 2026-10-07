package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

// openForeignRegistrationInteraction now opens the complete manifest-backed
// Agents workspace. The selected MCP's endpoint remains a separate Runtime
// observation, while agent bindings are saved with the capability.
func openForeignRegistrationInteraction(t *testing.T) (*Model, *profileBackend) {
	t.Helper()
	m, backend := typedProfileFixture()
	m.focusPane(ProfilesPane)
	m.selectPane(ProfilesPane, 1)
	openCapabilityProfileWorkspace(t, m, backend, "foreign", "Agents")
	return m, backend
}

func TestInteractionCapabilityEndpointAndAgentsAreSeparateSections(t *testing.T) {
	m, _ := openForeignRegistrationInteraction(t)
	m.form.SelectSection("Endpoint")
	view := m.View().Content
	for _, want := range []string{"Endpoint URI", "http://127.0.0.1:8765/mcp"} {
		if !strings.Contains(view, want) {
			t.Fatalf("declared endpoint fields missing %q:\n%s", want, view)
		}
	}
	m.form.SelectSectionID(sectionAgentsID)
	m.form.FocusSection()
	view = m.View().Content
	for _, want := range []string{"Agents", "Codex", "Claude Code"} {
		if !strings.Contains(view, want) {
			t.Fatalf("complete binding section missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "Check plain connection") {
		t.Fatal("runtime diagnostics leaked into the agent binding section")
	}
}

func TestInteractionCapabilityBackRestoresSelectedProfile(t *testing.T) {
	m, b := openForeignRegistrationInteraction(t)
	press(m, tea.KeyEscape, "")
	_, back := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if back != nil {
		m.Update(back())
	}
	if m.workspace == nil || m.workspace.Active || m.form != nil || b.request != nil {
		t.Fatalf("Back did not restore the originating profile without applying changes:\n%s", m.View().Content)
	}
	rows := m.profiles()
	if m.home.Profiles.Index != 1 || len(rows) < 2 || rows[1].Key.Target != "foreign" {
		t.Fatalf("Back lost the exact parent profile selection: index=%d rows=%+v", m.home.Profiles.Index, rows)
	}
}

func TestInteractionCapabilityEscapeReturnsFromDetailsToSections(t *testing.T) {
	m, _ := openForeignRegistrationInteraction(t)
	m.form.SelectSectionID(sectionAgentsID)
	m.form.FocusSection()
	press(m, tea.KeyRight, "")
	if m.form.FocusArea() != 1 {
		t.Fatal("right pane did not receive focus")
	}
	press(m, tea.KeyEscape, "")
	if m.form.FocusArea() != 0 || m.workspace == nil || !m.workspace.Active {
		t.Fatal("Escape from details did not return to the workspace section list")
	}
}

func TestInteractionCapabilityAgentListShowsScrollCues(t *testing.T) {
	m, b := typedProfileFixture()
	backend := openCapabilityProfileWorkspace(t, m, b, "foreign", "Agents")
	backend.preview.Key = m.workspace.Key
	for i := 0; i < 24; i++ {
		backend.preview.Destinations = append(backend.preview.Destinations, viewmodel.SetupDestination{ID: "agent-" + string(rune('a'+i)), ConfigPath: "/home/test/agent.json"})
	}
	// Reload so the expanded manifest destination list is rendered by the form.
	m.openSetupFormWithValues(backend.preview, nil)
	view := m.View().Content
	if !strings.Contains(strings.ToLower(view), "↓ more") {
		t.Fatalf("long destination list lacks a downward scroll cue:\n%s", view)
	}
	m.form.SelectSectionID(sectionAgentsID)
	m.form.FocusSection()
	press(m, tea.KeyRight, "")
	for i := 0; i < 24; i++ {
		press(m, tea.KeyDown, "")
	}
	if view = m.View().Content; !strings.Contains(strings.ToLower(view), "↑ more") {
		t.Fatalf("scrolled destination list lacks an upward cue:\n%s", view)
	}
}

func TestInteractionCapabilityMouseWheelScrollsDestinationDetail(t *testing.T) {
	m, b := typedProfileFixture()
	backend := openCapabilityProfileWorkspace(t, m, b, "foreign", "Agents")
	backend.preview.Key = m.workspace.Key
	for i := 0; i < 24; i++ {
		backend.preview.Destinations = append(backend.preview.Destinations, viewmodel.SetupDestination{ID: "agent-" + string(rune('a'+i)), ConfigPath: "/home/test/agent.json"})
	}
	m.openSetupFormWithValues(backend.preview, nil)
	m.form.SelectSectionID(sectionAgentsID)
	m.form.FocusSection()
	m.View()
	x, y, width, height, ok := m.setupOverlayBounds()
	if !ok {
		t.Fatal("setup workspace has no mouse hit region")
	}
	m.Update(tea.MouseWheelMsg{X: x + width - 4, Y: y + height/2, Button: tea.MouseWheelDown})
	if view := m.View().Content; !strings.Contains(strings.ToLower(view), "↑ more") {
		t.Fatalf("mouse wheel did not scroll the destination details:\n%s", view)
	}
}

func TestInteractionCapabilityKeyboardSaveAppliesCompleteDesiredSet(t *testing.T) {
	m, _ := openForeignRegistrationInteraction(t)
	backend := m.backend.(*capabilityProfileBackend)
	m.form.SelectSectionID(sectionAgentsID)
	m.form.FocusSection()
	press(m, tea.KeyRight, "")
	press(m, tea.KeySpace, " ") // Add Codex while preserving Claude.
	if got := m.form.Values()[m.pendingSetupField]; !containsStringFromValue(got, "codex") {
		t.Fatalf("keyboard did not add Codex to the desired set: %#v", got)
	}
	press(m, tea.KeyTab, "") // Keyboard focus reaches the visible Save action.
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("Enter on the focused Save action did not submit the complete binding:\n%s", m.View().Content)
	}
	m.Update(runTeaCmd(t, m, cmd))
	if backend.setup.installRequest == nil || !containsString(backend.setup.installRequest.DestinationIDs, "codex") || !containsString(backend.setup.installRequest.DestinationIDs, "claude") {
		t.Fatalf("complete capability save lost selected destinations: %+v", backend.setup.installRequest)
	}
}

func TestInteractionRuntimeConnectionCheckIsIndependentOfAgentSelection(t *testing.T) {
	m, b := openForeignRegistrationInteraction(t)
	m.form.SelectSectionID(sectionAgentsID)
	m.form.FocusSection()
	press(m, tea.KeyRight, "")
	press(m, tea.KeySpace, " ") // This only changes the unsaved destination draft.
	m.form.SelectSectionID(sectionRuntimeID)
	m.form.FocusSection()
	press(m, tea.KeyRight, "")
	if m.form.FocusArea() != 1 || !strings.Contains(m.View().Content, "Check plain connection") {
		t.Fatalf("Runtime child diagnostic is not isolated in its section:\n%s", m.View().Content)
	}
	press(m, tea.KeyDown, "")
	press(m, tea.KeyDown, "")
	_, action := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if action != nil {
		_, check := m.Update(action())
		if check != nil {
			m.Update(runTeaCmd(t, m, check))
		}
	}
	if b.checkedURL != "http://127.0.0.1:8765/mcp" || b.request != nil {
		t.Fatalf("connection check touched capability bindings: url=%q legacy request=%+v", b.checkedURL, b.request)
	}
	if m.result == nil || !strings.Contains(strings.Join(m.result.Rows, "\n"), "reachable") {
		t.Fatalf("connection result was not shown as a foreground observation: %+v", m.result)
	}
}

func TestInteractionCapabilityActionsRemainKeyboardReachable(t *testing.T) {
	m, _ := openForeignRegistrationInteraction(t)
	press(m, tea.KeyEscape, "") // Return from details to the section list.
	_, back := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if back != nil {
		m.Update(back())
	}
	if m.workspace == nil || m.workspace.Active || m.form != nil {
		t.Fatal("Escape did not return from the capability workspace")
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
	cmd := m.openTargetWorkspace(viewmodel.SetupRequest{SourceID: profile.Key.Source, PackageID: profile.Key.Package, Environment: profile.Key.Environment, Target: profile.Key.Target}, "Information")
	m.Update(cmd())
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

func TestInteractionUnknownOwnerOffersDiagnosisAndBindingButNotLifecycleControl(t *testing.T) {
	m, b := typedProfileFixture()
	b.snapshot.Profiles[1].Ownership = "unknown"
	m.Update(m.load()())
	key := b.snapshot.Profiles[1].Key
	openCapabilityProfileWorkspace(t, m, b, key.Target, "Overview")
	if m.workspace == nil || m.workspace.Profile == nil || m.workspace.Profile.Ownership != "unknown" {
		t.Fatalf("Overview lost the unknown ownership observation: %+v", m.workspace)
	}
	row := ProfileRow{Key: key, URL: b.snapshot.Profiles[1].URL, Profile: &b.snapshot.Profiles[1], Status: "foreign"}
	for _, action := range []string{"check-connection"} {
		if reason := m.profileActionReason(row, action); reason != "" {
			t.Fatalf("unknown owner blocked local diagnosis %q: %s", action, reason)
		}
	}
	startReason := m.profileActionReason(row, "s")
	if startReason == "" {
		t.Fatal("unknown owner unexpectedly permits Start")
	}
	if handled, _ := m.workspaceOverviewAction("g"); !handled || m.form == nil || !m.form.HasSectionID(sectionAgentsID) {
		t.Fatalf("unknown-owner Overview did not route to complete capability Agents configuration: handled=%t", handled)
	}
	m.form.SelectSectionID(sectionOverviewID)
	if !strings.Contains(m.form.View().Content, "Save and apply") {
		t.Fatal("unknown-owner workspace did not retain the Save and apply capability action")
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
