package tui

import (
	"errors"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

type resultState struct {
	Action      string
	Rows        []string
	Offset      int
	Column      int
	Failed      bool
	ActionIndex int
	Origin      string
	CanRetry    bool
	CanReturn   bool
}

func (m *Model) showOperationResult(msg operationMsg) {
	structured := viewmodel.OperationResult{}
	if msg.result != nil {
		structured = *msg.result
	}
	if structured.Step == "" {
		structured.Step = msg.step
	}
	if structured.Target == "" {
		structured.Target = msg.target
	}
	if msg.err != nil {
		msg.err = errors.New(m.cleanOutput(msg.err.Error()))
	}
	result := resultStateFromOperationOutcome(m.action, m.cleanOutput(msg.output), msg.err, structured, msg.result != nil)
	result.Failed = result.Failed || msg.failed
	result.Origin = msg.origin
	result.CanReturn = m.setupRetry != nil
	result.CanRetry = result.Failed && (m.setupRetry != nil || m.retryOperation != nil || m.action == "check connection")
	if msg.failed && msg.err == nil && len(structured.Errors) == 0 {
		if structured.Step != "" {
			result.Rows = append(result.Rows, "Failed step: "+structured.Step)
		}
		result.Rows = append(result.Rows, "Failed: operation reported an error")
	}
	m.output = strings.Join(result.Rows, "\n")
	m.result = result
}

func (m *Model) canEditResultAnswers() bool {
	return m.result != nil && m.result.CanReturn && m.result.Failed && m.setupRetry != nil
}

func (m *Model) returnToConfiguration() {
	if !m.canEditResultAnswers() {
		m.closeResult()
		return
	}
	draft := m.setupRetry
	m.result = nil
	m.pendingSetup = &draft.preview
	m.pendingSetupField = draft.destinationField
	if m.workspace != nil && m.workspace.Key == draft.preview.Key {
		m.workspace.cacheDraft(draft.values)
		if section := setupFailureSection(draft); section != "" {
			m.workspace.Section = section
		}
	}
	m.view = draft.origin
	m.openSetupFormWithValues(draft.preview, draft.values)
	if m.workspace != nil && m.workspace.Active && draft.section != "" {
		m.workspace.Section = setupFailureSection(draft)
		if m.form != nil {
			m.form.SelectSection(m.workspace.Section)
			m.form.FocusSection()
		}
	}
}

func setupFailureSection(draft *setupRetryDraft) string {
	if draft == nil {
		return ""
	}
	step := strings.ToLower(draft.step)
	detail := strings.ToLower(draft.failure)
	if strings.Contains(step+" "+detail, "auth") || strings.Contains(step+" "+detail, "credential") || strings.Contains(step+" "+detail, "token") {
		return "Authentication"
	}
	if strings.Contains(step+" "+detail, "connection") || strings.Contains(step+" "+detail, "endpoint") || strings.Contains(step+" "+detail, "url") {
		return "Connection"
	}
	return draft.section
}

func (m *Model) retryResult() tea.Cmd {
	if m.result == nil || !m.result.CanRetry {
		return nil
	}
	if m.setupRetry != nil {
		draft := *m.setupRetry
		draft.values = cloneSetupValues(draft.values)
		m.result = nil
		m.pendingSetup = &draft.preview
		m.pendingSetupField = draft.destinationField
		return m.applySetup(draft.values)
	}
	retry := m.retryOperation
	if m.action == "check connection" {
		if profile, ok := m.selectedContextProfile(); ok {
			m.result = nil
			return m.checkProfileConnection(profile)
		}
	}
	if retry == nil {
		return nil
	}
	m.result = nil
	return retry()
}

func (m *Model) closeResult() {
	if m.result == nil {
		return
	}
	if m.setupRetry != nil && m.workspace != nil && m.workspace.Key == m.setupRetry.preview.Key {
		m.workspace.cacheDraft(m.setupRetry.values)
		m.workspace.Active = false
	}
	m.view = m.result.Origin
	m.result = nil
	m.setupRetry = nil
	m.retryOperation = nil
	m.pendingSetup = nil
	m.pendingSetupField = ""
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
		return max(1, m.height-2)
	}
	visualRows := wrapResultRows(m.result.Rows, resultBodyWidth(m.width))
	dialogHeight := min(m.height-4, max(10, min(16, len(visualRows)+7)))
	return max(1, dialogHeight-7)
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
		m.closeResult()
	case "enter":
		return m.activateResultAction(r.ActionIndex)
	case "e":
		m.returnToConfiguration()
	case "r":
		return m.retryResult()
	case "tab", "shift+tab":
		count := resultActionCount(r)
		if stroke == "shift+tab" {
			r.ActionIndex = (r.ActionIndex + count - 1) % count
		} else {
			r.ActionIndex = (r.ActionIndex + 1) % count
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

func resultActionCount(result *resultState) int {
	if result == nil {
		return 1
	}
	return len(resultActionLabels(result, result.CanReturn))
}

func (m *Model) activateResultAction(index int) tea.Cmd {
	if m.result == nil {
		return nil
	}
	labels := resultActionLabels(m.result, m.result.CanReturn)
	if index < 0 || index >= len(labels) {
		return nil
	}
	switch labels[index] {
	case "Return to configuration":
		m.returnToConfiguration()
	case "Retry":
		return m.retryResult()
	default:
		m.closeResult()
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
	if _, ok := msg.(tea.MouseClickMsg); !ok || mouse.Button != tea.MouseLeft {
		return nil
	}
	width := min(70, m.width-8)
	if m.width >= 80 {
		width = m.width - 8
	}
	visualRows := wrapResultRows(r.Rows, resultBodyWidth(m.width))
	dialogHeight := min(m.height-4, max(10, min(16, len(visualRows)+7)))
	x := (m.width - width) / 2
	y := (m.height - dialogHeight) / 2
	footerY := y + dialogHeight - 2
	if mouse.Y == footerY {
		if action := resultActionAtX(r, m.canEditResultAnswers(), mouse.X-(x+1)); action >= 0 {
			return m.activateResultAction(action)
		}
	}
	if mouse.X < x || mouse.X >= x+width || mouse.Y < y || mouse.Y >= y+dialogHeight {
		m.closeResult()
	}
	return nil
}

func resultActionAtX(result *resultState, canReturn bool, x int) int {
	if x < 0 {
		return -1
	}
	labels := resultActionLabels(result, canReturn)
	position := 0
	for index, label := range labels {
		prefix := "  "
		if index == result.ActionIndex {
			prefix = "> "
		}
		width := ansi.StringWidth(prefix + label)
		if x >= position && x < position+width {
			return index
		}
		position += width
		if index+1 < len(labels) {
			position += ansi.StringWidth(" · ")
		}
	}
	return -1
}

func (m *Model) resultView() tea.View {
	if m.width < 80 || m.height < 16 {
		return m.smallResultView()
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
	dialogHeight := min(height-4, max(10, min(16, len(visualRows)+7)))
	x, y := (width-dialogWidth)/2, (height-dialogHeight)/2
	bodyRows := max(1, dialogHeight-7)
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
	box[1] = "║" + fit(resultHeader(r), dialogWidth-2) + "║"
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
	box[dialogHeight-4] = "╠" + strings.Repeat("═", dialogWidth-2) + "╣"
	aboveBelow := ""
	if len(visualRows) > bodyRows {
		aboveBelow = "↑ above · ↓ below · "
	}
	position := fmt.Sprintf("%s↑↓ scroll · %d-%d/%d", aboveBelow, min(r.Offset+1, len(visualRows)), min(r.Offset+bodyRows, len(visualRows)), len(visualRows))
	box[dialogHeight-3] = "║" + fit(position, dialogWidth-2) + "║"
	box[dialogHeight-2] = "║" + fit(resultActions(r, m.canEditResultAnswers()), dialogWidth-2) + "║"
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

func resultActionLabels(result *resultState, canReturn bool) []string {
	labels := []string{}
	if canReturn {
		labels = append(labels, "Return to configuration")
	}
	if result != nil && result.CanRetry {
		labels = append(labels, "Retry")
	}
	labels = append(labels, "Close / Back")
	return labels
}

func resultActions(result *resultState, canReturn bool) string {
	labels := resultActionLabels(result, canReturn)
	for index := range labels {
		if index == result.ActionIndex {
			labels[index] = "> " + labels[index]
		} else {
			labels[index] = "  " + labels[index]
		}
	}
	return strings.Join(labels, " · ")
}

func resultHeader(result *resultState) string {
	header := " Operation result · Tab select · Esc Close"
	if result != nil && result.CanReturn {
		header = " Operation result · Tab select · E Edit answers · Esc Close"
	}
	if result != nil && result.CanRetry {
		header = " Operation result · Tab select · R Retry · Esc Close"
	}
	if result != nil && result.CanReturn && result.CanRetry {
		header = " Operation result · Tab select · E Edit answers · R Retry · Esc Close"
	}
	return header
}

func (m *Model) smallResultView() tea.View {
	width, height := max(1, m.width), max(1, m.height)
	bodyWidth := max(1, width-2)
	rows := wrapResultRows(m.result.Rows, bodyWidth)
	bodyRows := max(1, height-2)
	limit := max(0, len(rows)-bodyRows)
	m.result.Offset = min(m.result.Offset, limit)
	controls := "Tab/Enter · Esc close"
	if m.result.CanReturn {
		controls += " · E edit"
	}
	if m.result.CanRetry {
		controls += " · R retry"
	}
	lines := []string{fit(fmt.Sprintf("Result · need 80×16 · %d×%d · %s", width, height, controls), width)}
	for index := 0; index < bodyRows; index++ {
		text := ""
		if row := m.result.Offset + index; row < len(rows) {
			text = rows[row]
		}
		lines = append(lines, fit(text, width))
	}
	footer := "Tab/Enter · Esc close"
	if m.result.CanReturn {
		footer += " · E edit"
	}
	if m.result.CanRetry {
		footer += " · R retry"
	}
	if m.result.CanReturn {
		footer = "Tab/Enter · E edit · Esc close"
	}
	if m.result.CanRetry {
		footer = "Tab/Enter · R retry · Esc close"
	}
	if m.result.CanReturn && m.result.CanRetry {
		footer = "Tab/Enter · E edit · R retry · Esc close"
	}
	if limit > 0 {
		footer = "↑↓ more · Tab/Enter · Esc close"
	}
	lines = append(lines, fit(footer, width))
	if len(lines) > height {
		lines = lines[:height]
	}
	return tea.NewView(navyCanvas(strings.Join(lines, "\n")))
}
