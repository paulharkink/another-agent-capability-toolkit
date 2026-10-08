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
	values := draft.values
	if draft.returnValues != nil {
		values = draft.returnValues
	}
	m.result = nil
	m.pendingSetup = &draft.preview
	m.pendingSetupField = draft.destinationField
	if m.workspace != nil && m.workspace.Key == draft.preview.Key {
		m.workspace.cacheDraft(values)
		if section := setupFailureSection(draft); section != "" {
			m.workspace.Section = section
		}
	}
	m.view = draft.origin
	m.openSetupFormWithValues(draft.preview, values)
	if m.form != nil {
		for _, name := range draft.resetInputs {
			for _, input := range draft.preview.Inputs {
				if input.Definition.Name == name {
					m.form.SetResetValue(name, input.InheritedValue, input.HasInheritedValue)
					origin := input.Provenance
					if origin == "" {
						origin = "unset"
					}
					label := input.Definition.Label
					if label == "" {
						label = name
					}
					m.form.SetResetPresentation(name, label+" ["+origin+"]", setupProvenanceHint(origin, input.ProvenancePath))
					m.form.SetOverridePresentation(name, label+" [unsaved override]", "Unsaved override · Will save as an override · Ctrl+R restore inherited value")
					m.form.MarkResetField(name)
					break
				}
			}
		}
	}
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
		return m.applySetupWithReset(draft.values, draft.resetInputs)
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
	if m.unsavedExitFailure {
		m.unsavedExitFailure = false
		m.view = m.result.Origin
		m.result = nil
		return
	}
	if m.setupRetry != nil && m.workspace != nil && m.workspace.Key == m.setupRetry.preview.Key {
		m.workspace.cacheDraft(m.setupRetry.values)
		m.workspace.Active = false
	}
	if m.form == nil {
		m.pendingDefaultAgents = false
	}
	m.view = m.result.Origin
	m.result = nil
	m.setupRetry = nil
	m.retryOperation = nil
	retainWorkspaceSetup := m.workspace != nil && m.workspace.Active && m.workspace.Preview != nil && m.pendingSetup != nil && m.workspace.Key == m.pendingSetup.Key
	if !retainWorkspaceSetup {
		m.pendingSetup = nil
		m.pendingSetupField = ""
	}
}

func (m *Model) resultFrameBounds() (x, y, width, height int) {
	if m.result != nil && m.result.CanReturn {
		if x, y, width, height, ok := m.setupOverlayBounds(); ok {
			return x, y, width, height
		}
		return 4, 4, max(1, m.width-8), max(1, m.height-6)
	}
	return 0, 0, m.width, m.height
}

func (m *Model) resultBodyWidth() int {
	_, _, frameWidth, _ := m.resultFrameBounds()
	if m.result != nil && m.result.CanReturn {
		return max(1, min(78, frameWidth-2)-4)
	}
	return max(1, min(78, m.width-8)-4)
}

func (m *Model) resultLayout(rowCount int) (x, y, dialogWidth, dialogHeight int) {
	frameX, frameY, frameWidth, frameHeight := m.resultFrameBounds()
	maxWidth := min(78, m.width-8)
	maxHeight := m.height - 4
	if m.result != nil && m.result.CanReturn {
		// Leave a visible inset between the result panel and its setup frame.
		maxWidth = min(78, max(1, frameWidth-2))
		maxHeight = max(1, frameHeight-2)
	}
	dialogWidth = max(1, min(maxWidth, frameWidth))
	dialogHeight = max(1, min(maxHeight, max(10, min(16, rowCount+7))))
	return frameX + (frameWidth-dialogWidth)/2, frameY + (frameHeight-dialogHeight)/2, dialogWidth, dialogHeight
}

func (m *Model) resultVisibleRows() int {
	if m.height < 16 {
		return max(1, m.height-2)
	}
	visualRows := wrapResultRows(m.result.Rows, m.resultBodyWidth())
	_, _, _, dialogHeight := m.resultLayout(len(visualRows))
	return max(1, dialogHeight-7)
}

