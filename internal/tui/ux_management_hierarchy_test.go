package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

type uxAgentPathBackend struct {
	fixtureBackend
	rows          []viewmodel.AgentManagementRow
	requestedPath string
}

func (b *uxAgentPathBackend) UIAgentManagement(context.Context) ([]viewmodel.AgentManagementRow, error) {
	return b.rows, nil
}

func (b *uxAgentPathBackend) UIAgentConfig(_ context.Context, _ string, path string) (string, error) {
	b.requestedPath = path
	return "contents from " + path, nil
}

func TestUXAgentsOverviewEnterFocusesDetailsAndDetailsShowsResolution(t *testing.T) {
	m := fixtureModel(t)
	m.agentManagement = []viewmodel.AgentManagementRow{{
		ID: "codex", Name: "Codex", Detection: "detected", Evidence: "binary: /opt/codex",
		Home: "/home/test/.codex", EffectiveConfigPath: "/home/test/.codex/config.toml",
		WriteConfigPath: "/home/test/.codex/config.toml", ConfigFiles: []viewmodel.AgentConfigFile{{Path: "/home/test/.codex/config.toml", Scope: "user", Precedence: "effective", Exists: true}},
	}}
	m.navigate("Agents")
	press(m, tea.KeyEnter, "")
	view := ansi.Strip(m.View().Content)
	for _, want := range []string{"Detected", "binary: /opt/codex", "Active home", "/home/test/.codex", "Effective config", "Intended write config", "Config candidates"} {
		if !strings.Contains(view, want) {
			t.Errorf("Agents detail omitted %q:\n%s", want, view)
		}
	}
	if m.management.Focus != ProfilesPane {
		t.Errorf("Enter did not focus the details pane: %v", m.management.Focus)
	}
}

func TestUXAgentConfigActionStaysVisibleWhenDetailsScroll(t *testing.T) {
	for _, size := range []struct{ width, height int }{{80, 16}, {100, 23}} {
		t.Run(fmt.Sprintf("%dx%d", size.width, size.height), func(t *testing.T) {
			m := fixtureModel(t)
			m.agentManagement = []viewmodel.AgentManagementRow{{
				ID: "codex", Name: "Codex", Detection: "detected", Evidence: strings.Repeat("long executable evidence ", 5),
				Home: "/home/test/.codex", EffectiveConfigPath: "/home/test/.codex/config.toml", WriteConfigPath: "/home/test/.codex/config.toml",
				ConfigFiles: []viewmodel.AgentConfigFile{{Path: "/home/test/.codex/config.toml", Scope: "user", Precedence: "effective", Exists: true}},
			}}
			m.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
			m.navigate("Agents")
			press(m, tea.KeyEnter, "")
			view := ansi.Strip(m.View().Content)
			if !strings.Contains(view, "View exact configuration candidate 1") {
				t.Fatalf("focused exact-config action is outside visible detail viewport:\n%s", view)
			}
			if !strings.Contains(view, "More above") {
				t.Fatalf("scrolled details omit the above cue:\n%s", view)
			}
		})
	}
}

func TestUXUnmaskedExactConfigViewerReturnsSameAgent(t *testing.T) {
	b := &typedAgentBackend{rows: []viewmodel.AgentManagementRow{{ID: "codex", Name: "Codex", Detection: "detected", ConfigFiles: []viewmodel.AgentConfigFile{{Path: "/home/test/config.json", Exists: true}}}}, content: "token = \"visible-secret\""}
	m := New(b).(*Model)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.agentManagement = b.rows
	m.navigate("Agents")
	m.managementKey("enter")
	cmd := m.managementKey("enter")
	if cmd == nil {
		t.Fatal("focused exact config control did not request its viewer")
	}
	_, _ = m.Update(cmd())
	if m.management.Modal != "viewer" || !strings.Contains(ansi.Strip(m.View().Content), "visible-secret") {
		t.Fatalf("exact configuration viewer did not show the unmasked file: %s", ansi.Strip(m.View().Content))
	}
	press(m, tea.KeyEscape, "")
	if m.view != "Agents" || m.selected != 0 || m.management.Focus != ProfilesPane {
		t.Fatalf("viewer did not return to the same agent details: view=%s selected=%d focus=%v", m.view, m.selected, m.management.Focus)
	}
}

