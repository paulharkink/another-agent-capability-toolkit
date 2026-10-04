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
	UIProfileSnapshot(context.Context) (viewmodel.ProfileSnapshot, error)
}
type registrationBackend interface {
	UIConfigureRegistrations(context.Context, viewmodel.RegistrationRequest) (viewmodel.OperationResult, error)
}
type connectionBackend interface {
	CheckConnection(context.Context, string, string) viewmodel.ConnectionObservation
}

func (m *Model) profileActionReason(p ProfileRow, action string) string {
	if m.profileError != nil {
		return "Profile refresh failed: " + m.profileError.Error()
	}
	if action == "parameters" {
		if _, ok := m.backend.(setupBackend); !ok {
			return "setup service unavailable"
		}
		c, ok := m.selectedCapability()
		if !ok || c.CatalogIndex < 0 {
			return "package is absent from the local catalog"
		}
		if p.Instance.Ownership != "local" || p.Status == "external" {
			return "runtime belongs to another installation"
		}
		return ""
	}
	if action == "check-connection" {
		if _, ok := m.backend.(connectionBackend); !ok {
			return "connection service support pending"
		}
		if p.URL == "" {
			return "No MCP endpoint is configured"
		}
		return ""
	}
	if action == "registrations" || action == "remove-registrations" {
		if _, ok := m.backend.(registrationBackend); !ok || p.Profile == nil {
			return "registration-only service support pending"
		}
		if action == "remove-registrations" {
			if len(p.Profile.RegisteredAgents) == 0 {
				return "No local registrations to remove"
			}
			return ""
		}
		if !p.Profile.CanConfigureRegistrations {
			if p.Profile.RegistrationDisabledReason != "" {
				return p.Profile.RegistrationDisabledReason
			}
			return "No MCP endpoint is configured"
		}
		if len(m.agents) == 0 && len(p.Profile.RegisteredAgents) == 0 {
			return "No named agents are available"
		}
		return ""
	}
	if p.Profile != nil {
		if action == "s" {
			if p.Profile.CanStart {
				return ""
			}
			if p.Profile.StartDisabledReason != "" {
				return p.Profile.StartDisabledReason
			}
			return "Start is unavailable"
		}
		if action == "x" {
			if p.Profile.CanStop {
				return ""
			}
			if p.Profile.StopDisabledReason != "" {
				return p.Profile.StopDisabledReason
			}
			return "Stop is unavailable"
		}
	}
	if p.Instance.Ownership != "local" || p.Status == "external" {
		return "Runtime action unavailable: this profile is not locally owned."
	}
	return ""
}
func (m *Model) checkProfileConnection(p ProfileRow) tea.Cmd {
	if reason := m.profileActionReason(p, "check-connection"); reason != "" {
		m.output = reason
		return nil
	}
	backend := m.backend.(connectionBackend)
	m.busy = true
	m.action = "check connection"
	m.home.Modal = nil
	origin := m.view
	url, transport := p.URL, ""
	if p.Profile != nil {
		transport = p.Profile.Transport
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
		return operationMsg{origin: origin, output: output}
	}
}
func (m *Model) registrationForm(p ProfileRow) {
	if reason := m.profileActionReason(p, "registrations"); reason != "" {
		m.output = reason
		return
	}
	m.openRegistrationOverlay(p, false)
}
func (m *Model) removeRegistrationForm(p ProfileRow) {
	if reason := m.profileActionReason(p, "remove-registrations"); reason != "" {
		m.output = reason
		return
	}
	m.openRegistrationOverlay(p, true)
}
func (m *Model) configureRegistrations(request viewmodel.RegistrationRequest, removing bool) tea.Cmd {
	backend, ok := m.backend.(registrationBackend)
	if !ok {
		m.output = "Registration service support pending"
		return nil
	}
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
		return operationMsg{origin: origin, output: strings.Join(lines, "\n"), err: err}
	}
}

func (m *Model) profileDetails() []string {
	row, ok := m.selectedContextProfile()
	if m.home.Focus != ProfilesPane || !ok || row.Profile == nil {
		return []string{m.selectedDetail(), "Enter / Esc Back"}
	}
	p := row.Profile
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