func (m *Model) resultKey(stroke string) tea.Cmd {
	r := m.result
	if r == nil {
		return nil
	}
	limit := max(0, len(wrapResultRows(r.Rows, m.resultBodyWidth()))-m.resultVisibleRows())
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
			r.Offset = min(max(0, len(wrapResultRows(r.Rows, m.resultBodyWidth()))-m.resultVisibleRows()), r.Offset+1)
		}
		return nil
	}
	if _, ok := msg.(tea.MouseClickMsg); !ok || mouse.Button != tea.MouseLeft {
		return nil
	}
	visualRows := wrapResultRows(r.Rows, m.resultBodyWidth())
	x, y, width, dialogHeight := m.resultLayout(len(visualRows))
	footerY := y + dialogHeight - 2
	if mouse.Y == footerY && mouse.X >= x+1 && mouse.X < x+width-1 {
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

	// Keep the underlying screen's palette fixed while drawing the result as a
	// separate foreground layer. Terminals do not alpha-composite, so dimming
	// the whole canvas changes the parent screen's palette and focus colors.
	r := m.result
	width, height := m.width, m.height
	visualRows := wrapResultRows(r.Rows, m.resultBodyWidth())
	// Keep setup recovery inside the same inset occupied by its editor.
	x, y, dialogWidth, dialogHeight := m.resultLayout(len(visualRows))
	bodyRows := max(1, dialogHeight-7)
	r.Offset = min(r.Offset, max(0, len(visualRows)-bodyRows))

	const (
		dialogBG = "\x1b[48;2;12;49;133m"
		goldFG   = "\x1b[38;2;255;223;134m"
		bodyFG   = "\x1b[38;2;233;245;255m"
		okFG     = "\x1b[38;2;217;246;228m"
		errFG    = "\x1b[1;38;2;255;77;95m"
	)
	statusFG := okFG
	if r.Failed {
		statusFG = errFG
	}
	result := m.result
	m.result = nil
	// Recover the rendered parent with ANSI colors intact. The result is drawn
	// over it without darkening or muting any content outside the dialog.
	underlying := m.View().Content
	m.result = result
	canvas := fixedPaletteRows(underlying, width, height)
	box := make([]string, dialogHeight)
	box[0] = "╔" + strings.Repeat("═", dialogWidth-2) + "╗"
	statusLabel := "\x1b[1;38;2;119;255;174mSUCCESS\x1b[m · "
	if r.Failed {
		statusLabel = "\x1b[1;38;2;255;77;95mFAILED\x1b[m · "
	}
	box[1] = "║" + fit(statusLabel+resultHeader(r), dialogWidth-2) + "║"
	box[2] = "╠" + strings.Repeat("═", dialogWidth-2) + "╣"
	for i := 0; i < bodyRows; i++ {
		text := ""
		if index := r.Offset + i; index < len(visualRows) {
			text = ansi.Cut(visualRows[index], r.Column, r.Column+dialogWidth-4)
		}
		rowFG := bodyFG
		if strings.Contains(text, "Failed") {
			rowFG = errFG
		} else if i == 0 {
			rowFG = statusFG
		}
		bar := " "
		if len(visualRows) > bodyRows {
			bar = resultScrollbar(r.Offset, len(visualRows), bodyRows, i)
		}
		box[3+i] = "║" + fit(" "+text, dialogWidth-3) + bar + "║"
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
		// Restore the dialog's fixed blue surface and readable body foreground
		// after inline status colors reset their SGR state.
		line = strings.NewReplacer("\x1b[m", dialogBG+bodyFG, "\x1b[0m", dialogBG+bodyFG).Replace(line)
		canvas[y+i] = composeOverlayRow(canvas[y+i], dialogBG+goldFG+line, x, dialogWidth, width)
	}
	content := strings.Join(canvas, navySGR+"\n") + "\x1b[m"
	v := tea.NewView(content)
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

func resultScrollbar(offset, total, visible, row int) string {
	if total <= visible || visible < 1 {
		return " "
	}
	thumb := max(1, visible*visible/total)
	start := 0
	if total > visible {
		start = offset * (visible - thumb) / (total - visible)
	}
	if row >= start && row < start+thumb {
		return "█"
	}
	return "│"
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
	header := " Operation result · Esc Close"
	if result != nil && result.CanReturn {
		header = " Operation result · E Edit answers · Esc Close"
	}
	if result != nil && result.CanRetry {
		header = " Operation result · R Retry · Esc Close"
	}
	if result != nil && result.CanReturn && result.CanRetry {
		header = " Operation result · E Edit answers · R Retry · Esc Close"
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
	status := "SUCCESS"
	if m.result.Failed {
		status = "FAILED"
	}
	lines := []string{fit(fmt.Sprintf("Result · %s · need 80×16 · %d×%d · %s", status, width, height, controls), width)}
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
