package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestInteractionSetupRendersAsStackedOverlayAboveHome(t *testing.T) {
	m, _ := openSetupInteraction(t)
	lines := strings.Split(ansi.Strip(m.View().Content), "\n")
	if len(lines) < 8 || !strings.Contains(lines[0], "AACT · Another Agent Capability Toolkit") {
		t.Fatalf("setup replaced the base screen instead of stacking over it:\n%s", m.View().Content)
	}
	if !strings.Contains(lines[3], "Capabilities") {
		t.Fatalf("setup overlay hid the layer-1/layer-2 headings:\n%s", m.View().Content)
	}
	found := false
	for row := 4; row < len(lines); row++ {
		x := strings.Index(lines[row], "╔")
		if x <= 0 {
			continue
		}
		if !strings.Contains(lines[row], "╗") || !strings.HasPrefix(lines[row], "║") {
			t.Fatalf("setup border is not inset over the base pane at row %d:\n%s", row, m.View().Content)
		}
		found = true
		break
	}
	if !found {
		t.Fatalf("setup has no visibly inset stacked overlay below layer 1/2:\n%s", m.View().Content)
	}
}

func TestInteractionSetupInsetMouseTargetsOnlyOverlayActions(t *testing.T) {
	m, backend := openSetupInteraction(t)
	m.View()
	m.Update(tea.MouseClickMsg{X: 1, Y: 1, Button: tea.MouseLeft})
	if m.form == nil {
		t.Fatal("clicking underlying home content dismissed the setup overlay")
	}
	lines := strings.Split(ansi.Strip(m.View().Content), "\n")
	for y, line := range lines {
		if index := strings.Index(line, "[ Cancel ]"); index >= 0 {
			x := lipgloss.Width(line[:index]) + 2
			m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
			if m.form != nil || backend.installRequest != nil {
				t.Fatalf("translated Cancel click did not dismiss without applying: form=%t request=%#v", m.form != nil, backend.installRequest)
			}
			return
		}
	}
	t.Fatal("stacked setup overlay did not show a clickable Cancel action")
}
