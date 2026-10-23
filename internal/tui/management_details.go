package tui

import (
	"strings"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

// agentManagementDetails keeps the Agents detail pane focused on AACT's own
// registrations. Detection and destination configuration are shown elsewhere.
func agentManagementDetails(row viewmodel.AgentManagementRow) []string {
	if len(row.Registrations) == 0 {
		return []string{"Registrations: None"}
	}
	lines := []string{"Registrations:"}
	for _, registration := range row.Registrations {
		lines = append(lines, "  "+registration)
	}
	return lines
}

func agentManagementStatus(detection string) string {
	switch strings.ToLower(strings.TrimSpace(detection)) {
	case "installed", "detected":
		return "Detected"
	case "not-detected", "not detected":
		return "Not detected"
	case "unsupported", "unsupported-here", "unsupported here":
		return "Unsupported here"
	default:
		return "Unverified"
	}
}

func wrapManagementDetails(lines []string, width int) []string {
	wrapped := make([]string, 0, len(lines))
	for _, line := range lines {
		wrapped = append(wrapped, splitDisplayLine(line, max(1, width))...)
	}
	return wrapped
}
