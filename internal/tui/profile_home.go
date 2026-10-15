package tui

import (
	"context"
	"fmt"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/agents"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/forms"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
	"strings"
)

type capabilityInstancesBackend interface {
	UICapabilityInstances(context.Context) (map[string]viewmodel.CapabilityInstanceSnapshot, error)
	UICreateInstance(context.Context, config.InstanceRef) error
}
type capabilityProfilesBackend interface {
	UICapabilityProfiles(context.Context) (map[string]viewmodel.CapabilityInstanceSnapshot, error)
	UICreateProfile(context.Context, config.InstanceRef) error
}

func capabilityInstanceRows(snapshot viewmodel.CapabilityInstanceSnapshot) []viewmodel.CapabilityInstance {
	if snapshot.Instances != nil {
		return snapshot.Instances
	}
	return snapshot.Profiles
}

type packSettingsBackend interface {
	PackSettings(context.Context) (map[string]string, error)
}
type instanceCreatedMsg struct {
	ref config.InstanceRef
	err error
}
type instanceRemovalBackend interface {
	UIRemoveInstance(context.Context, config.InstanceRef) (viewmodel.OperationResult, error)
}
type instanceRemovedMsg struct {
	ref config.InstanceRef
	err error
}

func (m *Model) instanceMode() bool {
	_, modern := m.backend.(capabilityInstancesBackend)
	_, legacy := m.backend.(capabilityProfilesBackend)
	return (modern || legacy) && (m.workspace == nil || m.workspace.InstanceRef.CapabilityID != "")
}
func instanceStatusSummary(p viewmodel.CapabilityInstance) string {
	skills, skillTotal, registered, registrationTotal := 0, 0, 0, 0
	for _, c := range p.Components {
		switch c.Kind {
		case "skill":
			skillTotal++
			if c.Status == "installed" {
				skills++
			}
		case "mcp":
			registrationTotal++
			if c.Status == "installed" {
				registered++
			}
		}
	}
	parts := []string{p.ConfigStatus}
	if skillTotal > 0 {
		parts = append(parts, fmt.Sprintf("skills %d/%d", skills, skillTotal))
	}
	if registrationTotal > 0 {
		parts = append(parts, fmt.Sprintf("registered %d/%d", registered, registrationTotal))
	}
	for _, r := range p.MCPs {
		status := r.MCPID + " " + r.Status
		if r.AuthenticationStatus == viewmodel.AuthenticationFailed {
			status += " AuthenticationFailed"
		} else if r.AuthenticationStatus == viewmodel.AuthenticationComplete {
			status += " AuthenticationComplete"
		}
		parts = append(parts, status)
	}
	return strings.Join(parts, " · ")
}

func instanceComponentStatusText(c viewmodel.ComponentStatus) string {
	status := c.Status
	if c.Kind == "mcp" && status == "installed" {
		status = "registered"
		if !c.Managed {
			status += " · unmanaged"
		}
	} else if status == "installed" && !c.Managed {
		status = "installed · unmanaged"
	}
	return agents.DisplayName(c.AgentID) + " · " + c.Kind + " · " + c.Name + " · " + status
}

func instanceConfigurationDetails(row InstanceRow) []string {
	profile := row.Configuration
	if profile == nil {
		return nil
	}
	rows := make([]string, 0, 1+len(profile.Components))
	if profile.ConfigError != "" {
		rows = append(rows, profile.ConfigError)
	}
	for _, component := range profile.Components {
		rows = append(rows, instanceComponentStatusText(component))
	}
	for _, runtime := range profile.MCPs {
		status := runtime.MCPID + " " + runtime.Status
		if runtime.AuthenticationStatus == viewmodel.AuthenticationFailed {
			status += " AuthenticationFailed"
		} else if runtime.AuthenticationStatus == viewmodel.AuthenticationComplete {
			status += " AuthenticationComplete"
		}
		rows = append(rows, status)
	}
	return rows
}

func (m *Model) openInstanceCreation(c CapabilityRow) {
	ref := config.InstanceRef{PackID: c.Source, CapabilityID: c.Package}
	m.creatingInstance = &ref
	m.workspace = nil
	m.pendingSetup = nil
	m.home.Modal = nil
	m.management.FormOverlay = true
	m.form = forms.NewForm(m.ctx, []catalog.Input{{Name: "name", Label: "Instance name", Type: "string", Required: true, Regex: "^[a-zA-Z0-9][a-zA-Z0-9_.-]*$"}}, nil)
	m.form.SetTitle("New instance · " + c.Name)
}
func (m *Model) packInstanceRequest(key state.Key) viewmodel.SetupRequest {
	return viewmodel.SetupRequest{Ref: config.InstanceRef{PackID: key.Source, CapabilityID: key.Package, Name: key.Target}}
}

func configurationRuntimeInstance(p viewmodel.CapabilityInstance, r viewmodel.RuntimeStatus, preview *viewmodel.SetupPreview) *viewmodel.Instance {
	key := p.Key
	if preview != nil && len(preview.MCPDefinitions) > 1 {
		for _, d := range preview.MCPDefinitions {
			if d.Name == r.MCPID {
				if d.EnabledInput != "" || d.TokenInput != "" || d.TokenFileInput != "" || d.TokenEnvInput != "" || d.TokenHeader != "" || d.TokenContainerEnv != "" || len(d.Args) > 0 || len(d.EnvInputs) > 0 || len(d.SecretEnvInputs) > 0 {
					key.MCPDefinition = d.Name
				} else {
					key.MCP = d.Name
				}
			}
		}
	}
	row := &viewmodel.Instance{Key: key, Name: r.MCPID, URL: r.URL, RuntimeStatus: r.Status, Ownership: r.Ownership, ObservedAt: r.ObservedAt, ObservationStale: r.Stale}
	row.CanStart = !r.Stale && r.Ownership == "local" && (r.Status == "stopped" || r.Status == "missing" || r.Status == "exited" || r.Status == "never-started")
	row.CanStop = !r.Stale && r.Ownership == "local" && r.Status == "running"
	for _, c := range p.Components {
		if c.Kind == "mcp" && c.Name == r.MCPID && c.Status == "installed" {
			row.RegisteredAgents = append(row.RegisteredAgents, c.AgentID)
		}
	}
	return row
}

type configurationInstanceRunBackend interface {
	UIConfigurationInstanceRun(context.Context, string, config.InstanceRef, string) (string, error)
}
type configurationProfileRunBackend interface {
	UIConfigurationProfileRun(context.Context, string, config.InstanceRef, string) (string, error)
}

type packAgentManagementBackend interface {
	UIPackAgentManagement(context.Context) ([]viewmodel.AgentManagementRow, error)
	UIPackAgentConfig(context.Context, string, string) (string, error)
}
