package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

// wrapResultRows converts logical command output into viewport-sized visual
// lines while keeping the original rows in resultState for raw inspection.
func wrapResultRows(rows []string, width int) []string {
	if width < 1 {
		return nil
	}
	var wrapped []string
	for _, row := range rows {
		row = ansi.Strip(strings.TrimSuffix(row, "\r"))
		parts := strings.Split(ansi.Wrap(row, width, " "), "\n")
		if len(parts) == 0 {
			parts = []string{""}
		}
		wrapped = append(wrapped, parts...)
	}
	return wrapped
}

// operationProgressRows shows only details supplied by the running operation.
// An empty step stays empty; the UI never guesses a pipeline stage.
func operationProgressRows(action, target, step string, width int) []string {
	var logical []string
	if action != "" {
		logical = append(logical, "Operation: "+action)
	}
	if target != "" {
		logical = append(logical, "Target: "+target)
	}
	if step != "" {
		logical = append(logical, "Current step: "+step)
	}
	return wrapResultRows(logical, width)
}

// resultStateFromOperation turns structured domain outcomes into truthful text.
// Changes and Errors are the service-reported effects; no success is inferred
// from a requested destination or from the operation name.
func resultStateFromOperation(action, output string, err error, operation viewmodel.OperationResult) *resultState {
	return resultStateFromOperationOutcome(action, output, err, operation, true)
}

func resultStateFromOperationOutcome(action, output string, err error, operation viewmodel.OperationResult, outcomeKnown bool) *resultState {
	var rows []string
	if action != "" {
		rows = append(rows, "Operation: "+action)
	}
	if operation.Target != "" {
		rows = append(rows, "Target: "+operation.Target)
	}
	if operation.Step != "" && (err != nil || len(operation.Errors) > 0) {
		rows = append(rows, "Failed step: "+operation.Step)
	}
	if outcomeKnown && operation.SavedApplicable {
		if operation.Saved {
			rows = append(rows, "Saved: yes")
		} else {
			rows = append(rows, "Saved: no")
		}
	}
	if outcomeKnown {
		if len(operation.Changes) == 0 {
			rows = append(rows, "Applied effects: none reported")
		} else {
			rows = append(rows, fmt.Sprintf("Applied effects: %d reported", len(operation.Changes)))
			for _, change := range operation.Changes {
				rows = append(rows, "Applied · "+operationEffectText(change))
			}
		}
	}
	for _, failure := range operation.Errors {
		rows = append(rows, "Failed · "+failure)
	}
	if err != nil {
		rows = append(rows, "Failed: "+err.Error())
	}
	if output != "" {
		rows = append(rows, output)
	}
	if len(rows) == 0 {
		rows = []string{"Completed"}
	}
	return &resultState{Action: action, Rows: rows, Failed: err != nil || len(operation.Errors) > 0}
}

