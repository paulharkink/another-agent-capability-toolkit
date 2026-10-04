package forms

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
)

func TestLongSplitSectionKeepsActionsAndShowsScrollCues(t *testing.T) {
	defs := make([]catalog.Input, 20)
	fields := make([]string, len(defs))
	for i := range defs {
		name := fmt.Sprintf("field_%02d", i)
		defs[i] = catalog.Input{Name: name, Label: fmt.Sprintf("Field %02d", i), Type: "string"}
		fields[i] = name
	}
	m := NewForm(context.Background(), defs, nil)
	m.SetSections(FormSection{Title: "Connection", Fields: fields})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	first := ansi.Strip(m.View().Content)
	if got := len(strings.Split(first, "\n")); got != 24 {
		t.Fatalf("split form should fit 24 terminal rows, got %d", got)
	}
	if !strings.Contains(first, "[ Save ]") || !strings.Contains(first, "[ Cancel ]") || !strings.Contains(first, "↓ more") {
		t.Fatalf("long split form hid actions or scroll cue:\n%s", first)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	for i := 0; i < 19; i++ {
		m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	last := ansi.Strip(m.View().Content)
	if got := len(strings.Split(last, "\n")); got != 24 {
		t.Fatalf("scrolled split form should fit 24 terminal rows, got %d", got)
	}
	if !strings.Contains(last, "Field 19") || !strings.Contains(last, "↑ more") || !strings.Contains(last, "[ Save ]") {
		t.Fatalf("selected late field or actions are hidden after scrolling:\n%s", last)
	}
	clickVisibleText(t, m, "Field 18:")
	if m.selected != 18 || !m.editing {
		t.Fatalf("mouse click after split scroll selected field %d, editing=%t", m.selected, m.editing)
	}
}
