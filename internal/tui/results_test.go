package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestOperationResultOpensScrollableViewerWithAllAgentMessagesAndError(t *testing.T) {
	m, _ := homeFixture()
	var lines []string
	for i := 1; i <= 40; i++ {
		lines = append(lines, fmt.Sprintf("agent-%02d: MCP registration configured", i))
	}
	m.action = "configure registrations"
	m.Update(operationMsg{origin: "Catalog", output: strings.Join(lines, "\n"), err: errors.New("agent-41: permission denied")})
	first := m.View().Content
	if !strings.Contains(first, "Operation result") || !strings.Contains(first, "agent-01: MCP registration configured") || !strings.Contains(first, "agent-41: permission denied") || strings.Contains(first, "agent-40: MCP registration configured") {
		t.Fatalf("result viewer did not start at first page:\n%s", first)
	}
	press(m, tea.KeyEnd, "")
	last := m.View().Content
	if !strings.Contains(last, "agent-40: MCP registration configured") {
		t.Fatalf("last agent result was lost:\n%s", last)
	}
	press(m, tea.KeyHome, "")
	m.Update(tea.MouseWheelMsg{X: m.width / 2, Y: m.height / 2, Button: tea.MouseWheelDown})
	if !strings.Contains(m.View().Content, "agent-02: MCP registration configured") {
		t.Fatal("mouse wheel did not scroll result viewer")
	}
	press(m, tea.KeyEscape, "")
	if strings.Contains(m.View().Content, "Operation result") || m.view != "Catalog" || !strings.Contains(m.output, "agent-41: permission denied") {
		t.Fatalf("closing result viewer lost screen or status: view=%q output=%q", m.view, m.output)
	}
}

func TestFailedOperationShowsConcreteCauseOnFirstPage(t *testing.T) {
	m, _ := homeFixture()
	m.action = "install"
	lines := make([]string, 35)
	for i := range lines {
		lines[i] = fmt.Sprintf("agent-%02d: skill configured", i)
	}
	m.Update(operationMsg{origin: "Catalog", output: strings.Join(lines, "\n"), err: errors.New("claude: config file permission denied")})
	view := m.View().Content
	if !strings.Contains(view, "Failed: claude: config file permission denied") {
		t.Fatalf("specific error was hidden below the first page:\n%s", view)
	}
}

func TestOperationResultViewerClosesByMouseWithoutOpeningUnderlyingActions(t *testing.T) {
	m, _ := homeFixture()
	m.action = "install"
	m.Update(operationMsg{origin: "Catalog", output: "codex: skill configured"})
	view := m.View().Content
	if !strings.Contains(view, "codex: skill configured") || !strings.Contains(view, "Close") {
		t.Fatalf("result was not foregrounded:\n%s", view)
	}
	m.Update(tea.MouseClickMsg{X: m.width / 2, Y: m.height - 2, Button: tea.MouseLeft})
	if strings.Contains(m.View().Content, "Operation result") || m.home.Modal != nil {
		t.Fatal("Close click did not dismiss viewer cleanly")
	}
}

func TestOperationResultViewerFitsSmallTerminal(t *testing.T) {
	m, _ := homeFixture()
	m.action = "install"
	m.Update(operationMsg{origin: "Catalog", output: "done"})
	m.Update(tea.WindowSizeMsg{Width: 30, Height: 5})
	view := m.View().Content
	if len(strings.Split(view, "\n")) > 5 || !strings.Contains(view, "80×16") {
		t.Fatalf("small terminal overflowed without resize guidance: %q", view)
	}
	press(m, tea.KeyEscape, "")
	if m.result != nil {
		t.Fatal("small terminal cannot close result")
	}
}

func TestOperationResultViewerPreservesGlobalQuitShortcut(t *testing.T) {
	m, _ := homeFixture()
	m.Update(operationMsg{origin: "Catalog", output: "done"})
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyF10})
	if cmd == nil {
		t.Fatal("F10 was swallowed by operation result viewer")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("F10 did not quit: %T", cmd())
	}
}
