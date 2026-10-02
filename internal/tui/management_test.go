package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

type typedAgentBackend struct {
	fixtureBackend
	rows    []viewmodel.AgentManagementRow
	content string
}

func (b *typedAgentBackend) UIAgentManagement(context.Context) ([]viewmodel.AgentManagementRow, error) {
	return b.rows, nil
}

func (b *typedAgentBackend) UIAgentConfig(_ context.Context, id, path string) (string, error) {
	if id != "codex" || path != "/home/test/config.json" {
		return "", fmt.Errorf("unexpected config request %s %s", id, path)
	}
	return b.content, nil
}

func typedAgentModel(t *testing.T) (*Model, *typedAgentBackend) {
	t.Helper()
	b := &typedAgentBackend{rows: []viewmodel.AgentManagementRow{{ID: "codex", Name: "Codex", Detection: "installed", Evidence: "CLI at /usr/bin/codex", ConfigFiles: []viewmodel.AgentConfigFile{{Path: "/home/test/config.json", Scope: "user", Precedence: "primary", Exists: true}}, Registrations: []string{"team / inspect / dev / production"}}}, content: "{\"token\":\"visible-secret\"}\nsecond line\nthird line"}
	m := New(b).(*Model)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.Update(m.Init()())
	m.navigate("Agents")
	return m, b
}

// This catches a management screen claiming an agent is installed from its ID alone.
func TestAgentsDoesNotInventDetectionOrConfigEvidence(t *testing.T) {
	m := fixtureModel(t)
	m.navigate("Agents")
	view := m.View().Content
	if !strings.Contains(view, "codex") || !strings.Contains(view, "Detection unavailable") {
		t.Fatalf("agent evidence was invented or hidden: %s", view)
	}
	press(m, tea.KeyEnter, "")
	view = m.View().Content
	if !strings.Contains(view, "View configuration files") || !strings.Contains(view, "disabled") {
		t.Fatalf("configuration viewer was offered without exact file data: %s", view)
	}
	press(m, tea.KeyEscape, "")
	if m.view != "Agents" || m.selected != 0 {
		t.Fatal("closing agent actions lost selected row")
	}
}

// This catches discarding verified detection evidence when the service provides it.
func TestAgentsShowsTypedDetectionSeparateFromConfigExistence(t *testing.T) {
	m, _ := typedAgentModel(t)
	view := m.View().Content
	for _, want := range []string{"Codex", "installed", "CLI at /usr/bin/codex", "/home/test/config.json", "team / inspect / dev / production"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q from agent details: %s", want, view)
		}
	}
}

// This catches masking config values or replacing exact file contents with a summary.
func TestAgentConfigViewerShowsExactContent(t *testing.T) {
	m, _ := typedAgentModel(t)
	press(m, tea.KeyEnter, "")
	if !strings.Contains(m.View().Content, "View configuration files") || strings.Contains(m.View().Content, "View configuration files — disabled") {
		t.Fatal("verified config viewer unavailable")
	}
	press(m, tea.KeyEnter, "")
	if !strings.Contains(m.View().Content, "/home/test/config.json") {
		t.Fatal("file picker did not show exact path")
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("file viewer did not request file content")
	}
	m.Update(cmd())
	view := m.View().Content
	if !strings.Contains(view, "visible-secret") || !strings.Contains(view, "second line") {
		t.Fatalf("viewer changed exact contents: %s", view)
	}
	press(m, tea.KeyEscape, "")
	if m.view != "Agents" {
		t.Fatal("closing viewer lost Agents screen")
	}
}

// This catches treating a saved profile as proof that an environment TOML exists.
func TestEnvironmentsShowsNoFileAndSeparatesSavedTargets(t *testing.T) {
	m := fixtureModel(t)
	m.profileSnapshot = &viewmodel.ProfileSnapshot{Profiles: []viewmodel.Profile{{Key: state.Key{Source: "team", Package: "inspect", Environment: "company", Target: "production"}, Name: "production"}}}
	m.navigate("Environments")
	view := m.View().Content
	if !strings.Contains(view, "No environment file") || !strings.Contains(view, "company") || !strings.Contains(view, "Saved profile") {
		t.Fatalf("environment browser lost file/profile distinction: %s", view)
	}
	press(m, tea.KeyDown, "")
	press(m, tea.KeyTab, "")
	view = m.View().Content
	if !strings.Contains(view, "production") {
		t.Fatalf("saved target unavailable from right pane: %s", view)
	}
	press(m, tea.KeyEnter, "")
	if !strings.Contains(m.View().Content, "View target") || !strings.Contains(m.View().Content, "disabled") {
		t.Fatal("exact TOML viewer offered without source data")
	}
}

