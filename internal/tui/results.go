package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

type resultState struct {
	Action      string
	Rows        []string
	Offset      int
	Column      int
	Failed      bool
	ActionIndex int
}

func (m *Model) showOperationResult(msg operationMsg) {
	m.output = m.cleanOutput(msg.output)
	if msg.err != nil {
		cause := "Failed: " + m.cleanOutput(msg.err.Error())
		if m.output == "" {
			m.output = cause
		} else {
			m.output = cause + "\n" + m.output
		}
	}
	rows := []string{"Completed"}
	if m.output != "" {
		rows = strings.Split(strings.TrimSuffix(m.output, "\n"), "\n")
	}
	for i, row := range rows {
		rows[i] = ansi.Strip(strings.TrimSuffix(row, "\r"))
	}
	m.result = &resultState{Action: m.action, Rows: rows, Failed: msg.err != nil || msg.failed}
}

func (m *Model) canEditResultAnswers() bool {
	return m.result != nil && m.result.Failed && m.setupRetry != nil
}

func (m *Model) editResultAnswers() {
	if !m.canEditResultAnswers() {
		return
	}
	draft := m.setupRetry
	m.result = nil
	m.openSetupFormWithValues(draft.preview, draft.values)
}

func resultBodyWidth(viewportWidth int) int {
	width := min(70, viewportWidth-8)
	if viewportWidth >= 80 {
		width = viewportWidth - 8
	}
	return max(1, width-4)
}

func (m *Model) resultVisibleRows() int {
	if m.height < 16 {
		return max(1, m.height-6)
	}
	visualRows := wrapResultRows(m.result.Rows, resultBodyWidth(m.width))
	dialogHeight := min(m.height-4, max(9, min(15, len(visualRows)+6)))
	return max(1, dialogHeight-6)
}

func (m *Model) resultKey(stroke string) tea.Cmd {
	r := m.result
	if r == nil {
		return nil
	}
	limit := max(0, len(wrapResultRows(r.Rows, resultBodyWidth(m.width)))-m.resultVisibleRows())
	switch stroke {
	case "f10", "ctrl+c":
		return tea.Quit
	case "esc", "q":
		m.result = nil
	case "enter":
		if m.canEditResultAnswers() && r.ActionIndex == 0 {
			m.editResultAnswers()
		} else {
			m.result = nil
		}
	case "e":
		m.editResultAnswers()
	case "tab", "shift+tab":
		if m.canEditResultAnswers() {
			r.ActionIndex = 1 - r.ActionIndex
		}
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
			r.Offset = min(max(0, len(wrapResultRows(r.Rows, resultBodyWidth(m.width)))-m.resultVisibleRows()), r.Offset+1)
		}
		return nil
	}
	dialogHeight := min(m.height-4, max(9, min(15, len(r.Rows)+6)))
	footerY := (m.height-dialogHeight)/2 + dialogHeight - 2
	if _, ok := msg.(tea.MouseClickMsg); ok && mouse.Button == tea.MouseLeft && (mouse.Y == footerY || mouse.Y == m.height-2) {
		if mouse.Y == footerY && m.canEditResultAnswers() && mouse.X < (m.width-70)/2+22 {
			m.editResultAnswers()
		} else {
			m.result = nil
		}
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
		return tea.NewView(navyCanvas(strings.Join(lines, "\n")))
	}

	// Match the approved demo's centered operation-result dialog: a darkened
	// navy overlay, gold double border, blue dialog surface, and status-colored
	// result text. Paint the entire viewport so no terminal-default black leaks
	// through around the dialog.
	width, height := m.width, m.height
	dialogWidth := min(70, width-8)
	if width >= 80 {
		dialogWidth = width - 8
	}
	bodyWidth := max(1, dialogWidth-4)
	visualRows := wrapResultRows(m.result.Rows, bodyWidth)
	dialogHeight := min(height-4, max(9, min(15, len(visualRows)+6)))
	x, y := (width-dialogWidth)/2, (height-dialogHeight)/2
	bodyRows := max(1, dialogHeight-6)
	r := m.result
	r.Offset = min(r.Offset, max(0, len(visualRows)-bodyRows))

	const (
		overlayBG = "\x1b[48;2;6;22;74m"
		dialogBG  = "\x1b[48;2;12;49;133m"
		goldFG    = "\x1b[38;2;255;223;134m"
		bodyFG    = "\x1b[38;2;233;245;255m"
		okFG      = "\x1b[38;2;217;246;228m"
		errFG     = "\x1b[38;2;255;220;200m"
	)
	statusFG := okFG
	if r.Failed {
		statusFG = errFG
	}
	// A terminal has no alpha compositing, so show the previous screen's
	// content in a muted foreground over the dark navy overlay as the closest
	// equivalent to the demo's translucent backdrop.
	result := m.result
	m.result = nil
	underlying := ansi.Strip(m.View().Content)
	m.result = result
	underRows := strings.Split(underlying, "\n")
	const mutedFG = "\x1b[38;2;82;103;143m"
	canvas := make([]string, height)
	for row := range canvas {
		text := ""
		if row < len(underRows) {
			text = fit(underRows[row], width)
		}
		canvas[row] = overlayBG + mutedFG + text
	}
	box := make([]string, dialogHeight)
	box[0] = "╔" + strings.Repeat("═", dialogWidth-2) + "╗"
	box[1] = "║" + fit(" Operation result", dialogWidth-2) + "║"
	box[2] = "╠" + strings.Repeat("═", dialogWidth-2) + "╣"
	for i := 0; i < bodyRows; i++ {
		text := ""
		if index := r.Offset + i; index < len(visualRows) {
			text = ansi.Cut(visualRows[index], r.Column, r.Column+dialogWidth-3)
		}
		rowFG := statusFG
		if i > 0 {
			rowFG = bodyFG
		}
		box[3+i] = "║" + fit(" "+text, dialogWidth-2) + "║"
		box[3+i] = rowFG + box[3+i]
	}
	box[dialogHeight-3] = "╠" + strings.Repeat("═", dialogWidth-2) + "╣"
	footer := fmt.Sprintf(" Back [Enter/Esc/click] · ↑↓ Scroll · ←→ Pan · %d-%d/%d", min(r.Offset+1, len(visualRows)), min(r.Offset+bodyRows, len(visualRows)), len(visualRows))
	if len(visualRows) > bodyRows {
		footer = strings.Replace(footer, "Back", "↑ above · ↓ below · Back", 1)
	}
	if m.canEditResultAnswers() {
		if r.ActionIndex == 0 {
			footer = "> Edit answers [Enter/E]   Back [Tab/ Esc] · ↑↓ Scroll · ←→ Pan"
		} else {
			footer = "  Edit answers [E]   > Back [Enter/Esc] · ↑↓ Scroll · ←→ Pan"
		}
	}
	box[dialogHeight-2] = "║" + fit(footer, dialogWidth-2) + "║"
	box[dialogHeight-1] = "╚" + strings.Repeat("═", dialogWidth-2) + "╝"
	for i, line := range box {
		canvas[y+i] = overlayBG + mutedFG + strings.Repeat(" ", x) + dialogBG + goldFG + line + overlayBG + mutedFG + strings.Repeat(" ", width-x-dialogWidth)
	}
	content := navySGR + strings.Join(canvas, "\x1b[m\n") + "\x1b[m"
	content = strings.ReplaceAll(content, "\x1b[m", overlayBG)
	content = strings.TrimSuffix(content, overlayBG) + "\x1b[m"
	v := tea.NewView(content)
	v.MouseMode = tea.MouseModeCellMotion
	return v
}
