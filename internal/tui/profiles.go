package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/forms"
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
	choices := []catalog.Choice{}
	selected := []string{}
	for _, agent := range m.agents {
		if strings.EqualFold(agent, "all") {
			continue
		}
		choices = append(choices, catalog.Choice{Value: agent, Label: agent})
		for _, registered := range p.Profile.RegisteredAgents {
			if registered == agent {
				selected = append(selected, agent)
				break
			}
		}
	}
	// Retain registrations absent from the current agent inventory until explicitly deselected.
	for _, registered := range p.Profile.RegisteredAgents {
		if strings.EqualFold(registered, "all") {
			continue
		}
		found := false
		for _, choice := range choices {
			if choice.Value == registered {
				found = true
				break
			}
		}
		if !found {
			choices = append(choices, catalog.Choice{Value: registered, Label: registered + " (registered; unavailable)"})
			selected = append(selected, registered)
		}
	}
	if len(choices) == 0 {
		m.output = "No named agents are available"
		return
	}
	m.pendingRegistration = &viewmodel.RegistrationRequest{Key: p.Key, URL: p.URL, Transport: p.Profile.Transport}
	m.pendingRegistrationRemoval = false
	defs := []catalog.Input{{Name: "agent", Label: "Desired registrations (deselect to remove)", Type: "multichoice", Options: choices}}
	if p.Profile.Transport == "" {
		defs = append([]catalog.Input{{Name: "transport", Label: "Transport (select MCP protocol)", Type: "choice", Required: true, Options: []catalog.Choice{{Value: "streamable-http", Label: "Streamable HTTP"}, {Value: "sse", Label: "SSE"}}}}, defs...)
	}
	m.form = forms.NewForm(m.ctx, defs, map[string]any{"agent": selected})
	m.form.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
	// Keep the originating Actions selection so cancelling returns to the same item.
}
func (m *Model) removeRegistrationForm(p ProfileRow) {
	if reason := m.profileActionReason(p, "remove-registrations"); reason != "" {
		m.output = reason
		return
	}
	choices := make([]catalog.Choice, 0, len(p.Profile.RegisteredAgents))
	for _, agent := range p.Profile.RegisteredAgents {
		choices = append(choices, catalog.Choice{Value: agent, Label: agent})
	}
	m.pendingRegistration = &viewmodel.RegistrationRequest{
		Key: p.Key, URL: p.URL, Transport: p.Profile.Transport,
		AgentIDs: append([]string(nil), p.Profile.RegisteredAgents...),
	}
	m.pendingRegistrationRemoval = true
	m.form = forms.NewForm(m.ctx, []catalog.Input{{Name: "agent", Label: "Select registrations to remove", Type: "multichoice", Options: choices}}, nil)
	m.form.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
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
	rows := m.profiles()
	if m.home.Focus != ProfilesPane || len(rows) == 0 || rows[m.home.Profiles.Index].Profile == nil {
		return []string{m.selectedDetail(), "Enter / Esc Back"}
	}
	p := rows[m.home.Profiles.Index].Profile
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
