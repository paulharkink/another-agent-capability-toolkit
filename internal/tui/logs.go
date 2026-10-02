package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

type logsMsg struct {
	label, content string
	err            error
}

// openProfileLogs asks the existing service for a bounded, read-only snapshot.
func (m *Model) openProfileLogs(p ProfileRow) tea.Cmd {
	if reason := m.profileActionReason(p, "l"); reason != "" {
		m.output = reason
		return nil
	}
	key := p.Key
	label := p.Name
	if label == "" {
		label = p.Key.Package + " / " + p.Key.Target
	}
	backend := m.backend
	ctx := m.ctx
	m.busy = true
	m.action = "view logs"
	m.home.Modal = nil
	return func() tea.Msg {
		content, err := backend.UIRun(ctx, "logs", key.Source, key.Package, "", key.Environment, key.Target)
		return logsMsg{label: label, content: content, err: err}
	}
}

func (m *Model) showLogs(msg logsMsg) {
	m.busy = false
	if msg.err != nil {
		m.output = m.cleanOutput(msg.err.Error())
		return
	}
	content := strings.TrimSuffix(msg.content, "\n")
	rows := []string{"No log output"}
	if content != "" {
		rows = strings.Split(content, "\n")
	}
	for i, line := range rows {
		rows[i] = ansi.Strip(strings.TrimSuffix(line, "\r"))
	}
	m.home.Modal = &modalState{Kind: "logs", Label: msg.label, Rows: rows}
}

func (m *Model) logVisibleRows() int { return max(1, m.height-8) }

func (m *Model) logKey(stroke string) tea.Cmd {
	modal := m.home.Modal
	if modal == nil || modal.Kind != "logs" {
		return nil
	}
	limit := max(0, len(modal.Rows)-m.logVisibleRows())
	switch stroke {
	case "esc", "enter", "q":
		m.home.Modal = nil
	case "up", "k":
		modal.Offset = max(0, modal.Offset-1)
	case "down", "j":
		modal.Offset = min(limit, modal.Offset+1)
	case "pgup":
		modal.Offset = max(0, modal.Offset-m.logVisibleRows())
	case "pgdown":
		modal.Offset = min(limit, modal.Offset+m.logVisibleRows())
	case "home":
		modal.Offset = 0
	case "end":
		modal.Offset = limit
	case "left":
		modal.Column = max(0, modal.Column-4)
	case "right":
		modal.Column += 4
	}
	return nil
}

func (m *Model) logMouse(msg tea.MouseMsg) tea.Cmd {
	modal := m.home.Modal
	if modal == nil || modal.Kind != "logs" {
		return nil
	}
	mouse := msg.Mouse()
	if _, ok := msg.(tea.MouseWheelMsg); ok {
		if mouse.Button == tea.MouseWheelUp {
			modal.Offset = max(0, modal.Offset-1)
		} else if mouse.Button == tea.MouseWheelDown {
			modal.Offset = min(max(0, len(modal.Rows)-m.logVisibleRows()), modal.Offset+1)
		}
		return nil
	}
	if _, ok := msg.(tea.MouseClickMsg); ok && mouse.Button == tea.MouseLeft {
		for _, h := range m.home.Hits {
			if h.Control == "log-close" && h.contains(mouse.X, mouse.Y) {
				m.home.Modal = nil
				break
			}
		}
	}
	return nil
}

func (m *Model) logsOverlay(lines []string) []string {
	modal := m.home.Modal
	if modal == nil || modal.Kind != "logs" {
		return lines
	}
	w := min(m.width-4, 100)
	x := (m.width - w) / 2
	visible := min(len(modal.Rows), m.logVisibleRows())
	visible = max(1, visible)
	modal.Offset = min(modal.Offset, max(0, len(modal.Rows)-visible))
	y := max(2, (len(lines)-visible-3)/2)
	box := []string{"┌" + fit(" Logs · "+modal.Label+" · latest 200 lines", w-2) + "┐"}
	for i := 0; i < visible; i++ {
		row := modal.Rows[modal.Offset+i]
		box = append(box, "│"+fit(ansi.Cut(row, modal.Column, modal.Column+w-2), w-2)+"│")
	}
	status := " ↑↓/PgUp/PgDn Scroll · ←→ Pan · Esc Close"
	box = append(box, "│"+fit(status, w-2)+"│", "└"+strings.Repeat("─", w-2)+"┘")
	m.home.Hits = append(m.home.Hits, hitRegion{X: x + 1, Y: y + len(box) - 2, Width: w - 2, Height: 1, Control: "log-close"})
	for i, line := range box {
		if y+i >= len(lines) {
			break
		}
		lines[y+i] = ansi.Cut(lines[y+i], 0, x) + line + ansi.Cut(lines[y+i], x+w, m.width)
	}
	return lines
}
