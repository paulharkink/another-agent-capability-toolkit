package tui

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/app"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/forms"
	mcpruntime "github.com/paulharkink/another-agent-capability-toolkit/internal/mcp"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

// workspaceDestinations keeps each destination identity intact for selection
// and achievement reconciliation.
func workspaceDestinations(preview viewmodel.SetupPreview) []viewmodel.SetupDestination {
	return preview.Destinations
}

func destinationDisplayPath(preview viewmodel.SetupPreview, destination viewmodel.SetupDestination) string {
	if !preview.MCP {
		return firstNonempty(destination.Path, destination.SkillsPath)
	}
	configPath := firstNonempty(destination.ConfigPath, destination.Path)
	skillsPath := destination.SkillsPath
	if skillsPath == "" || filepath.Clean(skillsPath) == filepath.Clean(configPath) {
		return configPath
	}
	return "skills: " + skillsPath + " · config: " + configPath
}

func firstNonempty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

const (
	sectionInputsID      = "tool:inputs"
	sectionOverviewID    = "tool:overview"
	sectionAgentsID      = "tool:agent-access"
	sectionEndpointID    = "tool:endpoint"
	sectionRuntimeID     = "tool:runtime"
	sectionLogsID        = "tool:logs"
	sectionInformationID = "tool:information"
)

// workspaceState is the cached editor context for one exact target. It keeps
// the invoking layer and profile observation beside the target draft so later
// route handling can restore Back without losing the parent selection.
type workspaceState struct {
	InstanceRef          config.InstanceRef
	Key                  state.Key
	SelectedMCP          string
	SelectedRuntimeID    string
	RuntimeRecordTargets []runtimeRecordTarget
	Section              string
	SectionID            string
	InvokingView         string
	InvokingSelection    int
	Active               bool
	Existing             bool
	Installed            bool
	RegisteredMCPs       map[string]bool
	InputsSaved          bool
	SaveNotice           string
	AgentActionsKnown    bool
	AgentActionsDisabled bool
	ObservedOnly         bool
	Preview              *viewmodel.SetupPreview
	Instance             *viewmodel.Instance
	InstanceSnapshot     *viewmodel.InstanceSnapshot
	Draft                map[string]any
}

type runtimeRecordTarget struct {
	Key       state.Key
	ID        string
	Ownership string
}

const runtimeManagedElsewhereReason = "Runtime belongs to another installation (another AACT installation); manage it there."

func (m *Model) workspaceForeignRuntimeReason(mcpName string) string {
	if m.workspace == nil {
		return ""
	}
	parent := m.workspaceObservedForeignCapability()
	if parent == nil {
		return ""
	}
	return firstNonempty(parent.ManagementReason, parent.ConfigError, runtimeManagedElsewhereReason)
}

func observedForeignRunningRuntime(ownership, status string) bool {
	return ownership == "other-aact" && strings.EqualFold(strings.TrimSpace(status), "running")
}

func (m *Model) workspaceObservedForeignCapability() *viewmodel.CapabilityInstance {
	if m.workspace == nil {
		return nil
	}
	snapshot, ok := m.capabilityInstances[m.workspace.Key.Package]
	if !ok {
		return nil
	}
	for _, instance := range capabilityInstances(snapshot) {
		if capabilityInstanceKey(instance) != m.workspace.Key || instance.Origin != "observed" || instance.ConfigStatus != "read-only" || instance.Ownership != "other-aact" {
			continue
		}
		copy := instance
		return &copy
	}
	return nil
}

func (m *Model) workspaceInputsReadOnly() bool {
	return m.workspaceForeignRuntimeReason("") != ""
}

type runtimeRecordRemovalBackend interface {
	UIRemoveRuntimeRecord(context.Context, state.Key, string) (string, error)
}

func (m *Model) refreshObservedWorkspaceFacts() {
	if m.workspace == nil || !m.workspace.ObservedOnly || m.workspace.Instance == nil || m.form == nil {
		return
	}
	profile := m.workspace.Instance
	key := m.workspace.Key
	registered := "none observed"
	if len(profile.RegisteredAgents) > 0 {
		registered = strings.Join(profile.RegisteredAgents, ", ")
	}
	m.form.SetSectionContentID(sectionOverviewID, []string{
		"Package configuration unavailable: this package is absent from the local catalog.",
		"Capability Pack: " + key.Source + " · Capability: " + key.Package,
		"Runtime: " + nonempty(profile.RuntimeStatus, "unknown") + " · Ownership: " + nonempty(profile.Ownership, "unknown"),
		"Locate the Capability Pack directory to edit capability settings and manage complete agent bindings.",
	})
	m.form.SetSectionContentID(sectionAgentsID, []string{
		"Observed registrations: " + registered,
		"Complete agent bindings require the Capability Pack catalog. Locate the pack directory to manage them.",
	})
	m.form.SetSectionContentID(sectionEndpointID, []string{
		"Endpoint: " + nonempty(profile.URL, "not configured"),
		"Transport: " + nonempty(profile.Transport, "unknown"),
		"Connection checks observe this endpoint and do not change its configuration.",
	})
	checkDisabled := ""
	if profile.URL == "" {
		checkDisabled = "No MCP endpoint is configured"
	} else if _, ok := m.backend.(connectionBackend); !ok {
		checkDisabled = "The active AACT backend does not provide connection checks"
	}
	m.form.SetSectionActionsID(sectionEndpointID, forms.FormAction{ID: "check-connection", Label: "Check connection", Disabled: checkDisabled})
	m.form.SetSectionContentID(sectionInformationID, []string{
		"Observed target: Capability Pack " + key.Source + " · Capability " + key.Package + " · Environment " + nonempty(key.Environment, "(none)") + " · Target " + nonempty(key.Target, "default"),
		"Observed endpoint: " + nonempty(profile.URL, "unavailable"),
		"Observed transport: " + nonempty(profile.Transport, "unknown"),
		"Runtime status: " + nonempty(profile.RuntimeStatus, "unknown"),
		"Runtime ownership: " + nonempty(profile.Ownership, "unknown"),
		"This workspace reflects observed instance facts; it does not imply package installation or local runtime ownership.",
	})
	m.form.SetSectionActionsID(sectionOverviewID, forms.FormAction{ID: "locate-source", Label: "Locate Capability Pack"})
	m.form.SetSectionActionsID(sectionAgentsID, forms.FormAction{ID: "locate-source", Label: "Locate Capability Pack"})
}