func (m *Model) progressView() tea.View {
	width := max(1, m.width)
	height := max(1, m.height)
	target := m.pending.target
	if m.pendingSetup != nil {
		target = setupTargetLabel(m.pendingSetup.Key)
	} else if m.setupRetry != nil {
		target = setupTargetLabel(m.setupRetry.preview.Key)
	} else if m.workspace != nil && m.workspace.Active && m.action == "load setup" {
		target = setupTargetLabel(m.workspace.Key)
	} else if m.action == "check connection" {
		if profile, ok := m.selectedContextProfile(); ok {
			target = profile.URL
		}
	}
	var base tea.View
	if m.form != nil && m.workspace != nil && m.workspace.Active {
		base = m.setupOverlayView()
	} else if isManagementView(m.view) {
		base = m.managementView()
	} else {
		base = m.homeView()
	}
	if width < 12 || height < 12 {
		return base
	}
	panelWidth := min(78, width-8)
	innerWidth := max(1, panelWidth-4)
	contentHeight := max(1, min(16, height-8))
	header := operationProgressRows(m.action, target, m.progressStep, innerWidth)
	if contentHeight <= 4 {
		identity := "Operation: " + m.action
		if target != "" {
			identity += " · Target: " + target
		}
		header = wrapResultRows([]string{identity}, innerWidth)
		if m.progressStep != "" {
			header = append(header, wrapResultRows([]string{"Current step: " + m.progressStep}, innerWidth)...)
		}
	}
	elapsed := time.Duration(0)
	if !m.progressStarted.IsZero() {
		elapsed = time.Since(m.progressStarted)
	}
	spinner := []string{"|", "/", "-", "\\"}[m.progressFrame%4]
	if !m.progressStarted.IsZero() {
		quietSince := m.progressStarted
		quietLabel := "Waiting for child output"
		if !m.progressLastOutput.IsZero() {
			quietSince = m.progressLastOutput
			quietLabel = "Quiet"
		}
		header = append(header, fmt.Sprintf("%s  Elapsed: %s · %s: %s", spinner, elapsed.Truncate(time.Second), quietLabel, time.Since(quietSince).Truncate(time.Second)))
	} else {
		header = append(header, spinner+"  Working")
	}
	outputRows := wrapResultRows(strings.Split(strings.TrimSuffix(m.progressOutput, "\n"), "\n"), innerWidth)
	if len(header) > max(1, contentHeight-3) {
		header = header[:max(1, contentHeight-3)]
	}
	maxOutputRows := max(0, contentHeight-len(header)-3)
	if m.progressOutput != "" && maxOutputRows > 0 {
		maxOffset := max(0, len(outputRows)-maxOutputRows)
		offset := min(maxOffset, max(0, m.progressOffset))
		start := max(0, maxOffset-offset)
		end := min(len(outputRows), start+maxOutputRows)
		header = append(header, "Child output")
		if start > 0 {
			header = append(header, "↑ older output · use ↑/↓")
		}
		header = append(header, outputRows[start:end]...)
		if end < len(outputRows) {
			header = append(header, "↓ newer output · use ↑/↓")
		}
	} else {
		header = header[:min(len(header), contentHeight)]
	}
	if len(header) > contentHeight {
		header = header[:contentHeight]
	}
	const (
		dialogBG = "\x1b[48;2;12;49;133m"
		goldFG   = "\x1b[38;2;255;223;134m"
		bodyFG   = "\x1b[38;2;233;245;255m"
	)
	dialogHeight := min(height-4, max(8, min(20, len(header)+4)))
	bodyRows := max(1, dialogHeight-4)
	if len(header) > bodyRows {
		header = header[:bodyRows]
	}
	box := make([]string, dialogHeight)
	box[0] = "╔" + strings.Repeat("═", panelWidth-2) + "╗"
	box[1] = "║" + fit(" Operation in progress", panelWidth-2) + "║"
	box[2] = "╠" + strings.Repeat("═", panelWidth-2) + "╣"
	for index := 0; index < bodyRows; index++ {
		line := ""
		if index < len(header) {
			line = header[index]
		}
		box[3+index] = bodyFG + "║" + fit(" "+line, panelWidth-2) + "║"
	}
	box[dialogHeight-1] = "╚" + strings.Repeat("═", panelWidth-2) + "╝"
	baseRows := fixedPaletteRows(base.Content, width, height)
	startY := max(0, (height-dialogHeight)/2)
	startX := max(0, (width-panelWidth)/2)
	for index, row := range box {
		panel := dialogBG + goldFG + row
		baseRows[startY+index] = composeOverlayRow(baseRows[startY+index], panel, startX, panelWidth, width)
	}
	content := strings.Join(baseRows, navySGR+"\n") + "\x1b[m"
	v := tea.NewView(content)
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

func operationEffectText(effect state.Installation) string {
	parts := []string{}
	if effect.AgentID != "" {
		parts = append(parts, effect.AgentID)
	}
	if effect.Component != "" {
		parts = append(parts, effect.Component)
	}
	if effect.RegistrationName != "" {
		parts = append(parts, "registration "+effect.RegistrationName)
	}
	if effect.Destination != "" {
		parts = append(parts, "destination "+effect.Destination)
	}
	if effect.URL != "" {
		parts = append(parts, effect.URL)
	}
	if effect.Transport != "" {
		parts = append(parts, effect.Transport)
	}
	return strings.Join(parts, " · ")
}
