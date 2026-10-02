package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

type resultState struct {
	Action string
	Rows   []string
	Offset int
	Column int
}

func (m *Model) showOperationResult(msg operationMsg) {
	m.output = m.cleanOutput(msg.output)
	if msg.err != nil {
		if m.output != "" {
			m.output += "\n"
		}
		m.output += m.cleanOutput(msg.err.Error())
	}
	rows := []string{"Completed"}
	if m.output != "" {
		rows = strings.Split(strings.TrimSuffix(m.output, "\n"), "\n")
	}
	for i, row := range rows {
		rows[i] = ansi.Strip(strings.TrimSuffix(row, "\r"))
	}
	m.result = &resultState{Action: m.action, Rows: rows}
}

func (m *Model) resultVisibleRows() int { return max(1, m.height-6) }

func (m *Model) resultKey(stroke string) tea.Cmd {
	r := m.result
	if r == nil {
		return nil
	}
	limit := max(0, len(r.Rows)-m.resultVisibleRows())
	switch stroke {
	case "f10", "ctrl+c":
		return tea.Quit
	case "esc", "enter", "q":
		m.result = nil
	case "up", "k":
		r.Offset = max(0, r.Offset-1)
	case "down", "j":
		r.Offset = min(limit, r.Offset+1)
	case "pgup":
		r.Offset = max(0, r.Offset-m.resultVisibleRows())
	case "pgdown":
		r.Offset = min(limit, r.Offset+m.resultVisibleRows())
	case "home":
		r.Offset = 0
	case "end":
		r.Offset = limit
	case "left":
		r.Column = max(0, r.Column-4)
	case "right":
		r.Column += 4
	}
	return nil
}

func (m *Model) resultMouse(msg tea.MouseMsg) tea.Cmd {
	r := m.result
	if r == nil {
		return nil
	}
	mouse := msg.Mouse()
	if _, ok := msg.(tea.MouseWheelMsg); ok {
		if mouse.Button == tea.MouseWheelUp {
			r.Offset = max(0, r.Offset-1)
		} else if mouse.Button == tea.MouseWheelDown {
			r.Offset = min(max(0, len(r.Rows)-m.resultVisibleRows()), r.Offset+1)
		}
		return nil
	}
	if _, ok := msg.(tea.MouseClickMsg); ok && mouse.Button == tea.MouseLeft && mouse.Y == m.height-2 {
		m.result = nil
	}
	return nil
}

func (m *Model) resultView() tea.View {
	if m.width < 80 || m.height < 16 {
		lines := []string{
			fmt.Sprintf("AACT needs 80×16; current %d×%d", m.width, m.height),
			"Resize terminal · Esc close · F10 quit",
		}
		if m.height < len(lines) {
			lines = lines[:max(0, m.height)]
		}
		for i := range lines {
			lines[i] = fit(lines[i], max(1, m.width))
		}
		return tea.NewView(strings.Join(lines, "\n"))
	}
	width, height := max(20, m.width), max(8, m.height)
	r := m.result
	visible := max(1, height-6)
	r.Offset = min(r.Offset, max(0, len(r.Rows)-visible))
	lines := make([]string, height)
	lines[0] = "╔" + strings.Repeat("═", width-2) + "╗"
	lines[1] = "║" + fit(" Operation result · "+r.Action, width-2) + "║"
	lines[2] = "╠" + strings.Repeat("═", width-2) + "╣"
	for i := 0; i < visible; i++ {
		row := ""
		if index := r.Offset + i; index < len(r.Rows) {
			row = ansi.Cut(r.Rows[index], r.Column, r.Column+width-3)
		}
		lines[3+i] = "║" + fit(" "+row, width-2) + "║"
	}
	lines[height-3] = "╠" + strings.Repeat("═", width-2) + "╣"
	lines[height-2] = "║" + fit(fmt.Sprintf(" Close [Enter/Esc/click] · ↑↓/PgUp/PgDn Scroll · ←→ Pan · %d-%d/%d", min(r.Offset+1, len(r.Rows)), min(r.Offset+visible, len(r.Rows)), len(r.Rows)), width-2) + "║"
	lines[height-1] = "╚" + strings.Repeat("═", width-2) + "╝"
	v := tea.NewView(strings.Join(lines, "\n"))
	v.MouseMode = tea.MouseModeCellMotion
	return v
}