func (s *workspaceState) cacheDraft(draft map[string]any) { s.Draft = cloneSetupValues(draft) }
func (s workspaceState) cachedDraft() map[string]any      { return cloneSetupValues(s.Draft) }

func workspaceInformationLines(preview viewmodel.SetupPreview, width int) []string {
	inner := max(20, width)
	name := preview.PackageName
	if name == "" {
		name = preview.Key.Package
	}
	lines := []string{
		"Capability information",
		fmt.Sprintf("Capability: %s · package %s", name, preview.Key.Package),
		fmt.Sprintf("Capability Pack: %s · directory %s", preview.Key.Source, preview.SourceRoot),
	}
	if preview.CredentialState != "" {
		lines = append(lines, "Credential state: "+preview.CredentialState)
	}
	if preview.CredentialNote != "" {
		lines = append(lines, splitDisplayLine("Credential note: "+preview.CredentialNote, inner)...)
	}
	instancePath := preview.InstancePath
	if instancePath == "" {
		instancePath = preview.ProfilePath
	}
	if instancePath != "" {
		lines = append(lines, "Instance: "+preview.Key.Target, "Pack instance TOML (read-only): "+instancePath)
	} else if preview.TargetPath != "" {
		lines = append(lines, "Environment: "+preview.Key.Environment+" · Target: "+preview.Key.Target, "Exact TOML: "+preview.TargetPath)
	} else if preview.Key.Environment != "" && preview.Key.Target != "" && preview.Key.Target != "default" {
		lines = append(lines, "Selected preset: "+preview.Key.Environment+" / "+preview.Key.Target)
	}
	instanceTOML := preview.InstanceTOML
	if instanceTOML == "" {
		instanceTOML = preview.ProfileTOML
	}
	if instanceTOML != "" {
		lines = append(lines, "Read-only instance TOML")
		for _, line := range strings.Split(strings.TrimSuffix(instanceTOML, "\n"), "\n") {
			lines = append(lines, splitDisplayLine(line, inner)...)
		}
	} else if preview.TargetTOML != "" {
		lines = append(lines, "Raw TOML")
		for _, rawLine := range strings.Split(strings.TrimSuffix(preview.TargetTOML, "\n"), "\n") {
			lines = append(lines, splitDisplayLine(rawLine, inner)...)
		}
	} else {
		lines = append(lines, "Raw TOML: (empty)")
	}
	lines = append(lines, "Resolved inputs and value origins")
	for _, input := range preview.Inputs {
		if !input.HasValue {
			continue
		}
		label := input.Definition.Label
		if label == "" {
			label = input.Definition.Name
		}
		path := input.ProvenancePath
		if path == "" {
			path = "path unavailable"
		}
		lines = append(lines, splitDisplayLine(fmt.Sprintf("%s: %v · from %s · %s", label, input.Value, input.Provenance, path), inner)...)
	}
	destinations := workspaceDestinations(preview)
	if preview.MCP {
		lines = append(lines, "Agent destinations · planned skill and MCP configuration paths")
	} else {
		lines = append(lines, "Agent destinations · skill directories")
	}
	if len(destinations) == 0 {
		lines = append(lines, "No named agent destinations are available")
	} else {
		for _, destination := range destinations {
			selection := "not selected"
			if destination.Selected {
				selection = "selected for apply"
			}
			path := destinationDisplayPath(preview, destination)
			lines = append(lines, splitDisplayLine(fmt.Sprintf("%s · %s · %s", destination.ID, selection, path), inner)...)
			if destination.Detection != "" {
				lines = append(lines, splitDisplayLine("Detection: "+destination.Detection, inner)...)
			}
			if destination.Note != "" {
				lines = append(lines, splitDisplayLine("Note: "+destination.Note, inner)...)
			}
			if destination.DisabledReason != "" {
				lines = append(lines, splitDisplayLine("Unavailable for new setup: "+destination.DisabledReason, inner)...)
			}
		}
	}
	return lines
}

func workspaceOverviewLines(preview viewmodel.SetupPreview, installed bool, profile *viewmodel.Instance) []string {
	if profile != nil && observedForeignRunningRuntime(profile.Ownership, profile.RuntimeStatus) {
		return []string{
			"Inputs are managed by another AACT installation.",
			"MCP runtime: " + nonempty(profile.RuntimeStatus, "unknown"),
			"Ownership: " + nonempty(profile.Ownership, "unknown"),
			"Endpoint: " + nonempty(profile.URL, "not configured"),
		}
	}
	configuration := "not saved"
	if preview.Configured {
		configuration = "saved"
	}
	installation := "not recorded"
	if installed {
		installation = "recorded"
	}
	lines := []string{"Configuration: " + configuration + " · package installation: " + installation}
	if !preview.MCP {
		lines = append(lines, "Next: review package settings, then Save and apply.")
		return lines
	}
	if profile == nil {
		lines = append(lines, "MCP runtime: not observed", "Ownership: unknown", "Endpoint: not configured")
		lines = append(lines, "Next: edit configuration, then use Save and apply.")
		return lines
	}
	runtime := profile.RuntimeStatus
	if runtime == "" {
		runtime = "unknown"
	}
	owner := profile.Ownership
	if owner == "" {
		owner = "unknown"
	}
	lines = append(lines, "MCP runtime: "+runtime, "Ownership: "+owner, "Endpoint: "+nonempty(profile.URL, "not configured"))
	return lines
}

