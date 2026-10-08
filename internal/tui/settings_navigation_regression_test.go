package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestSettingsDetailFactsAreNotSelectable(t *testing.T) {
	m := fixtureModel(t)
	m.navigate("Settings")
	m.management.Focus = ProfilesPane
	m.management.SettingsDetailIndex = 0

	view := m.View().Content
	for _, hit := range m.management.Hits {
		if hit.Control == "settings-detail" {
			t.Fatalf("read-only detail line is exposed as a selectable row: %+v\n%s", hit, ansi.Strip(view))
		}
	}

	_, right := managementPaneWidths(m.width)
	details := wrapManagementDetails(m.managementSettingsDetails(), right-1)
	actionIndex, hasAction := m.settingsActionIndex(details)
	if !hasAction {
		t.Fatal("environment settings fixture must expose its edit action")
	}
	press(m, tea.KeyDown, "")
	if m.management.SettingsDetailIndex != actionIndex {
		t.Fatalf("Down selected an informational line (%d), want the only action at %d", m.management.SettingsDetailIndex, actionIndex)
	}

	view = ansi.Strip(m.View().Content)
	if strings.Contains(view, "> Source:") || strings.Contains(view, "> Pack:") {
		t.Fatalf("read-only facts have a selection marker:\n%s", view)
	}
}

func TestSettingsClickOnReadOnlyDetailDoesNotFocusIt(t *testing.T) {
	m := fixtureModel(t)
	m.navigate("Settings")
	m.management.Focus = CapabilitiesPane
	view := m.View().Content
	left, _ := managementPaneWidths(m.width)
	oldIndex := m.management.SettingsDetailIndex
	m.Update(tea.MouseClickMsg{X: left + 4, Y: 5, Button: tea.MouseLeft})
	if m.management.Focus != CapabilitiesPane || m.management.SettingsDetailIndex != oldIndex {
		t.Fatalf("clicking read-only detail changed focus or selection:\n%s", ansi.Strip(view))
	}
}

func TestCapabilityPackScopeUsesConsistentNames(t *testing.T) {
	m := fixtureModel(t)
	m.settings["source"] = "sample-capability-pack"
	m.settings["checkout"] = "capability-packs/sample"
	m.settings["environment_root"] = "capability-packs/sample/environments"
	view := ansi.Strip(m.View().Content)
	for _, forbidden := range []string{"Source:", "Checkout:", "Env:"} {
		if strings.Contains(view, forbidden) {
			t.Fatalf("ambiguous scope label %q remains:\n%s", forbidden, view)
		}
	}
	if !strings.Contains(view, "Capability Pack:") || !strings.Contains(view, "Environment directory:") {
		t.Fatalf("scope does not name the pack and environment directory:\n%s", view)
	}
	m.selected = 0
	details := strings.Join(m.managementSettingsDetails(), "\n")
	for _, expected := range []string{"Capability Pack ID:", "Capability Pack directory:", "Catalog TOML:", "Environment directory:", "<Capability Pack>/environments", "external directory"} {
		if !strings.Contains(details, expected) {
			t.Errorf("Capability Pack settings omit %q:\n%s", expected, details)
		}
	}
}

func TestOtherCapabilityPacksExplainsWhyLocationsAreRemembered(t *testing.T) {
	m := fixtureModel(t)
	m.selected = 3
	details := strings.Join(m.managementSettingsDetails(), "\n")
	for _, expected := range []string{"Other Capability Packs", "AACT remembers", "installations from another pack remain manageable"} {
		if !strings.Contains(details, expected) {
			t.Errorf("Other Capability Packs purpose is unclear; missing %q:\n%s", expected, details)
		}
	}
	if strings.Contains(strings.ToLower(details), "checkout") || strings.Contains(details, "listing is unavailable") {
		t.Fatalf("Diagnostics still uses checkout jargon or implies a non-existent listing:\n%s", details)
	}
}
