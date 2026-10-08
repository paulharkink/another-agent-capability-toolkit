package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func (m *Model) setupInformationView() tea.View {
	width := max(40, m.width)
	height := max(12, m.height)
	lines := workspaceInformationLines(*m.pendingSetup, width-4)
	visible := max(1, height-8)
	start := min(max(0, m.setupInfoOffset), max(0, len(lines)-visible))
	m.setupInfoOffset = start
	box := []string{"╔" + fit(" Profile information · F3 / Esc Back", width-2) + "╗"}
	for i := 0; i < visible; i++ {
		line := ""
		if start+i < len(lines) {
			line = lines[start+i]
		}
		box = append(box, "║"+fit(line, width-2)+"║")
	}
	position := fmt.Sprintf("Line %d/%d", start+1, max(1, len(lines)))
	if start > 0 {
		position = "↑ More above · " + position
	}
	if start+visible < len(lines) {
		position += " · ↓ More below"
	}
	box = append(box, "║"+fit(position, width-2)+"║", "║"+fit("↑↓ / PgUp / PgDn Scroll · Esc or F3 Return to configuration", width-2)+"║", "╚"+strings.Repeat("═", width-2)+"╝")
	return tea.NewView(navyCanvas(strings.Join(box, "\n")))
}

func splitDisplayLine(line string, width int) []string {
	if line == "" {
		return []string{""}
	}
	width = max(1, width)
	out := []string{}
	current := ""
	for _, r := range line {
		candidate := current + string(r)
		if current != "" && lipgloss.Width(candidate) > width {
			out = append(out, current)
			current = string(r)
		} else {
			current = candidate
		}
	}
	return append(out, current)
}