func workspaceProfileInformationLines(profile *viewmodel.Instance) []string {
	if profile == nil {
		return nil
	}
	status := nonempty(profile.RuntimeStatus, "unknown")
	owner := nonempty(profile.Ownership, "unknown")
	endpoint := nonempty(profile.URL, "not configured")
	lines := []string{"Selected MCP definition", "Endpoint URI: " + endpoint, "Live runtime status: " + status, "Runtime ownership: " + owner}
	if profile.RegistrationDisabledReason != "" {
		lines = append(lines, "Registration availability: "+profile.RegistrationDisabledReason)
	}
	observedAt := profile.ObservedAt
	if observedAt.IsZero() {
		observedAt = profile.LastActionAt
	}
	if observedAt.IsZero() {
		lines = append(lines, "Live observation time: unavailable")
	} else {
		freshness := "current"
		if profile.ObservationStale {
			freshness = "stale"
		}
		lines = append(lines, fmt.Sprintf("Live observation: %s · %s", freshness, observedAt.Format(time.RFC3339)))
	}
	if profile.LocalLastAction != "" {
		lastAction := "Local last action: " + profile.LocalLastAction
		if !profile.LocalLastActionAt.IsZero() {
			lastAction += " · " + profile.LocalLastActionAt.Format(time.RFC3339)
		}
		lines = append(lines, lastAction)
	}
	if len(profile.RegisteredAgents) == 0 {
		lines = append(lines, "Local registrations: none")
	} else {
		lines = append(lines, "Local registrations: "+strings.Join(profile.RegisteredAgents, ", "))
	}
	return lines
}

