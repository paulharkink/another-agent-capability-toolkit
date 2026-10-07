package tui

import (
	"context"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
)

type logsMsg struct {
	label, content string
	err            error
	session        uint64
}
type profileLogsBackend interface {
	UIProfileLogs(context.Context, state.Key) (string, error)
}
type logPollMsg struct{ session uint64 }

// openProfileLogs asks the existing service for a bounded, read-only snapshot.
func (m *Model) openProfileLogs(p ProfileRow) tea.Cmd {
	m.cancelLogFetch()
	if reason := m.profileActionReason(p, "l"); reason != "" {
		m.output = reason
		return nil
	}
	if p.Profile != nil {
		if p.Profile.ObservationStale {
			m.logSession++
			m.home.Modal = &modalState{Kind: "logs", Label: p.Name, Rows: []string{"Runtime observation is stale.", "Refresh the target status before viewing logs."}}
			return nil
		}
		if p.Profile.RuntimeStatus != "running" {
			label := p.Name
			if label == "" {
				label = p.Key.Package + " / " + p.Key.Target
			}
			message := "No MCP container has been created for this target. Configure this target, then use Start from its workspace before opening logs."
			if p.Profile.RuntimeStatus == "missing" || p.Profile.RuntimeStatus == "exited" {
				message = "No MCP container is currently running for this target. Use Start from its workspace before opening logs."
			}
			m.logSession++
			m.logProfile = p.Key
			m.logLabel = label
			m.busy = false
			m.home.Modal = &modalState{Kind: "logs", Label: label, Rows: strings.Split(message, "\n"), Follow: false}
			return nil
		}
	}
	key := p.Key
	label := p.Name
	if label == "" {
		label = p.Key.Package + " / " + p.Key.Target
	}
	m.logSession++
	m.logProfile = key
	m.logLabel = label
	m.busy = true
	m.action = "view logs"
	m.home.Modal = &modalState{Kind: "logs", Label: label, Rows: []string{"Loading logs…"}, Follow: true}
	return m.fetchLogs(m.logSession)
}

func (m *Model) fetchLogs(_ uint64) tea.Cmd {
	m.cancelLogFetch()
	// Each request gets its own session so a canceled, superseded request cannot
	// be mistaken for the active request's failure after Follow resumes.
	m.logSession++
	session := m.logSession
	backend, key, label := m.backend, m.logProfile, m.logLabel
	ctx, cancel := context.WithCancel(m.ctx)
	m.logCancel = cancel
	return func() tea.Msg {
		var content string
		var err error
		if logs, ok := backend.(profileLogsBackend); ok {
			content, err = logs.UIProfileLogs(ctx, key)
		} else {
			content, err = backend.UIRun(ctx, "logs", key.Source, key.Package, "", key.Environment, key.Target)
		}
		return logsMsg{label: label, content: content, err: err, session: session}
	}
}

func (m *Model) nextLogPoll(session uint64) tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return logPollMsg{session: session} })
}

