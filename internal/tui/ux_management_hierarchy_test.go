package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

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
	for _, category := range []string{"Environment source", "Agent defaults", "Runtime backend", "Diagnostics"} {
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
