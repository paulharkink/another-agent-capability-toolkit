package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

// Optional extensions keep the original Backend compatible with older callers.
type profileSnapshotBackend interface {
	UIProfileSnapshot(context.Context) (viewmodel.InstanceSnapshot, error)
}
type instanceSnapshotBackend interface {
	UIInstanceSnapshot(context.Context) (viewmodel.InstanceSnapshot, error)
}
type registrationBackend interface {
	UIConfigureRegistrations(context.Context, viewmodel.RegistrationRequest) (viewmodel.OperationResult, error)
}
type connectionBackend interface {
	CheckConnection(context.Context, string, string) viewmodel.ConnectionObservation
}

func (m *Model) instanceActionReason(p InstanceRow, action string) string {
	owner, status := "unknown", p.Status
	if p.Instance != nil {
		owner, status = p.Instance.Ownership, p.Instance.RuntimeStatus
	}
	if m.instanceError != nil {
		return "Instance refresh failed: " + m.instanceError.Error()
	}
	if action == "parameters" {
		if _, ok := m.backend.(setupBackend); !ok {
			return "The active AACT backend does not support setup operations."
		}
		c, ok := m.selectedCapability()
		if !ok || c.CatalogIndex < 0 {
			return "package is absent from the local catalog"
		}
		return ""
	}
	if action == "check-connection" {
		if _, ok := m.backend.(connectionBackend); !ok {
			return "The active AACT backend does not provide connection checks."
		}
		if p.URL == "" {
			return "No MCP endpoint is configured"
		}
		return ""
	}
	if owner == "other-aact" && (action == "s" || action == "x") {
		return runtimeManagedElsewhereReason
	}
	if action == "registrations" || action == "remove-registrations" {
		if _, ok := m.backend.(registrationBackend); !ok || p.Instance == nil {
			return "The active AACT backend does not support registration-only changes."
		}
		if action == "remove-registrations" {
			if len(p.Instance.RegisteredAgents) == 0 {
				return "No local registrations to remove"
			}
			return ""
		}
		if !p.Instance.CanConfigureRegistrations {
			if p.Instance.RegistrationDisabledReason != "" {
				return p.Instance.RegistrationDisabledReason
			}
			return "No MCP endpoint is configured"
		}
		if len(m.agents) == 0 && len(p.Instance.RegisteredAgents) == 0 {
			return "No named agents are available"
		}
		return ""
	}
	if action == "l" || action == "logs" {
		if owner != "local" {
			return "Logs are available only for locally owned runtimes"
		}
		return ""
	}
	if action == "s" {
		if p.Instance != nil && p.Instance.CanStart && p.Instance.Ownership == "local" && !p.Instance.ObservationStale {
			return ""
		}
		if p.Instance != nil && strings.TrimSpace(p.Instance.StartDisabledReason) != "" {
			return p.Instance.StartDisabledReason
		}
		return runtimeStartBlocker(p.Instance, "")
	}
	if action == "x" {
		if p.Instance != nil && p.Instance.CanStop && p.Instance.Ownership == "local" && !p.Instance.ObservationStale {
			return ""
		}
		if p.Instance != nil && strings.TrimSpace(p.Instance.StopDisabledReason) != "" {
			return p.Instance.StopDisabledReason
		}
		return runtimeStopBlocker(p.Instance, "")
	}
	if owner == "other-aact" {
		return "Runtime is owned by another AACT installation and is not locally owned."
	}
	if owner != "local" || status == "external" {
		return "Runtime status for this MCP has not been observed; refresh this instance to check its owner and state."
	}
	return ""
}
func (m *Model) checkProfileConnection(p InstanceRow) tea.Cmd {
	if reason := m.instanceActionReason(p, "check-connection"); reason != "" {
		m.output = reason
		return nil
	}
	backend := m.backend.(connectionBackend)
	m.retryOperation = func() tea.Cmd { return m.checkProfileConnection(p) }
	m.busy = true
	m.action = "check connection"
	m.home.Modal = nil
	origin := m.view
	url, transport := p.URL, ""
	if p.Instance != nil {
		transport = p.Instance.Transport
	}
	return func() tea.Msg {
		observation := backend.CheckConnection(m.ctx, url, transport)
		status := "unreachable"
		if observation.Reachable {
			status = "reachable"
		}
		output := fmt.Sprintf("%s: %s · checked %s", url, status, observation.CheckedAt.Format("2006-01-02 15:04:05 MST"))
		if observation.Error != "" {
			output += "\n" + observation.Error
		}
		result := viewmodel.OperationResult{Target: url, Step: "connection", Connection: observation}
		if observation.Error != "" {
			result.Errors = []string{observation.Error}
		}
		return operationMsg{origin: origin, output: output, failed: !observation.Reachable, step: result.Step, target: result.Target, result: &result}
	}
}
func (m *Model) registrationForm(p InstanceRow) {
	if reason := m.instanceActionReason(p, "registrations"); reason != "" {
		m.output = reason
		return
	}
	m.openRegistrationOverlay(p, false)
}
func (m *Model) removeRegistrationForm(p InstanceRow) {
	if reason := m.instanceActionReason(p, "remove-registrations"); reason != "" {
		m.output = reason
		return
	}
	m.openRegistrationOverlay(p, true)
}
func (m *Model) configureRegistrations(request viewmodel.RegistrationRequest, removing bool) tea.Cmd {
	backend, ok := m.backend.(registrationBackend)
	if !ok {
		m.output = "The active AACT backend does not support registration-only changes"
		return nil
	}
	m.retryOperation = func() tea.Cmd { return m.configureRegistrations(request, removing) }
	m.busy = true
	m.action = "configure registrations"
	if removing {
		m.action = "remove registrations"
	}
	m.home.Modal = nil
	origin := m.view
	return func() tea.Msg {
		result, err := backend.UIConfigureRegistrations(m.ctx, request)
		lines := []string{}
		if result.Connection.Error != "" {
			lines = append(lines, "Connection warning: "+result.Connection.Error)
		} else if result.Connection.Reachable {
			lines = append(lines, "MCP endpoint reachable")
		} else if !result.Connection.CheckedAt.IsZero() {
			lines = append(lines, "Connection warning: MCP endpoint is unreachable")
		}
		lines = append(lines, result.Message)
		for _, change := range result.Changes {
			verb := "configured"
			if removing {
				verb = "removed"
			}
			lines = append(lines, fmt.Sprintf("%s: %s registration %s", change.AgentID, change.Component, verb))
		}
		lines = append(lines, result.Errors...)
		structured := result
		return operationMsg{origin: origin, output: strings.Join(lines, "\n"), err: err, failed: len(result.Errors) > 0, step: result.Step, target: result.Target, result: &structured}
	}
}

func (m *Model) instanceDetails() []string {
	row, ok := m.selectedContextProfile()
	if m.home.Focus != ProfilesPane || !ok || row.Instance == nil {
		return []string{m.selectedDetail(), "Enter / Esc Back"}
	}
	p := row.Instance
	lines := []string{p.Key.Source + " / " + p.Key.Package, p.Key.Environment + " / " + p.Key.Target, "Runtime: " + p.RuntimeStatus + " · owner: " + p.Ownership, p.URL}
	if p.LocalLastAction != "" {
		lines = append(lines, "Local last action: "+p.LocalLastAction+" · "+p.LocalLastActionAt.Format("2006-01-02 15:04:05"))
	}
	if !p.ObservedAt.IsZero() {
		lines = append(lines, "Runtime observed: "+p.ObservedAt.Format("2006-01-02 15:04:05"))
	}
	if p.RuntimeStatus == "conflict" {
		reason := p.RegistrationDisabledReason
		if reason == "" {
			reason = p.StartDisabledReason
		}
		if reason != "" {
			lines = append(lines, reason)
		}
	}
	return append(lines, "Enter / Esc Back")
}