func TestUXAgentSecondConfigControlOpensSecondCandidate(t *testing.T) {
	b := &uxAgentPathBackend{rows: []viewmodel.AgentManagementRow{{
		ID: "codex", Name: "Codex", Detection: "detected",
		ConfigFiles: []viewmodel.AgentConfigFile{
			{Path: "/home/test/effective.json", Scope: "user", Exists: true},
			{Path: "/home/test/project.json", Scope: "project", Exists: true},
		},
	}}}
	m := New(b).(*Model)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.agentManagement = b.rows
	m.navigate("Agents")
	press(m, tea.KeyEnter, "")
	press(m, tea.KeyDown, "")
	if cmd := m.managementKey("enter"); cmd == nil {
		t.Fatal("second exact configuration control did not request its viewer")
	} else {
		_, _ = m.Update(cmd())
	}
	if b.requestedPath != "/home/test/project.json" || m.management.ViewerPath != b.requestedPath {
		t.Fatalf("second candidate opened the wrong config: requested=%q viewer=%q", b.requestedPath, m.management.ViewerPath)
	}
}

func TestUXAgentMouseOpensClickedConfigCandidate(t *testing.T) {
	b := &uxAgentPathBackend{rows: []viewmodel.AgentManagementRow{{
		ID: "codex", Name: "Codex", Detection: "detected",
		ConfigFiles: []viewmodel.AgentConfigFile{
			{Path: "/home/test/effective.json", Scope: "user", Exists: true},
			{Path: "/home/test/project.json", Scope: "project", Exists: true},
		},
	}}}
	m := New(b).(*Model)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.agentManagement = b.rows
	m.navigate("Agents")
	m.management.Focus = ProfilesPane
	_, _, firstControl := m.agentManagementDisplayLines()
	m.View()
	var candidate hitRegion
	for _, hit := range m.management.Hits {
		if hit.Control == "agent-config" && hit.Index == firstControl+1 {
			candidate = hit
			break
		}
	}
	if candidate.Control == "" {
		t.Fatal("second candidate has no visible mouse control")
	}
	_, cmd := m.Update(tea.MouseClickMsg{X: candidate.X + 1, Y: candidate.Y, Button: tea.MouseLeft})
	if cmd != nil {
		_, _ = m.Update(cmd())
	}
	if b.requestedPath != "/home/test/project.json" || m.management.ViewerPath != b.requestedPath {
		t.Fatalf("mouse opened the wrong config candidate: requested=%q viewer=%q", b.requestedPath, m.management.ViewerPath)
	}
}

func TestUXAgentFactSelectionDoesNotOpenAnyConfig(t *testing.T) {
	b := &uxAgentPathBackend{rows: []viewmodel.AgentManagementRow{{
		ID: "codex", Detection: "detected", Evidence: "binary: /opt/codex",
		ConfigFiles: []viewmodel.AgentConfigFile{{Path: "/home/test/config.json", Exists: true}},
	}}}
	m := New(b).(*Model)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.agentManagement = b.rows
	m.navigate("Agents")
	press(m, tea.KeyEnter, "")
	press(m, tea.KeyHome, "")
	if cmd := m.managementKey("enter"); cmd != nil {
		t.Fatal("Enter on a fact requested a configuration viewer")
	}
	if m.management.Modal == "viewer" || b.requestedPath != "" {
		t.Fatalf("fact selection opened a config: modal=%q path=%q", m.management.Modal, b.requestedPath)
	}
}

func TestUXSettingsFactEnterDoesNotActivateOffscreenAction(t *testing.T) {
	m := fixtureModel(t)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 16})
	m.navigate("Settings")
	m.management.Focus = ProfilesPane
	m.management.SettingsDetailIndex = 0 // Capability Pack heading, not an action.
	if cmd := m.managementKey("enter"); cmd != nil || m.form != nil {
		t.Fatal("Enter on a Capability Pack fact activated the offscreen edit action")
	}
	if m.management.Focus != ProfilesPane || m.view != "Settings" {
		t.Fatalf("non-action activation changed navigation: view=%q focus=%v", m.view, m.management.Focus)
	}
}

