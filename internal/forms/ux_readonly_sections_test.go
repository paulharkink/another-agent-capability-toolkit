package forms

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
)

func TestReadOnlySectionContentIsVisibleAndScrollableWithoutMovingSection(t *testing.T) {
	form := NewForm(t.Context(), nil, nil)
	form.SetSections(FormSection{Title: "Overview"}, FormSection{Title: "Information"})
	form.SetSectionContent("Overview", []string{
		"Target configuration: saved",
		"Package installation: recorded",
		"MCP runtime: running",
		"Ownership: local",
		"Reachability: not checked",
		"Registration: codex",
		"Observation: current",
	})
	form.Update(tea.WindowSizeMsg{Width: 64, Height: 12})
	form.SelectSection("Overview")
	start := ansi.Strip(form.View().Content)
	for _, line := range []string{"Target configuration: saved", "Package installation: recorded"} {
		if !strings.Contains(start, line) {
			t.Fatalf("read-only section omitted %q:\n%s", line, start)
		}
	}
	form.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	for range 5 {
		form.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	view := ansi.Strip(form.View().Content)
	if form.SectionTitle() != "Overview" || !strings.Contains(view, "↑ more") || !strings.Contains(view, "Observation: current") {
		t.Fatalf("read-only scroll moved L3 or has no scroll cue: section=%q\n%s", form.SectionTitle(), view)
	}
	if form.editing {
		t.Fatal("entering or scrolling a read-only section started field editing")
	}
}

func TestUXReadOnlySectionCannotEditPriorSectionField(t *testing.T) {
	m := NewForm(t.Context(), []catalog.Input{{Name: "token", Label: "Token", Type: "secret"}}, map[string]any{"token": "saved"})
	m.SetSections(
		FormSection{Title: "Overview"},
		FormSection{Title: "Authentication", Fields: []string{"token"}},
	)
	m.Update(tea.KeyPressMsg{Code: tea.KeyRight}) // Focus the read-only Overview pane.
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.editing || m.selected != len(m.defs) {
		t.Fatalf("read-only Overview activated an unrelated field: editing=%v selected=%d", m.editing, m.selected)
	}
}