func (m *Model) showLogs(msg logsMsg) tea.Cmd {
	if msg.session != m.logSession {
		return nil
	}
	m.logCancel = nil
	m.busy = false
	if msg.err != nil {
		m.showOperationResult(operationMsg{origin: m.view, err: msg.err, failed: true, step: "logs", target: setupTargetLabel(m.logProfile)})
		return nil
	}
	if m.home.Modal != nil && m.home.Modal.Kind == "logs" && !m.home.Modal.Follow {
		return nil
	}
	content := strings.TrimSuffix(msg.content, "\n")
	rows := []string{"No log output"}
	if content != "" {
		rows = strings.Split(content, "\n")
	}
	for i, line := range rows {
		rows[i] = ansi.Strip(strings.TrimSuffix(line, "\r"))
	}
	if m.home.Modal != nil && m.home.Modal.Kind == "logs" {
		modal := m.home.Modal
		modal.Rows = rows
		modal.Offset = max(0, len(rows)-m.logVisibleRows())
	} else {
		label := msg.label
		if label == "" {
			label = m.logProfile.Package + " / " + m.logProfile.Target
		}
		m.home.Modal = &modalState{Kind: "logs", Label: label, Rows: rows, Offset: max(0, len(rows)-m.logVisibleRows()), Follow: true}
	}
	return m.nextLogPoll(msg.session)
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
		m.logSession++
		m.cancelLogFetch()
	case "f":
		modal.Follow = !modal.Follow
		if modal.Follow {
			modal.Offset = max(0, len(modal.Rows)-m.logVisibleRows())
			return m.fetchLogs(m.logSession)
		}
	case "up", "k":
		modal.Follow = false
		modal.Offset = max(0, modal.Offset-1)
	case "down", "j":
		modal.Follow = false
		modal.Offset = min(limit, modal.Offset+1)
	case "pgup":
		modal.Follow = false
		modal.Offset = max(0, modal.Offset-m.logVisibleRows())
	case "pgdown":
		modal.Follow = false
		modal.Offset = min(limit, modal.Offset+m.logVisibleRows())
	case "home":
		modal.Follow = false
		modal.Offset = 0
	case "end":
		modal.Follow = false
		modal.Offset = limit
	case "left":
		modal.Column = max(0, modal.Column-4)
	case "right":
		modal.Column += 4
	}
	return nil
}

func (m *Model) cancelLogFetch() {
	if m.logCancel != nil {
		m.logCancel()
		m.logCancel = nil
	}
}

func (m *Model) logMouse(msg tea.MouseMsg) tea.Cmd {
	modal := m.home.Modal
	if modal == nil || modal.Kind != "logs" {
		return nil
	}
	mouse := msg.Mouse()
	if _, ok := msg.(tea.MouseWheelMsg); ok {
		modal.Follow = false
		if mouse.Button == tea.MouseWheelUp {
			modal.Offset = max(0, modal.Offset-1)
		} else if mouse.Button == tea.MouseWheelDown {
			modal.Offset = min(max(0, len(modal.Rows)-m.logVisibleRows()), modal.Offset+1)
		}
		return nil
	}
	if _, ok := msg.(tea.MouseClickMsg); ok && mouse.Button == tea.MouseLeft {
		for _, h := range m.home.Hits {
			if h.Control == "log-follow" && h.contains(mouse.X, mouse.Y) {
				return m.logKey("f")
			}
			if h.Control == "log-close" && h.contains(mouse.X, mouse.Y) {
				m.home.Modal = nil
				m.logSession++
				m.cancelLogFetch()
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
		textWidth := w - 2
		bar := " "
		if len(modal.Rows) > visible {
			textWidth = w - 3
			bar = scrollbarGlyph(modal.Offset, len(modal.Rows), visible, i)
		}
		box = append(box, "│"+fit(ansi.Cut(row, modal.Column, modal.Column+textWidth), textWidth)+bar+"│")
	}
	mode := "[F Pause]"
	if !modal.Follow {
		mode = "[F Follow]"
	}
	status := " " + mode + " · ↑↓/PgUp/PgDn Scroll · ←→ Pan · [Close]"
	box = append(box, "│"+fit(status, w-2)+"│", "└"+strings.Repeat("─", w-2)+"┘")
	statusY := y + len(box) - 2
	m.home.Hits = append(m.home.Hits,
		hitRegion{X: x + 2, Y: statusY, Width: len(mode), Height: 1, Control: "log-follow"},
		hitRegion{X: x + 1 + strings.Index(status, "[Close]"), Y: statusY, Width: len("[Close]"), Height: 1, Control: "log-close"})
	for i, line := range box {
		if y+i >= len(lines) {
			break
		}
		lines[y+i] = ansi.Cut(lines[y+i], 0, x) + line + ansi.Cut(lines[y+i], x+w, m.width)
	}
	return lines
}