func TestUXSettingsActionRequiresHighlightedControl(t *testing.T) {
	m := fixtureModel(t)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 16})
	m.navigate("Settings")
	m.management.Focus = ProfilesPane
	m.management.SettingsDetailIndex = 0
	if cmd := m.managementKey("enter"); cmd != nil {
		_, _ = m.Update(cmd())
	}
	if m.form != nil {
		t.Fatal("a non-action Capability Pack fact opened a Settings editor")
	}
	press(m, tea.KeyEnd, "")
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "Change environment directory") || !strings.Contains(view, "More above") {
		t.Fatalf("focused Settings action is not visible with its scroll cue:\n%s", view)
	}
	if cmd := m.managementKey("enter"); cmd != nil {
		_, _ = m.Update(cmd())
	}
	if m.form == nil {
		t.Fatal("Enter on the highlighted Settings action did not open its editor")
	}
}

func TestUXMissingConfigDoesNotClaimAgentMissing(t *testing.T) {
	m := fixtureModel(t)
	m.agentManagement = []viewmodel.AgentManagementRow{{ID: "claude", Detection: "not-detected", ConfigFiles: []viewmodel.AgentConfigFile{{Path: "/tmp/claude.json", Exists: false}}}}
	m.navigate("Agents")
	view := strings.ReplaceAll(ansi.Strip(m.View().Content), "\n", " ")
	if !strings.Contains(view, "Status: Not detected") || !strings.Contains(view, "/tmp/claude.json") {
		t.Fatalf("config absence replaced detection status or candidate path:\n%s", view)
	}
}

func TestUXUnsupportedServicesAreExplanationsNotFakeButtons(t *testing.T) {
	m := fixtureModel(t)
	m.navigate("Settings")
	m.selected = 2
	view := strings.ReplaceAll(ansi.Strip(m.View().Content), "\n", " ")
	if !strings.Contains(view, "unavailable") || !strings.Contains(view, "this service") || strings.Contains(view, "[ Select backend") || strings.Contains(view, "disabled:") {
		t.Fatalf("unsupported runtime service is shown as a fake action:\n%s", view)
	}
}

func TestUXSettingsCategoriesHaveRelatedControlsOnly(t *testing.T) {
	m := fixtureModel(t)
	m.navigate("Settings")
	view := ansi.Strip(m.View().Content)
	for _, category := range []string{"Capability Pack", "Agent defaults", "Runtime backend", "Diagnostics"} {
		if !strings.Contains(view, category) {
			t.Errorf("Settings omitted category %q:\n%s", category, view)
		}
	}
}

func TestUXManagementReadonlyDiagnosticsNoSave(t *testing.T) {
	m := fixtureModel(t)
	m.navigate("Settings")
	// The category list is the layer-one view; Enter opens the selected category.
	press(m, tea.KeyEnd, "")
	press(m, tea.KeyEnter, "")
	view := ansi.Strip(m.View().Content)
	if strings.Contains(view, "[ Save ]") || strings.Contains(view, "[ Cancel ]") {
		t.Fatalf("read-only diagnostics offers edit controls:\n%s", view)
	}
}

func TestUXHelpMatchesCurrentKeyGrammarAndReturnsOrigin(t *testing.T) {
	m := fixtureModel(t)
	m.navigate("Agents")
	m.management.Focus = ProfilesPane
	press(m, tea.KeyF1, "")
	view := ansi.Strip(m.View().Content)
	for _, want := range []string{"Enter", "F2", "Open / focus", "F9", "m", "Esc", "scroll"} {
		if !strings.Contains(strings.ToLower(view), strings.ToLower(want)) {
			t.Errorf("Help omitted key grammar %q:\n%s", want, view)
		}
	}
	press(m, tea.KeyEscape, "")
	if m.view != "Agents" || m.management.Focus != ProfilesPane {
		t.Fatalf("Help did not restore its origin and focus: view=%q focus=%v", m.view, m.management.Focus)
	}
}