// This catches Help returning to Home when it was opened from a management screen.
func TestManagementHelpReturnsToOrigin(t *testing.T) {
	m := fixtureModel(t)
	m.navigate("Settings")
	press(m, tea.KeyDown, "")
	press(m, tea.KeyF1, "")
	if m.view != "Help" || !strings.Contains(m.View().Content, "default") || strings.Contains(m.View().Content, "fixed:") {
		t.Fatal("context help not shown")
	}
	press(m, tea.KeyEscape, "")
	if m.view != "Settings" || m.selected != 1 {
		t.Fatalf("Help did not restore Settings selection: view=%q selected=%d", m.view, m.selected)
	}
}

// This catches management navigation being keyboard-only in a mouse-capable terminal.
func TestAgentManagementMouseSelectAndAction(t *testing.T) {
	m := fixtureModel(t)
	m.navigate("Agents")
	m.View()
	m.Update(tea.MouseClickMsg{X: 4, Y: 5, Button: tea.MouseLeft})
	if m.selected != 1 {
		t.Fatalf("mouse selected row %d, want second agent", m.selected)
	}
	m.View()
	for _, hit := range m.management.Hits {
		if hit.Control == "actions" {
			m.Update(tea.MouseClickMsg{X: hit.X + 1, Y: hit.Y, Button: tea.MouseLeft})
			break
		}
	}
	if !strings.Contains(m.View().Content, "View configuration files") {
		t.Fatal("visible Actions control not clickable")
	}
}

// This catches private and public sources being collapsed into one environment.
func TestEnvironmentBrowserKeepsSourceIdentity(t *testing.T) {
	m := fixtureModel(t)
	m.profileSnapshot = &viewmodel.ProfileSnapshot{Profiles: []viewmodel.Profile{
		{Key: state.Key{Source: "public", Package: "inspect", Environment: "dev", Target: "one"}},
		{Key: state.Key{Source: "company", Package: "inspect", Environment: "dev", Target: "two"}},
	}}
	m.navigate("Environments")
	view := m.View().Content
	if !strings.Contains(view, "public / dev") || !strings.Contains(view, "company / dev") {
		t.Fatalf("source identity lost: %s", view)
	}
	press(m, tea.KeyDown, "")
	press(m, tea.KeyRight, "")
	if !strings.Contains(m.View().Content, "two") || strings.Contains(m.View().Content, "one · Saved profile") {
		t.Fatal("targets from two sources mixed")
	}
}

// This catches unavailable Settings writes being implied by a selectable row.
func TestSettingsActionsExplainMissingPreferenceService(t *testing.T) {
	m := fixtureModel(t)
	m.navigate("Settings")
	press(m, tea.KeyF2, "")
	view := m.View().Content
	if !strings.Contains(view, "Default named agents") || !strings.Contains(view, "disabled: service support pending") || !strings.Contains(view, "Docker backend") {
		t.Fatalf("settings actions did not explain service gaps: %s", view)
	}
}

// This catches the visible Help Back control being inert for mouse users.
func TestHelpVisibleBackControlIsClickable(t *testing.T) {
	m := fixtureModel(t)
	m.navigate("Help")
	m.View()
	for _, hit := range m.management.Hits {
		if hit.Control == "back" {
			m.Update(tea.MouseClickMsg{X: hit.X + 1, Y: hit.Y, Button: tea.MouseLeft})
			if m.view != "Catalog" {
				t.Fatal("click on Help Back did not return home")
			}
			return
		}
	}
	t.Fatal("Help Back has no click region")
}

// This catches Help reopening the environment browser at a different target.
func TestHelpRestoresEnvironmentPaneAndSelection(t *testing.T) {
	m := fixtureModel(t)
	m.profileSnapshot = &viewmodel.ProfileSnapshot{Profiles: []viewmodel.Profile{{Key: state.Key{Source: "team", Package: "inspect", Environment: "dev", Target: "production"}}}}
	m.navigate("Environments")
	press(m, tea.KeyDown, "")
	press(m, tea.KeyRight, "")
	press(m, tea.KeyF1, "")
	press(m, tea.KeyEscape, "")
	if m.view != "Environments" || m.management.EnvironmentIndex != 1 || m.management.Focus != ProfilesPane || !strings.Contains(m.View().Content, "production") {
		t.Fatalf("Help lost selected environment target: %s", m.View().Content)
	}
}

// This catches stale target-pane indexes after an environment disappears on refresh.
func TestEnvironmentRefreshClampsRemovedSelection(t *testing.T) {
	m := fixtureModel(t)
	m.profileSnapshot = &viewmodel.ProfileSnapshot{Profiles: []viewmodel.Profile{{Key: state.Key{Source: "team", Package: "inspect", Environment: "dev", Target: "production"}}}}
	m.navigate("Environments")
	press(m, tea.KeyDown, "")
	press(m, tea.KeyRight, "")
	m.profileSnapshot = &viewmodel.ProfileSnapshot{}
	press(m, tea.KeyDown, "")
	if m.management.EnvironmentIndex != 0 || !strings.Contains(m.View().Content, "No environment file") {
		t.Fatal("removed environment did not clamp selection")
	}
}
