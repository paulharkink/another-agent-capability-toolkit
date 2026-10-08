package forms

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
)

func TestUXTabCyclesSectionsDetailsAndActions(t *testing.T) {
	m := NewForm(t.Context(), []catalog.Input{{Name: "endpoint", Label: "Endpoint", Type: "string"}}, map[string]any{"endpoint": "https://fixture.invalid"})
	m.SetSections(FormSection{Title: "Connection", Fields: []string{"endpoint"}}, FormSection{Title: "Authentication"})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 23})
	assertFocusedPane := func(want string) {
		t.Helper()
		view := ansi.Strip(m.View().Content)
		if strings.Contains(view, "L3") || strings.Contains(view, "L4") || strings.Contains(view, "FOCUSED") {
			t.Fatalf("pane hierarchy labels appeared in the rendered view:\n%s", view)
		}
		if !strings.Contains(view, want) {
			t.Fatalf("pane title %q missing after Tab:\n%s", want, view)
		}
	}
	assertFocusedPane("── Sections")
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	assertFocusedPane("── Connection")
	assertFocusedPane("> Endpoint")
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	assertFocusedPane("[Actions]")
	assertFocusedPane("[ Save ]")
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	assertFocusedPane("── Sections")
}
