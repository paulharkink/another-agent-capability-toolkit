package tui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
	"strings"
	"testing"
)

func TestInteractionRegistrationShowsNamedAgentDetectionFailureInDetail(t *testing.T) {
	m, _ := openForeignRegistrationInteraction(t)
	m.agentManagement = []viewmodel.AgentManagementRow{{ID: "codex", Name: "Codex", Detection: "failed: permission denied", Note: "Could not inspect Codex configuration"}}
	press(m, tea.KeyDown, "")
	view := m.View().Content
	if !strings.Contains(view, "Agent detection: failed: permission denied") {
		t.Fatalf("selected agent detection failure is hidden from its detail pane:\n%s", view)
	}
}
func TestInteractionRegistrationShowsForeignNameConflictInDetail(t *testing.T) {
	m, _ := openForeignRegistrationInteraction(t)
	m.agentManagement = []viewmodel.AgentManagementRow{{ID: "codex", Name: "Codex", Detection: "installed"}, {ID: "claude", Name: "Claude", Detection: "installed", Note: `Name conflict: existing MCP entry "plain-dev" is not owned by AACT`}}
	press(m, tea.KeyDown, "")
	press(m, tea.KeyDown, "")
	view := m.View().Content
	for _, want := range []string{"Name conflict:", "plain-dev"} {
		if !strings.Contains(view, want) {
			t.Fatalf("foreign-name conflict detail is missing %q:\n%s", want, view)
		}
	}
}
func TestInteractionRegistrationPartialApplyReportsOnlySuccessfulAgentAsConfigured(t *testing.T) {
	m, b := openForeignRegistrationInteraction(t)
	b.result = viewmodel.OperationResult{Message: "Registration update completed with errors", Changes: []state.Installation{{AgentID: "codex", Component: "mcp"}}, Errors: []string{`claude: refusing foreign MCP registration "plain-dev"`}}
	m.registration.Marked["codex"] = true
	m.registration.Marked["claude"] = true
	_, cmd := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("Ctrl-S did not apply selected agent registrations")
	}
	m.Update(cmd())
	view := m.View().Content
	if !strings.Contains(view, "codex: mcp registration configured") {
		t.Fatalf("successful agent result is missing:\n%s", view)
	}
	if strings.Contains(view, "claude: mcp registration configured") {
		t.Fatalf("failed agent was reported as registered:\n%s", view)
	}
	if !strings.Contains(view, `claude: refusing foreign MCP registration "plain-dev"`) {
		t.Fatalf("concrete per-agent failure is missing:\n%s", view)
	}
}
