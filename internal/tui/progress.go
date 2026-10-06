package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
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
	if operation.Saved {
		rows = append(rows, "Saved: yes")
	} else {
		rows = append(rows, "Saved: no")
	}
	if len(operation.Changes) > 0 {
		rows = append(rows, fmt.Sprintf("Applied: %d recorded effect(s)", len(operation.Changes)))
		for _, change := range operation.Changes {
			rows = append(rows, fmt.Sprintf("Applied · %s · %s · %s", change.AgentID, change.Component, change.Destination))
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
