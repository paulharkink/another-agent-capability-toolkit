package tui

import (
	"fmt"
	"strings"

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
	lines := []string{"Operation in progress"}
	lines = append(lines, operationProgressRows(m.action, target, "", max(1, width-8))...)
	if len(lines) > height {
		lines = lines[:height]
	}
	canvas := make([]string, height)
	for i := range canvas {
		canvas[i] = strings.Repeat(" ", width)
	}
	startY := max(0, (height-len(lines))/2)
	for index, line := range lines {
		line = fit(line, max(1, width-4))
		startX := max(0, (width-ansi.StringWidth(line))/2)
		canvas[startY+index] = strings.Repeat(" ", startX) + line + strings.Repeat(" ", max(0, width-startX-ansi.StringWidth(line)))
	}
	return tea.NewView(navyCanvas(strings.Join(canvas, "\n")))
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
