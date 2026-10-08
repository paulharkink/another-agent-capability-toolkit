package tui

import (
	"context"
	"fmt"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/forms"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
	"strings"
)

type capabilityProfilesBackend interface {
	UICapabilityProfiles(context.Context) (map[string]viewmodel.CapabilityProfileSnapshot, error)
	UICreateProfile(context.Context, config.ProfileRef) error
}
type packSettingsBackend interface {
	PackSettings(context.Context) (map[string]string, error)
}
type profileCreatedMsg struct {
	ref config.ProfileRef
	err error
}

func (m *Model) profileMode() bool {
	_, ok := m.backend.(capabilityProfilesBackend)
	return ok && (m.workspace == nil || m.workspace.Reference.CapabilityID != "")
}
func profileStatusSummary(p viewmodel.CapabilityProfile) string {
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
		parts = append(parts, r.MCPID+" "+r.Status)
	}
	return strings.Join(parts, " · ")
}
func (m *Model) openProfileCreation(c CapabilityRow) {
	ref := config.ProfileRef{PackID: c.Source, CapabilityID: c.Package}
	m.creatingProfile = &ref
	m.workspace = nil
	m.pendingSetup = nil
	m.home.Modal = nil
	m.form = forms.NewForm(m.ctx, []catalog.Input{{Name: "name", Label: "Profile name", Type: "string", Required: true, Regex: "^[a-zA-Z0-9][a-zA-Z0-9_.-]*$"}}, nil)
	m.form.SetTitle("Create another profile · " + c.Name)
}
func (m *Model) packProfileRequest(key state.Key) viewmodel.SetupRequest {
	return viewmodel.SetupRequest{Ref: config.ProfileRef{PackID: key.Source, CapabilityID: key.Package, Name: key.Target}}
}

func configurationRuntimeProfile(p viewmodel.CapabilityProfile, r viewmodel.RuntimeStatus, preview *viewmodel.SetupPreview) *viewmodel.Profile {
	key := p.Key
	if preview != nil && len(preview.MCPDefinitions) > 1 {
		for _, d := range preview.MCPDefinitions {
			if d.Name == r.MCPID {
				if d.EnabledInput != "" || d.TokenInput != "" || d.TokenFileInput != "" || d.TokenEnvInput != "" || d.TokenHeader != "" || d.TokenContainerEnv != "" || len(d.Args) > 0 || len(d.EnvInputs) > 0 || len(d.SecretEnvInputs) > 0 {
					key.Profile = d.Name
				} else {
					key.MCP = d.Name
				}
			}
		}
	}
	row := &viewmodel.Profile{Key: key, Name: r.MCPID, URL: r.URL, RuntimeStatus: r.Status, Ownership: r.Ownership, ObservedAt: r.ObservedAt, ObservationStale: r.Stale}
	row.CanStart = !r.Stale && r.Ownership == "local" && (r.Status == "stopped" || r.Status == "missing" || r.Status == "exited" || r.Status == "never-started")
	row.CanStop = !r.Stale && r.Ownership == "local" && r.Status == "running"
	for _, c := range p.Components {
		if c.Kind == "mcp" && c.Name == r.MCPID && c.Status == "installed" {
			row.RegisteredAgents = append(row.RegisteredAgents, c.AgentID)
		}
	}
	return row
}

type configurationProfileRunBackend interface {
	UIConfigurationProfileRun(context.Context, string, config.ProfileRef, string) (string, error)
}
