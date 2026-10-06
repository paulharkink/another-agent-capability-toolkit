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
		if !strings.Contains(view, want) {
			t.Fatalf("focused pane %q missing after Tab:\n%s", want, view)
		}
	}
	assertFocusedPane("L3 Sections · FOCUSED")
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	assertFocusedPane("L4 · Connection · FOCUSED")
	assertFocusedPane("> Endpoint")
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	assertFocusedPane("[Actions]")
	assertFocusedPane("[ Save ]")
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	assertFocusedPane("L3 Sections · FOCUSED")
}
