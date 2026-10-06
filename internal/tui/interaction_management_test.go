package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

func TestManagementListMarksSelectableRows(t *testing.T) {
	m := fixtureModel(t)
	m.agentManagement = []viewmodel.AgentManagementRow{{ID: "codex", Name: "Codex"}, {ID: "claude", Name: "Claude"}}
	m.navigate("Agents")
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "> Codex (codex)") || !strings.Contains(view, "› Claude (claude)") {
		t.Fatalf("agent list does not distinguish selectable rows:\n%s", view)
	}
}

func TestSettingsCategoryFocusKeepsControlsInRelatedPane(t *testing.T) {
	m := fixtureModel(t)
	m.navigate("Settings")
	m.selected = 0
	press(m, tea.KeyDown, "")
	if m.selected != 1 {
		t.Fatalf("Down did not select the next Settings category: %d", m.selected)
	}
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "> Agent defaults") || !strings.Contains(view, "Default named agents affect future MCP") || !strings.Contains(view, "installations") {
		t.Fatalf("Settings category and related explanation are not distinct:\n%s", view)
	}
	press(m, tea.KeyEnter, "")
	if m.management.Focus != ProfilesPane || !strings.Contains(ansi.Strip(m.View().Content), "Details · Agent defaults") {
		t.Fatalf("Enter did not focus Agent defaults details: focus=%v\n%s", m.management.Focus, m.View().Content)
	}
}

func TestInteractionF9RoutesToAllManagementDestinations(t *testing.T) {
	for _, destination := range []string{"Agents", "Environments", "Settings", "Help"} {
		t.Run(destination, func(t *testing.T) {
			m, _ := homeFixture()
			press(m, tea.KeyF9, "")
			if m.view != "Catalog" || m.home.Modal == nil || m.home.Modal.Kind != "main" {
				t.Fatalf("F9 should open the Main menu over the current screen: view=%q modal=%#v", m.view, m.home.Modal)
			}
			if !strings.Contains(m.View().Content, destination) {
				t.Fatalf("Main menu omitted %q:\n%s", destination, m.View().Content)
			}
			for i := 0; i < managementDestinationIndex(destination); i++ {
				press(m, tea.KeyDown, "")
			}
			press(m, tea.KeyEnter, "")
			if m.view != destination || m.home.Modal != nil {
				t.Fatalf("Main menu Enter did not route to %q: view=%q modal=%#v", destination, m.view, m.home.Modal)
			}
		})
	}
}

func managementDestinationIndex(destination string) int {
	for i, name := range []string{"Agents", "Environments", "Settings", "Help"} {
		if name == destination {
			return i
		}
	}
	return 0
}

func TestInteractionHomeFocusAndPopupBackOutOneLayerAtATime(t *testing.T) {
	m, _ := homeFixture()
	press(m, tea.KeyF9, "")
	for i := 0; i < managementDestinationIndex("Agents"); i++ {
		press(m, tea.KeyDown, "")
	}
	press(m, tea.KeyEnter, "")
	if m.view != "Agents" {
		t.Fatalf("Main menu did not open Agents: %q", m.view)
	}
	press(m, tea.KeyEscape, "")
	if m.view != "Catalog" || m.home.Focus != CapabilitiesPane {
		t.Fatalf("Esc from management should return to home layer 1: view=%q focus=%v", m.view, m.home.Focus)
	}
	press(m, tea.KeyEnter, "")
	if m.home.Focus != ProfilesPane || m.home.Modal != nil {
		t.Fatalf("first Enter should focus home layer 2 only: focus=%v modal=%#v", m.home.Focus, m.home.Modal)
	}
	press(m, tea.KeyDown, "") // View capability details.
	press(m, tea.KeyEnter, "")
	if m.home.Modal == nil || m.home.Modal.Kind != "details" {
		t.Fatalf("Enter from layer 2 should open capability details: %#v", m.home.Modal)
	}
	press(m, tea.KeyEscape, "")
	if m.home.Modal != nil || m.home.Focus != ProfilesPane {
		t.Fatalf("first Esc should close only the popup and restore layer 2: focus=%v modal=%#v", m.home.Focus, m.home.Modal)
	}
	press(m, tea.KeyEscape, "")
	if m.home.Focus != CapabilitiesPane || m.home.Modal != nil {
		t.Fatalf("second Esc should return from layer 2 to layer 1: focus=%v modal=%#v", m.home.Focus, m.home.Modal)
	}
}

func TestInteractionManagementArrowNavigationShowsOverflowCues(t *testing.T) {
	m := fixtureModel(t)
	rows := make([]viewmodel.AgentManagementRow, 0, 30)
	for i := 0; i < 30; i++ {
		rows = append(rows, viewmodel.AgentManagementRow{ID: fmt.Sprintf("agent-%02d", i), Name: fmt.Sprintf("Agent %02d", i), Detection: "detected"})
	}
	m.agentManagement = rows
	m.navigate("Agents")
	view := m.View().Content
	if !strings.Contains(view, "↓ More below") {
		t.Fatalf("Agents list with hidden rows lacks a downward cue:\n%s", view)
	}
	press(m, tea.KeyDown, "")
	if m.selected != 1 || !strings.Contains(m.View().Content, "Agent 01") {
		t.Fatalf("Down did not move the selected layer-1 row: selected=%d\n%s", m.selected, m.View().Content)
	}
	press(m, tea.KeyEnd, "")
	view = m.View().Content
	if m.selected != len(rows)-1 || !strings.Contains(view, "↑ More above") || strings.Contains(view, "↓ More below") {
		t.Fatalf("End did not reach final row with an upward continuation cue: selected=%d\n%s", m.selected, view)
	}
}
