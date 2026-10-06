package tui

import (
	"fmt"
	"strings"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

// agentManagementDetails presents discovery and configuration provenance without
// treating the presence of a config file as evidence that the agent was found.
func agentManagementDetails(row viewmodel.AgentManagementRow) []string {
	lines := []string{"Status: " + agentManagementStatus(row.Detection)}
	if row.Evidence != "" {
		lines = append(lines, "Evidence: "+row.Evidence)
	} else {
		lines = append(lines, "Evidence: No executable or application evidence reported")
	}
	if row.Home != "" {
		lines = append(lines, "Active home: "+row.Home)
	} else {
		lines = append(lines, "Active home: Not reported")
	}
	if row.EffectiveConfigPath != "" {
		lines = append(lines, "Effective config: "+row.EffectiveConfigPath)
	} else {
		lines = append(lines, "Effective config: Not resolved")
	}
	if row.WriteConfigPath != "" {
		lines = append(lines, "Intended write config: "+row.WriteConfigPath)
	} else {
		lines = append(lines, "Intended write config: Not supported or not resolved")
	}
	if row.Note != "" {
		lines = append(lines, "Resolution note: "+row.Note)
	}
	if len(row.ConfigFiles) == 0 {
		lines = append(lines, "Config candidates: None reported")
	} else {
		lines = append(lines, fmt.Sprintf("Config candidates: %d", len(row.ConfigFiles)))
		for i, file := range row.ConfigFiles {
			state := "missing"
			if file.Exists {
				state = "exists"
			}
			label := fmt.Sprintf("Candidate %d: %s (%s; %s; %s)", i+1, file.Path, file.Scope, file.Precedence, state)
			if file.Evidence != "" {
				label += " — " + file.Evidence
			}
			if file.Profile != "" {
				label += "; recorded for profile " + file.Profile
			}
			if file.Home != "" {
				label += "; home " + file.Home
			}
			lines = append(lines, label)
		}
	}
	if len(row.Registrations) == 0 {
		lines = append(lines, "Observed registrations: None reported")
	} else {
		lines = append(lines, "Observed registrations:")
		for _, registration := range row.Registrations {
			lines = append(lines, "  "+registration)
		}
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