func nonempty(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func (m *Model) instanceForMCPName(name string) *viewmodel.Instance {
	if m.workspace == nil {
		return nil
	}
	if m.workspace.InstanceRef.CapabilityID != "" {
		for _, p := range m.capabilityInstances[m.workspace.Key.Package].Instances {
			if p.Key != m.workspace.Key {
				continue
			}
			for _, r := range p.MCPs {
				if r.MCPID == name {
					return configurationRuntimeInstance(p, r, m.workspace.Preview)
				}
			}
		}
	}
	key := m.workspace.Key
	if snapshot := m.workspace.InstanceSnapshot; snapshot != nil {
		for _, profile := range runtimeInstances(snapshot) {
			if profile.Key.Source == key.Source && profile.Key.Package == key.Package && profile.Key.Environment == key.Environment && profile.Key.Target == key.Target && profile.Key.MCP == name {
				copy := profile
				return &copy
			}
		}
	}
	for _, instance := range m.mcps {
		if instance.Key.Source == key.Source && instance.Key.Package == key.Package && instance.Key.Environment == key.Environment && instance.Key.Target == key.Target && instance.Key.MCP == name {
			return &viewmodel.Instance{Key: instance.Key, Name: instance.Name, URL: instance.URL, RuntimeStatus: instance.Status, Ownership: instance.Ownership}
		}
	}
	if m.workspace.Instance != nil && m.workspace.Instance.Key.MCP == name {
		copy := *m.workspace.Instance
		return &copy
	}
	if m.workspace.Instance != nil && m.workspace.Instance.Key.MCP == "" && m.workspace.Preview != nil && len(m.workspace.Preview.MCPDefinitions) == 1 {
		copy := *m.workspace.Instance
		copy.Key.MCP = name
		return &copy
	}
	return nil
}

func (m *Model) runtimeStatusForMCP(name string) *viewmodel.RuntimeStatus {
	if m.workspace == nil {
		return nil
	}
	snapshot, ok := m.capabilityInstances[m.workspace.Key.Package]
	if !ok {
		return nil
	}
	for _, instance := range capabilityInstances(snapshot) {
		if instance.Key != m.workspace.Key {
			continue
		}
		for i := range instance.MCPs {
			if instance.MCPs[i].MCPID == name {
				return &instance.MCPs[i]
			}
		}
	}
	return nil
}

func (m *Model) legacyWorkspaceMCP() *viewmodel.Instance {
	if m.workspace == nil {
		return nil
	}
	key := m.workspace.Key
	if m.workspace.Instance != nil {
		profile := *m.workspace.Instance
		if profile.Key.Source == key.Source && profile.Key.Package == key.Package && profile.Key.Environment == key.Environment && profile.Key.Target == key.Target {
			return &profile
		}
	}
	matches := []viewmodel.Instance{}
	if snapshot := m.workspace.InstanceSnapshot; snapshot != nil {
		for _, profile := range runtimeInstances(snapshot) {
			if profile.Key.Source == key.Source && profile.Key.Package == key.Package && profile.Key.Environment == key.Environment && profile.Key.Target == key.Target {
				matches = append(matches, profile)
			}
		}
	}
	if len(matches) == 1 {
		return &matches[0]
	}
	return nil
}

func (m *Model) workspaceOverviewAction(stroke string) (bool, tea.Cmd) {
	if m.workspace == nil || m.form == nil {
		return false, nil
	}
	switch strings.ToLower(stroke) {
	case "g", "r":
		m.workspace.Section = "Agent access"
		m.workspace.SectionID = sectionAgentsID
		m.form.SelectSectionID(sectionAgentsID)
		m.form.FocusSection()
	case "i":
		m.workspace.Section = "Information"
		m.workspace.SectionID = sectionInformationID
		m.form.SelectSectionID(sectionInformationID)
		m.form.FocusSection()
	case "l":
		if !m.form.HasSectionID(sectionLogsID) {
			return false, nil
		}
		m.workspace.Section = "Logs"
		m.workspace.SectionID = sectionLogsID
		m.form.SelectSectionID(m.workspace.SectionID)
		m.form.FocusSection()
		if m.workspace.Preview == nil || len(m.workspace.Preview.MCPDefinitions) > 1 {
			m.output = "Choose a specific MCP under Runtime to view its logs."
			return true, nil
		}
		var profile *viewmodel.Instance
		if len(m.workspace.Preview.MCPDefinitions) == 1 {
			profile = m.instanceForMCPName(m.workspace.Preview.MCPDefinitions[0].Name)
		} else {
			profile = m.legacyWorkspaceMCP()
		}
		if profile == nil {
			m.output = "Runtime observation is unavailable; refresh this target before viewing logs."
			return true, nil
		}
		m.workspace.cacheDraft(m.form.Values())
		return true, m.openProfileLogs(InstanceRow{Key: m.workspace.Key, URL: profile.URL, Name: profile.Name, Status: profile.RuntimeStatus, Instance: profile})
	case "s", "x":
		shortcut := strings.ToLower(stroke)
		if shortcut == "s" {
			if m.workspace.Preview == nil || !m.workspace.Preview.MCP {
				return false, nil
			}
			definitions := m.workspace.Preview.MCPDefinitions
			if len(definitions) > 1 {
				parent := m.workspace.Instance
				row := InstanceRow{}
				if parent != nil {
					row = InstanceRow{Key: parent.Key, Instance: parent, URL: parent.URL, Status: parent.RuntimeStatus}
				}
				if reason := m.instanceActionReason(row, "s"); reason != "" {
					m.output = reason
					return true, nil
				}
				m.output = "Choose a specific MCP under Runtime to start it."
				return true, nil
			}
			profile := (*viewmodel.Instance)(nil)
			mcpName := ""
			if len(definitions) == 1 {
				mcpName = definitions[0].Name
				profile = m.instanceForMCPName(mcpName)
			} else {
				profile = m.legacyWorkspaceMCP()
				if profile != nil {
					mcpName = profile.Key.MCP
				}
			}
			if profile == nil && m.workspace.Instance != nil {
				parent := *m.workspace.Instance
				profile = &parent
			}
			row := InstanceRow{}
			if profile != nil {
				row = InstanceRow{Key: profile.Key, Instance: profile, URL: profile.URL, Status: profile.RuntimeStatus}
			}
			if reason := m.instanceActionReason(row, "s"); reason != "" {
				m.output = reason
				return true, nil
			}
			if m.instanceError != nil {
				m.output = "Runtime observation failed: " + m.instanceError.Error() + "; refresh this target before starting it."
				return true, nil
			}
			if mcpName == "" && len(definitions) > 0 {
				m.output = "Choose a specific MCP under Runtime to start it."
				return true, nil
			}
			m.workspace.cacheDraft(m.form.Values())
			return true, m.run(operation{action: "start", source: m.workspace.Key.Source, packageID: m.workspace.Key.Package, environment: m.workspace.Key.Environment, target: m.workspace.Key.Target, mcp: mcpName})
		}
		if m.workspace.Preview == nil || len(m.workspace.Preview.MCPDefinitions) > 1 {
			m.output = "Choose a specific MCP under Runtime to stop it."
			return true, nil
		}
		definition := catalog.MCP{}
		var profile *viewmodel.Instance
		if len(m.workspace.Preview.MCPDefinitions) == 1 {
			definition = m.workspace.Preview.MCPDefinitions[0]
			profile = m.instanceForMCPName(definition.Name)
		} else {
			profile = m.legacyWorkspaceMCP()
			if profile != nil {
				definition.Name = profile.Key.MCP
			}
		}
		if profile == nil {
			m.output = "No locally owned MCP runtime is available to stop."
			return true, nil
		}
		reason := m.instanceActionReason(InstanceRow{Key: profile.Key, Instance: profile, URL: profile.URL, Status: profile.RuntimeStatus}, shortcut)
		if reason != "" {
			m.output = reason
			return true, nil
		}
		m.workspace.cacheDraft(m.form.Values())
		return true, m.run(operation{action: "stop", source: m.workspace.Key.Source, packageID: m.workspace.Key.Package, environment: m.workspace.Key.Environment, target: m.workspace.Key.Target, mcp: definition.Name})
	default:
		return false, nil
	}
	return true, nil
}

func (m *Model) configureWorkspaceForm(preview viewmodel.SetupPreview) {
	if m.workspace == nil || m.workspace.Key != preview.Key || m.form == nil {
		return
	}
	stored := preview
	m.workspace.Preview = &stored
	orphanRuntimes := m.workspaceOrphanRuntimes(preview)
	sections := workspaceFormSections(preview, m.form.Definitions(), m.pendingSetupField)
	inputsReadOnly := m.workspaceInputsReadOnly()
	if inputsReadOnly {
		found := false
		for i := range sections {
			if sections[i].ID == sectionInputsID {
				sections[i].Fields = nil
				found = true
				break
			}
		}
		if !found {
			sections = append([]forms.FormSection{{ID: sectionInputsID, Title: "Inputs"}}, sections...)
		}
	}
	if len(orphanRuntimes) > 0 && !preview.MCP {
		sections = append(sections, forms.FormSection{ID: sectionRuntimeID, Title: "Runtime"})
	}
	m.form.SetSections(sections...)
	m.configureComponentActions()
	m.form.SetSectionHeading("Workspace sections")
	if m.workspace.Section == "" {
		m.workspace.Section = "Inputs"
		m.workspace.SectionID = sectionInputsID
		for _, section := range sections {
			if len(section.Fields) > 0 {
				m.workspace.Section = section.Title
				m.workspace.SectionID = section.ID
				break
			}
		}
	}
	if m.workspace.Instance != nil {
		m.workspace.Instance.Key = preview.Key
	}
	m.workspace.InputsSaved = preview.Configured
	ready, required := 0, 0
	for _, input := range preview.Inputs {
		if !input.Definition.Required {
			continue
		}
		required++
		if input.HasValue {
			ready++
		}
	}
	inputLines := []string{fmt.Sprintf("Inputs · %d / %d required ready", ready, required), "Save stores these inputs. It does not install or start anything."}
	if inputsReadOnly {
		inputLines = []string{"Inputs are managed by another AACT installation."}
	}
	if m.workspace.SaveNotice != "" && !inputsReadOnly {
		inputLines = append(inputLines, m.workspace.SaveNotice)
	}
	if m.hasUnsavedSetupInputs() && !inputsReadOnly {
		inputLines = append(inputLines, "Unsaved changes · Save before installing or starting.")
	}
	for _, issue := range preview.ValidationIssues {
		if inputsReadOnly {
			break
		}
		inputLines = append(inputLines, "Needs attention: "+issue)
	}
	if m.workspace.SelectedMCP != "" && !inputsReadOnly {
		inputLines = append(inputLines, "Editing inputs for MCP "+m.workspace.SelectedMCP+" · answers are saved at this capability instance.")
	}
	for _, section := range preview.Sections {
		if inputsReadOnly {
			break
		}
		if len(section.Fields) > 0 {
			inputLines = append(inputLines, strings.ToUpper(section.Title))
		}
	}
	m.form.SetSectionContentID(sectionInputsID, inputLines)

	agentAccess := []string{"Select agents to install this capability for.", "Agent install edits configuration only; it does not start Docker.", "Agent          Target   Skill         MCP config"}
	for _, destination := range workspaceDestinations(preview) {
		status := "Not installed"
		marker := "[ ]"
		if destination.Selected {
			status, marker = "Installed", "[x]"
		}
		mcpStatus := "Not installed"
		if !preview.MCP {
			mcpStatus = "—"
		} else if destination.Selected {
			mcpStatus = "Installed"
		}
		name := firstNonempty(destination.Name, destination.ID)
		agentAccess = append(agentAccess, fmt.Sprintf("%-14s %-8s %-13s %s", name, marker, status, mcpStatus))
		details := []string{}
		detectionFailed := strings.HasPrefix(strings.ToLower(destination.Detection), "failed")
		if detectionFailed {
			details = append(details, destination.Detection)
		}
		note := strings.ToLower(destination.Note)
		if detectionFailed || strings.Contains(note, "conflict") || strings.Contains(note, "permission") || strings.Contains(note, "failed") {
			details = append(details, destination.Note)
		}
		if destination.DisabledReason != "" {
			details = append(details, destination.DisabledReason)
		}
		for _, detail := range details {
			agentAccess = append(agentAccess, "  "+name+": "+detail)
		}
	}
	m.form.SetSectionContentID(sectionAgentsID, agentAccess)
	m.refreshAgentAccessActions()

	m.workspace.RuntimeRecordTargets = nil
	if preview.MCP || len(orphanRuntimes) > 0 {
		runtime := []string{}
		logs := []string{}
		runtimeActions := []forms.FormAction{}
		definitions := append([]catalog.MCP(nil), preview.MCPDefinitions...)
		if len(definitions) == 0 {
			if profile := m.legacyWorkspaceMCP(); profile != nil {
				definitions = append(definitions, catalog.MCP{Name: profile.Key.MCP})
			}
		}
		for _, definition := range definitions {
			profile := m.instanceForMCPName(definition.Name)
			if len(preview.MCPDefinitions) == 0 {
				profile = m.legacyWorkspaceMCP()
			}
			label := definition.Name
			if label == "" && profile != nil {
				label = nonempty(profile.Name, preview.Key.Package)
			}
			status, owner, endpoint := "not observed", "unknown", "not configured"
			if profile != nil {
				status, owner, endpoint = nonempty(profile.RuntimeStatus, "unknown"), nonempty(profile.Ownership, "unknown"), nonempty(profile.URL, "not configured")
			}
			child := m.runtimeStatusForMCP(definition.Name)
			runtimeLine := label + " · status: " + status + " · ownership: " + owner + " · endpoint: " + endpoint
			if m.workspace.SelectedMCP == definition.Name {
				runtimeLine = "> " + runtimeLine
			}
			runtime = append(runtime, runtimeLine)
			foreignReason := m.workspaceForeignRuntimeReason(definition.Name)
			if foreignReason == "" && m.runtimeLoginRunsOnHost(definition.Name) {
				runtime = append(runtime, "  You can log in before starting this MCP. If Start asks for credentials, select Login.")
			}
			startProfile := profile
			if startProfile == nil && len(preview.MCPDefinitions) == 0 && m.workspace.Instance != nil {
				parent := *m.workspace.Instance
				startProfile = &parent
			}
			startRow := InstanceRow{}
			if startProfile != nil {
				startRow = InstanceRow{Key: startProfile.Key, Instance: startProfile, URL: startProfile.URL, Status: startProfile.RuntimeStatus}
			}
			startDisabled := m.runtimeStartDisabledReason(definition.Name, startRow)
			if foreignReason != "" {
				startDisabled = foreignReason
			}
			runtimeActions = append(runtimeActions, forms.FormAction{ID: "start:" + definition.Name, Label: "Start " + definition.Name, Disabled: startDisabled})
			if _, supportsAuthentication := definition.Actions["authenticate"]; supportsAuthentication {
				authenticateDisabled := m.runtimeAuthenticateDisabledReason(definition.Name, startRow)
				if foreignReason != "" {
					authenticateDisabled = foreignReason
				}
				runtimeActions = append(runtimeActions, forms.FormAction{ID: "authenticate:" + definition.Name, Label: "Login to " + definition.Name, Disabled: authenticateDisabled})
			}
			if child != nil {
				if child.Error != "" {
					runtime = append(runtime, "  MCP observation: "+child.Error)
				}
				for _, record := range child.RuntimeRecords {
					line := fmt.Sprintf("  Runtime ID: %s · %s · ownership: %s", record.ID, record.Status, record.Ownership)
					if record.Name != "" {
						line += " · container: " + record.Name
					}
					if record.URL != "" {
						line += " · endpoint: " + record.URL
					}
					if record.LastAction != "" {
						line += " · last action: " + record.LastAction
					}
					if record.ID == m.workspace.SelectedRuntimeID {
						line = "> " + line
					}
					runtime = append(runtime, line)
					if strings.TrimSpace(record.ID) == "" {
						continue
					}
					targetIndex := len(m.workspace.RuntimeRecordTargets)
					m.workspace.RuntimeRecordTargets = append(m.workspace.RuntimeRecordTargets, runtimeRecordTarget{Key: child.Key, ID: record.ID, Ownership: record.Ownership})
					deleteLabel := "Delete runtime " + record.ID
					deleteID := "delete-runtime:" + fmt.Sprint(targetIndex)
					disabled := ""
					switch {
					case child.Stale:
						if strings.TrimSpace(child.Error) != "" {
							disabled = "Runtime observation failed: " + strings.TrimSpace(child.Error)
						} else {
							disabled = "Runtime observation is stale; refresh before deleting"
						}
					case record.Status == "missing":
						deleteLabel = "Forget missing runtime " + record.ID
					case record.Ownership == "other-aact":
						disabled = runtimeManagedElsewhereReason
					case record.Ownership != "local":
						disabled = "Runtime ownership is " + record.Ownership + "; AACT cannot delete it"
					}
					runtimeActions = append(runtimeActions, forms.FormAction{ID: deleteID, Label: deleteLabel, Disabled: disabled})
				}
			}
			editDisabled := foreignReason
			runtimeActions = append(runtimeActions, forms.FormAction{ID: "edit-inputs:" + definition.Name, Label: "Edit capability inputs for " + definition.Name, Disabled: editDisabled})
			if profile != nil && profile.CanStop && (child == nil || len(child.RuntimeRecords) == 1) {
				runtimeActions = append(runtimeActions, forms.FormAction{ID: "stop:" + definition.Name, Label: "Stop " + definition.Name})
			} else if profile != nil && profile.RuntimeStatus == "running" && child != nil && len(child.RuntimeRecords) > 1 {
				runtimeActions = append(runtimeActions, forms.FormAction{ID: "stop:" + definition.Name, Label: "Stop " + definition.Name, Disabled: "Multiple runtime records are linked; select one exact record"})
			} else if foreignReason != "" {
				runtimeActions = append(runtimeActions, forms.FormAction{ID: "stop:" + definition.Name, Label: "Stop " + definition.Name, Disabled: foreignReason})
			}
			if profile != nil && profile.RuntimeStatus == "running" && (child == nil || len(child.RuntimeRecords) == 1) {
				runtimeActions = append(runtimeActions, forms.FormAction{ID: "logs:" + definition.Name, Label: "View " + definition.Name + " logs"})
			}
			if profile != nil && profile.URL != "" && (child == nil || len(child.RuntimeRecords) <= 1) {
				runtimeActions = append(runtimeActions, forms.FormAction{ID: "check-connection:" + definition.Name, Label: "Check " + definition.Name + " connection"})
			}
			logs = append(logs, label+" · "+status)
		}
		for _, orphan := range orphanRuntimes {
			label := nonempty(orphan.MCPID, "unknown MCP")
			runtime = append(runtime, "Unlinked MCP runtime · "+label+" · status: "+nonempty(orphan.Status, "unknown")+" · ownership: "+nonempty(orphan.Ownership, "unknown"))
			if orphan.Error != "" {
				runtime = append(runtime, "  "+orphan.Error)
			}
			for _, record := range orphan.RuntimeRecords {
				if strings.TrimSpace(record.ID) == "" {
					continue
				}
				line := "  Runtime ID: " + record.ID + " · " + nonempty(record.Status, "unknown") + " · ownership: " + nonempty(record.Ownership, "unknown")
				if record.Name != "" {
					line += " · container: " + record.Name
				}
				if record.URL != "" {
					line += " · endpoint: " + record.URL
				}
				runtime = append(runtime, line)
				targetIndex := len(m.workspace.RuntimeRecordTargets)
				m.workspace.RuntimeRecordTargets = append(m.workspace.RuntimeRecordTargets, runtimeRecordTarget{Key: orphan.Key, ID: record.ID, Ownership: record.Ownership})
				label := "Forget runtime record " + record.ID
				disabled := ""
				if orphan.Stale {
					if strings.TrimSpace(orphan.Error) != "" {
						disabled = "Runtime observation failed: " + strings.TrimSpace(orphan.Error)
					} else {
						disabled = "Runtime observation is stale; refresh before removing this record"
					}
				} else if record.Ownership == "other-aact" {
					disabled = runtimeManagedElsewhereReason
				} else if record.Status != "missing" && record.Ownership == "local" {
					label = "Delete runtime " + record.ID
				} else if record.Status == "missing" {
					label = "Forget missing runtime " + record.ID
				}
				runtimeActions = append(runtimeActions, forms.FormAction{ID: "delete-runtime:" + fmt.Sprint(targetIndex), Label: label, Disabled: disabled})
			}
		}
		if len(runtime) == 0 {
			runtime = []string{"Runtime definitions are unavailable in this setup preview."}
		}
		if len(logs) == 0 {
			logs = []string{"No runtime logs are available."}
		}
		status := "Stopped"
		authentication := ""
		if len(definitions) > 0 {
			if profile := m.instanceForMCPName(definitions[0].Name); profile != nil {
				if profile.RuntimeStatus == "running" {
					status = "Running"
				}
				if label := authenticationDisplayStatus(profile.AuthenticationStatus); label != "" {
					authentication = "Authentication: " + label
				}
			}
		}
		inputStatus, installStatus := "Not saved", "Not installed"
		if m.workspace.InputsSaved {
			inputStatus = "Saved"
		}
		if m.workspace.Installed {
			installStatus = "Complete"
		}
		header := []string{"Status: " + status}
		if inputsReadOnly {
			header = append(header, "Inputs are managed by another AACT installation.")
		} else {
			if len(preview.ValidationIssues) > 0 {
				header = append(header, "Inputs need attention: "+strings.Join(preview.ValidationIssues, "; "))
			}
			if authentication != "" {
				header = append(header, authentication)
			}
			header = append(header, "Inputs: "+inputStatus, "Agent install: "+installStatus, "This starts the Docker instance. It does not edit agent configuration.")
		}
		runtime = append(header, runtime...)
		m.form.SetSectionContentID(sectionRuntimeID, runtime)
		m.form.SetSectionActionsID(sectionRuntimeID, runtimeActions...)
		m.form.SetSectionContentID(sectionLogsID, logs)
	}
	informationPreview := preview
	if inputsReadOnly {
		informationPreview.Inputs = nil
		informationPreview.InstanceTOML = ""
		informationPreview.ProfileTOML = ""
		informationPreview.TargetTOML = ""
		informationPreview.InstancePath = ""
		informationPreview.ProfilePath = ""
		informationPreview.TargetPath = ""
	}
	information := workspaceInformationLines(informationPreview, m.form.SectionContentWidth())
	if inputsReadOnly {
		information = append([]string{"Inputs are managed by another AACT installation."}, information...)
	}
	if preview.MCP {
		if profileInformation := workspaceProfileInformationLines(m.workspace.Instance); len(profileInformation) > 0 {
			information = append(profileInformation, information...)
		}
	}
	m.form.SetSectionContentID(sectionInformationID, information)
	if m.workspace.SectionID != "" && !m.form.HasSectionID(m.workspace.SectionID) {
		m.workspace.SectionID = ""
	}
	if m.workspace.SectionID == "" && !m.form.HasSection(m.workspace.Section) {
		m.workspace.Section = "Inputs"
		m.workspace.SectionID = sectionInputsID
	}
	selectionChanged := false
	if m.workspace.SectionID != "" && m.form.SectionID() != m.workspace.SectionID {
		selectionChanged = m.form.SelectSectionID(m.workspace.SectionID)
	} else if m.workspace.SectionID == "" && m.form.SectionTitle() != m.workspace.Section {
		m.form.SelectSection(m.workspace.Section)
		selectionChanged = true
	}
	if selectionChanged {
		m.form.FocusSection()
	}
	m.form.SetSubmitLabel("Save")
	m.form.SetSubmitHidden(m.workspace.Active && (m.form.SectionID() != sectionInputsID || inputsReadOnly))
	m.form.SetCancelHidden(m.workspace.Active && (m.form.SectionID() != sectionInputsID || inputsReadOnly))
	m.form.SetTabThroughControls(m.workspace.Active && m.form.SectionID() == sectionInputsID && !inputsReadOnly)
	m.refreshAgentAccessActions()
}

func (m *Model) workspaceOrphanRuntimes(preview viewmodel.SetupPreview) []viewmodel.RuntimeStatus {
	if m.workspace == nil {
		return nil
	}
	declared := map[string]bool{}
	for _, definition := range preview.MCPDefinitions {
		declared[definition.Name] = true
	}
	snapshot, ok := m.capabilityInstances[preview.Key.Package]
	if !ok {
		return nil
	}
	var orphaned []viewmodel.RuntimeStatus
	for _, instance := range capabilityInstances(snapshot) {
		if instance.Key != preview.Key {
			continue
		}
		for _, runtime := range instance.MCPs {
			if declared[runtime.MCPID] {
				continue
			}
			orphaned = append(orphaned, runtime)
		}
		break
	}
	return orphaned
}

func appendActionReason(current, reason string) string {
	if current == "" {
		return reason
	}
	return current + " · " + reason
}

func (m *Model) refreshAgentAccessActions() {
	if m.form == nil || m.workspace == nil || !m.workspace.Active || m.workspace.Preview == nil {
		return
	}
	reason := m.agentAccessInputGuardReason()
	installDisabled := reason != ""
	m.workspace.AgentActionsKnown = true
	m.workspace.AgentActionsDisabled = installDisabled
	m.form.SetSectionActionsID(sectionAgentsID,
		forms.FormAction{ID: "install-selected", Label: "Install selected", Disabled: reason},
		forms.FormAction{ID: "uninstall-selected", Label: "Uninstall selected"},
	)
}

func formSectionFields(sections []forms.FormSection, title string) []string {
	for _, section := range sections {
		if section.Title == title {
			return section.Fields
		}
	}
	return nil
}

func (m *Model) runtimeStartDisabledReason(mcpName string, row InstanceRow) string {
	if reason := m.workspaceForeignRuntimeReason(mcpName); reason != "" {
		return reason
	}
	if m.workspace == nil || m.workspace.Preview == nil {
		return m.instanceActionReason(row, "s")
	}
	parent := m.workspace.Instance
	if parent == nil {
		parent = exactInstanceSnapshot(m.instanceSnapshot, m.workspace.Key)
	}
	if row.Instance == nil && parent != nil && parent.Ownership != "local" {
		// Parent ownership does not make an unobserved named child startable,
		// but it does explain why AACT cannot safely manage that child.
		return runtimeStartBlocker(parent, "")
	}
	preview := m.workspace.Preview
	reason := m.instanceActionReason(row, "s")
	switch {
	case m.hasUnsavedSetupInputs():
		reason = appendActionReason(reason, "Save changed inputs first")
	case !m.workspace.InputsSaved && len(preview.ValidationIssues) > 0:
		reason = appendActionReason(reason, "Inputs need attention: "+strings.Join(preview.ValidationIssues, "; "))
	case !m.workspace.InputsSaved:
		reason = appendActionReason(reason, "Save valid inputs first")
	case !m.workspace.Installed || !m.workspaceMCPRegistered(mcpName):
		reason = appendActionReason(reason, "Install this capability for an agent first")
	}
	if reason == "" {
		reason = m.mcpRegistrationAddressReason(mcpName)
	}
	return reason
}

func (m *Model) mcpRegistrationAddressReason(mcpName string) string {
	if m.workspace == nil || m.workspace.Preview == nil || mcpName == "" {
		return ""
	}
	var definition *catalog.MCP
	for i := range m.workspace.Preview.MCPDefinitions {
		if m.workspace.Preview.MCPDefinitions[i].Name == mcpName {
			definition = &m.workspace.Preview.MCPDefinitions[i]
			break
		}
	}
	// Older definitions without declared host ports use the observed runtime
	// endpoint. They cannot be compared to a pre-Start address.
	if definition == nil || definition.HostPortInput == "" {
		return ""
	}
	childKey, ok := m.workspace.Preview.MCPDefinitionKeys[mcpName]
	if !ok {
		return ""
	}
	registeredURLs := make([]string, 0, 1)
	for _, installation := range m.inventory {
		if installation.Key == childKey && installation.Component == "mcp" && !installation.ExternalRegistration && installation.URL != "" {
			registeredURLs = append(registeredURLs, installation.URL)
		}
	}
	if len(registeredURLs) == 0 {
		return ""
	}
	values := make(map[string]any, len(m.workspace.Preview.Inputs))
	for _, input := range m.workspace.Preview.Inputs {
		if input.HasValue {
			values[input.Definition.Name] = input.Value
		} else if input.Value != nil {
			values[input.Definition.Name] = input.Value
		} else if input.Definition.Default != nil {
			values[input.Definition.Name] = input.Definition.Default
		}
	}
	desiredURL, err := app.ConfiguredMCPRegistrationURL(*definition, values)
	if err != nil {
		return "Cannot resolve the saved MCP address: " + err.Error()
	}
	for _, registeredURL := range registeredURLs {
		if sameMCPRegistrationURL(registeredURL, desiredURL) {
			return ""
		}
	}
	return fmt.Sprintf("Agent MCP registration points to %s; saved inputs resolve to %s. Select Install to update this agent binding before Start.", registeredURLs[0], desiredURL)
}

func sameMCPRegistrationURL(left, right string) bool {
	if left == right {
		return true
	}
	a, errA := url.Parse(left)
	b, errB := url.Parse(right)
	if errA != nil || errB != nil || a.User != nil || b.User != nil {
		return false
	}
	if !strings.EqualFold(a.Scheme, b.Scheme) || a.Port() != b.Port() || a.Path != b.Path || a.RawPath != b.RawPath || a.RawQuery != b.RawQuery || a.ForceQuery != b.ForceQuery || a.Fragment != b.Fragment {
		return false
	}
	hostA, hostB := strings.ToLower(a.Hostname()), strings.ToLower(b.Hostname())
	if hostA == hostB {
		return true
	}
	return isLoopbackName(hostA) && isLoopbackName(hostB)
}

func isLoopbackName(host string) bool {
	switch host {
	case "localhost", "127.0.0.1":
		return true
	default:
		return false
	}
}

func (m *Model) runtimeAuthenticateDisabledReason(mcpName string, row InstanceRow) string {
	if reason := m.workspaceForeignRuntimeReason(mcpName); reason != "" {
		return reason
	}
	if m.workspace == nil || m.workspace.Preview == nil {
		return "Refresh this target before logging in"
	}
	if m.hasUnsavedSetupInputs() {
		return "Save changed inputs first"
	}
	if !m.workspace.InputsSaved || !m.workspace.Preview.Configured {
		if len(m.workspace.Preview.ValidationIssues) > 0 {
			return "Inputs need attention: " + strings.Join(m.workspace.Preview.ValidationIssues, "; ")
		}
		return "Save valid inputs first"
	}
	definition := m.workspaceMCPDefinition(mcpName)
	if definition == nil {
		return "Refresh this target; its Login action is unavailable"
	}
	command, ok := mcpruntime.ResolveActionCommand(*definition, "authenticate", "")
	if !ok {
		return "This MCP does not declare a Login action"
	}
	if command.Target != "container" {
		return ""
	}
	exactMCP := row.Key.MCP == mcpName || row.Key.MCPDefinition == mcpName || (row.Instance != nil && row.Instance.Name == mcpName)
	if row.Instance == nil || !exactMCP || strings.ToLower(strings.TrimSpace(row.Status)) != "running" {
		return "Start this MCP before logging in"
	}
	return ""
}

func (m *Model) workspaceMCPDefinition(name string) *catalog.MCP {
	if m.workspace == nil || m.workspace.Preview == nil {
		return nil
	}
	for i := range m.workspace.Preview.MCPDefinitions {
		if m.workspace.Preview.MCPDefinitions[i].Name == name {
			return &m.workspace.Preview.MCPDefinitions[i]
		}
	}
	return nil
}

func (m *Model) runtimeLoginRunsOnHost(name string) bool {
	definition := m.workspaceMCPDefinition(name)
	if definition == nil {
		return false
	}
	command, ok := mcpruntime.ResolveActionCommand(*definition, "authenticate", "")
	return ok && command.Target != "container"
}

func (m *Model) refreshRuntimeActionAvailability() {
	if m.form == nil || m.workspace == nil || !m.workspace.Active || m.workspace.Preview == nil {
		return
	}
	actions := m.form.SectionActionsID(sectionRuntimeID)
	changed := false
	for i := range actions {
		prefix := ""
		switch {
		case strings.HasPrefix(actions[i].ID, "start:"):
			prefix = "start:"
		case strings.HasPrefix(actions[i].ID, "authenticate:"):
			prefix = "authenticate:"
		default:
			continue
		}
		mcpName := strings.TrimPrefix(actions[i].ID, prefix)
		profile := m.instanceForMCPName(mcpName)
		if len(m.workspace.Preview.MCPDefinitions) == 0 {
			profile = m.legacyWorkspaceMCP()
		}
		row := InstanceRow{}
		if profile != nil {
			row = InstanceRow{Key: profile.Key, Instance: profile, URL: profile.URL, Status: profile.RuntimeStatus}
		} else if len(m.workspace.Preview.MCPDefinitions) == 0 && m.workspace.Instance != nil {
			parent := *m.workspace.Instance
			row = InstanceRow{Key: parent.Key, Instance: &parent, URL: parent.URL, Status: parent.RuntimeStatus}
		}
		disabled := ""
		if prefix == "start:" {
			disabled = m.runtimeStartDisabledReason(mcpName, row)
		} else {
			disabled = m.runtimeAuthenticateDisabledReason(mcpName, row)
		}
		if actions[i].Disabled != disabled {
			actions[i].Disabled = disabled
			changed = true
		}
	}
	if changed {
		m.form.SetSectionActionsID(sectionRuntimeID, actions...)
	}
}

func workspaceFormSections(preview viewmodel.SetupPreview, defs []catalog.Input, destinationField string) []forms.FormSection {
	known := make(map[string]bool, len(defs))
	ordered := make([]string, 0, len(defs))
	hasDestination := false
	for _, def := range defs {
		if def.Name == preview.ItemFieldName {
			continue
		}
		if def.Name == destinationField {
			hasDestination = true
			continue
		}
		known[def.Name] = true
		ordered = append(ordered, def.Name)
	}
	sections := []forms.FormSection{}
	if len(ordered) > 0 {
		sections = append(sections, forms.FormSection{ID: sectionInputsID, Title: "Inputs", Fields: ordered})
	}
	if preview.ItemFieldName != "" && len(preview.Items) > 1 {
		sections = append(sections, forms.FormSection{ID: sectionComponentsID, Title: "Components", Fields: []string{preview.ItemFieldName}})
	}
	var agentFields []string
	if hasDestination {
		agentFields = []string{destinationField}
	}
	sections = append(sections, forms.FormSection{ID: sectionAgentsID, Title: "Agent access", Fields: agentFields})
	if preview.MCP {
		sections = append(sections, forms.FormSection{ID: sectionRuntimeID, Title: "Runtime"}, forms.FormSection{ID: sectionLogsID, Title: "Logs"})
	}
	sections = append(sections, forms.FormSection{ID: sectionInformationID, Title: "Information"})
	return sections
}
