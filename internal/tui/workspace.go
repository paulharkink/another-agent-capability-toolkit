package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/forms"
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
	sectionOverviewID    = "tool:overview"
	sectionAgentsID      = "tool:agents"
	sectionEndpointID    = "tool:endpoint"
	sectionRuntimeID     = "tool:runtime"
	sectionLogsID        = "tool:logs"
	sectionInformationID = "tool:information"
)

// workspaceState is the cached editor context for one exact target. It keeps
// the invoking layer and profile observation beside the target draft so later
// route handling can restore Back without losing the parent selection.
type workspaceState struct {
	Key               state.Key
	Section           string
	SectionID         string
	InvokingView      string
	InvokingSelection int
	Active            bool
	Existing          bool
	Installed         bool
	ObservedOnly      bool
	Preview           *viewmodel.SetupPreview
	Profile           *viewmodel.Profile
	ProfileSnapshot   *viewmodel.ProfileSnapshot
	Draft             map[string]any
}

func (m *Model) refreshObservedWorkspaceFacts() {
	if m.workspace == nil || !m.workspace.ObservedOnly || m.workspace.Profile == nil || m.form == nil {
		return
	}
	profile := m.workspace.Profile
	key := m.workspace.Key
	registered := "none observed"
	if len(profile.RegisteredAgents) > 0 {
		registered = strings.Join(profile.RegisteredAgents, ", ")
	}
	m.form.SetSectionContentID(sectionOverviewID, []string{
		"Package configuration unavailable: this package is absent from the local catalog.",
		"Source: " + key.Source + " · Package: " + key.Package,
		"Runtime: " + nonempty(profile.RuntimeStatus, "unknown") + " · Ownership: " + nonempty(profile.Ownership, "unknown"),
		"Locate the source checkout to edit capability settings and manage complete agent bindings.",
	})
	m.form.SetSectionContentID(sectionAgentsID, []string{
		"Observed registrations: " + registered,
		"Complete agent bindings require the capability manifest. Locate its source to manage them.",
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
		checkDisabled = "Connection diagnostics are unavailable"
	}
	m.form.SetSectionActionsID(sectionEndpointID, forms.FormAction{ID: "check-connection", Label: "Check connection", Disabled: checkDisabled})
	m.form.SetSectionContentID(sectionInformationID, []string{
		"Observed target identity: " + key.Source + " / " + key.Package + " / " + nonempty(key.Environment, "(none)") + " / " + nonempty(key.Target, "default"),
		"Observed endpoint: " + nonempty(profile.URL, "unavailable"),
		"Observed transport: " + nonempty(profile.Transport, "unknown"),
		"Runtime status: " + nonempty(profile.RuntimeStatus, "unknown"),
		"Runtime ownership: " + nonempty(profile.Ownership, "unknown"),
		"This workspace reflects observed profile facts; it does not imply package installation or local runtime ownership.",
	})
	m.form.SetSectionActionsID(sectionOverviewID, forms.FormAction{ID: "locate-source", Label: "Locate source checkout"})
	m.form.SetSectionActionsID(sectionAgentsID, forms.FormAction{ID: "locate-source", Label: "Locate source checkout"})
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
		fmt.Sprintf("Source identity: %s · checkout %s", preview.Key.Source, preview.SourceRoot),
	}
	if preview.CredentialState != "" {
		lines = append(lines, "Credential state: "+preview.CredentialState)
	}
	if preview.CredentialNote != "" {
		lines = append(lines, splitDisplayLine("Credential note: "+preview.CredentialNote, inner)...)
	}
	if preview.TargetPath != "" {
		lines = append(lines, "Environment: "+preview.Key.Environment+" · Target: "+preview.Key.Target, "Exact TOML: "+preview.TargetPath)
	} else if preview.Key.Environment != "" && preview.Key.Target != "" && preview.Key.Target != "default" {
		lines = append(lines, "Selected preset: "+preview.Key.Environment+" / "+preview.Key.Target)
	}
	if preview.TargetTOML != "" {
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

func workspaceOverviewLines(preview viewmodel.SetupPreview, installed bool, profile *viewmodel.Profile) []string {
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

func workspaceProfileInformationLines(profile *viewmodel.Profile) []string {
	if profile == nil {
		return nil
	}
	status := nonempty(profile.RuntimeStatus, "unknown")
	owner := nonempty(profile.Ownership, "unknown")
	endpoint := nonempty(profile.URL, "not configured")
	lines := []string{"Selected MCP profile", "Endpoint URI: " + endpoint, "Live runtime status: " + status, "Runtime ownership: " + owner}
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

func (m *Model) profileForMCPName(name string) *viewmodel.Profile {
	if m.workspace == nil {
		return nil
	}
	key := m.workspace.Key
	if snapshot := m.workspace.ProfileSnapshot; snapshot != nil {
		for i := range snapshot.Profiles {
			profile := snapshot.Profiles[i]
			if profile.Key.Source == key.Source && profile.Key.Package == key.Package && profile.Key.Environment == key.Environment && profile.Key.Target == key.Target && profile.Key.MCP == name {
				copy := profile
				return &copy
			}
		}
	}
	for _, instance := range m.mcps {
		if instance.Key.Source == key.Source && instance.Key.Package == key.Package && instance.Key.Environment == key.Environment && instance.Key.Target == key.Target && instance.Key.MCP == name {
			return &viewmodel.Profile{Key: instance.Key, Name: instance.Name, URL: instance.URL, RuntimeStatus: instance.Status, Ownership: instance.Ownership}
		}
	}
	if m.workspace.Profile != nil && m.workspace.Profile.Key.MCP == name {
		copy := *m.workspace.Profile
		return &copy
	}
	if m.workspace.Profile != nil && m.workspace.Profile.Key.MCP == "" && m.workspace.Preview != nil && len(m.workspace.Preview.MCPDefinitions) == 1 {
		copy := *m.workspace.Profile
		copy.Key.MCP = name
		return &copy
	}
	return nil
}

func (m *Model) legacyWorkspaceMCP() *viewmodel.Profile {
	if m.workspace == nil {
		return nil
	}
	key := m.workspace.Key
	if m.workspace.Profile != nil {
		profile := *m.workspace.Profile
		if profile.Key.Source == key.Source && profile.Key.Package == key.Package && profile.Key.Environment == key.Environment && profile.Key.Target == key.Target {
			return &profile
		}
	}
	matches := []viewmodel.Profile{}
	if snapshot := m.workspace.ProfileSnapshot; snapshot != nil {
		for _, profile := range snapshot.Profiles {
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
		m.workspace.Section = "Agents"
		m.workspace.SectionID = sectionAgentsID
	case "i":
		m.workspace.Section = "Information"
		m.workspace.SectionID = sectionInformationID
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
		var profile *viewmodel.Profile
		if len(m.workspace.Preview.MCPDefinitions) == 1 {
			profile = m.profileForMCPName(m.workspace.Preview.MCPDefinitions[0].Name)
		} else {
			profile = m.legacyWorkspaceMCP()
		}
		if profile == nil {
			m.output = "Runtime observation is unavailable; refresh this target before viewing logs."
			return true, nil
		}
		m.workspace.cacheDraft(m.form.Values())
		return true, m.openProfileLogs(ProfileRow{Key: m.workspace.Key, URL: profile.URL, Name: profile.Name, Status: profile.RuntimeStatus, Profile: profile})
	case "s", "x":
		shortcut := strings.ToLower(stroke)
		if shortcut == "s" {
			if m.workspace.Preview == nil || !m.workspace.Preview.MCP {
				return false, nil
			}
			definitions := m.workspace.Preview.MCPDefinitions
			if len(definitions) > 1 {
				parent := m.workspace.Profile
				row := ProfileRow{}
				if parent != nil {
					row = ProfileRow{Key: parent.Key, Profile: parent, URL: parent.URL, Status: parent.RuntimeStatus}
				}
				if reason := m.profileActionReason(row, "s"); reason != "" {
					m.output = reason
					return true, nil
				}
				m.output = "Choose a specific MCP under Runtime to start it."
				return true, nil
			}
			profile := (*viewmodel.Profile)(nil)
			mcpName := ""
			if len(definitions) == 1 {
				mcpName = definitions[0].Name
				profile = m.profileForMCPName(mcpName)
			} else {
				profile = m.legacyWorkspaceMCP()
				if profile != nil {
					mcpName = profile.Key.MCP
				}
			}
			if profile == nil && m.workspace.Profile != nil {
				parent := *m.workspace.Profile
				profile = &parent
			}
			row := ProfileRow{}
			if profile != nil {
				row = ProfileRow{Key: profile.Key, Profile: profile, URL: profile.URL, Status: profile.RuntimeStatus}
			}
			if reason := m.profileActionReason(row, "s"); reason != "" {
				m.output = reason
				return true, nil
			}
			if m.profileError != nil {
				m.output = "Runtime observation failed: " + m.profileError.Error() + "; refresh this target before starting it."
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
		var profile *viewmodel.Profile
		if len(m.workspace.Preview.MCPDefinitions) == 1 {
			definition = m.workspace.Preview.MCPDefinitions[0]
			profile = m.profileForMCPName(definition.Name)
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
		reason := m.profileActionReason(ProfileRow{Key: profile.Key, Profile: profile, URL: profile.URL, Status: profile.RuntimeStatus}, shortcut)
		if reason != "" {
			m.output = reason
			return true, nil
		}
		m.workspace.cacheDraft(m.form.Values())
		return true, m.run(operation{action: "stop", source: m.workspace.Key.Source, packageID: m.workspace.Key.Package, environment: m.workspace.Key.Environment, target: m.workspace.Key.Target, mcp: definition.Name})
	default:
		return false, nil
	}
	if m.workspace.SectionID != "" {
		m.form.SelectSectionID(m.workspace.SectionID)
	} else {
		m.form.SelectSection(m.workspace.Section)
	}
	m.form.FocusSection()
	return true, nil
}

func (m *Model) configureWorkspaceForm(preview viewmodel.SetupPreview) {
	if m.workspace == nil || m.workspace.Key != preview.Key || m.form == nil {
		return
	}
	stored := preview
	m.workspace.Preview = &stored
	sections := workspaceFormSections(preview, m.form.Definitions(), m.pendingSetupField)
	m.form.SetSections(sections...)
	m.form.SetSectionHeading("Workspace sections")
	if m.workspace.Section == "" {
		m.workspace.Section = "Agents"
		m.workspace.SectionID = sectionAgentsID
		for _, section := range sections {
			if len(section.Fields) > 0 {
				m.workspace.Section = section.Title
				m.workspace.SectionID = section.ID
				break
			}
		}
	}
	if m.workspace.Profile != nil {
		m.workspace.Profile.Key = preview.Key
	}
	m.form.SetSectionContentID(sectionOverviewID, workspaceOverviewLines(preview, m.workspace.Installed, m.workspace.Profile))

	agents := []string{}
	for _, destination := range workspaceDestinations(preview) {
		state := "Not selected"
		if destination.Selected {
			state = "Selected for Save"
		}
		path := destinationDisplayPath(preview, destination)
		agents = append(agents, destination.ID+" · "+state+" · "+path)
		if destination.Detection != "" {
			agents = append(agents, destination.ID+" · Detection: "+destination.Detection)
		}
		if destination.Note != "" {
			agents = append(agents, destination.ID+" · Note: "+destination.Note)
		}
		if destination.DisabledReason != "" {
			agents = append(agents, destination.ID+" · Unavailable for new setup: "+destination.DisabledReason)
		}
	}
	if len(agents) == 0 {
		agents = []string{"No named agent destinations are available."}
	}
	m.form.SetSectionContentID(sectionAgentsID, agents)

	if preview.MCP {
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
			profile := m.profileForMCPName(definition.Name)
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
			runtime = append(runtime, label+" · status: "+status+" · ownership: "+owner+" · endpoint: "+endpoint)
			startProfile := profile
			if startProfile == nil && m.workspace.Profile != nil {
				parent := *m.workspace.Profile
				startProfile = &parent
			}
			startRow := ProfileRow{}
			if startProfile != nil {
				startRow = ProfileRow{Key: startProfile.Key, Profile: startProfile, URL: startProfile.URL, Status: startProfile.RuntimeStatus}
			}
			startDisabled := m.profileActionReason(startRow, "s")
			runtimeActions = append(runtimeActions, forms.FormAction{ID: "start:" + definition.Name, Label: "Start " + definition.Name, Disabled: startDisabled})
			if profile != nil && profile.CanStop {
				runtimeActions = append(runtimeActions, forms.FormAction{ID: "stop:" + definition.Name, Label: "Stop " + definition.Name})
			}
			if profile != nil && profile.RuntimeStatus == "running" {
				runtimeActions = append(runtimeActions, forms.FormAction{ID: "logs:" + definition.Name, Label: "View " + definition.Name + " logs"})
			}
			if profile != nil && profile.URL != "" {
				runtimeActions = append(runtimeActions, forms.FormAction{ID: "check-connection:" + definition.Name, Label: "Check " + definition.Name + " connection"})
			}
			logs = append(logs, label+" · "+status)
		}
		if len(runtime) == 0 {
			runtime = []string{"Runtime definitions are unavailable in this setup preview."}
		}
		if len(logs) == 0 {
			logs = []string{"No runtime logs are available."}
		}
		m.form.SetSectionContentID(sectionRuntimeID, runtime)
		m.form.SetSectionActionsID(sectionRuntimeID, runtimeActions...)
		m.form.SetSectionContentID(sectionLogsID, logs)
	}
	information := workspaceInformationLines(preview, max(20, m.form.PaneWidth()/2))
	if preview.MCP {
		if profileInformation := workspaceProfileInformationLines(m.workspace.Profile); len(profileInformation) > 0 {
			information = append(profileInformation, information...)
		}
	}
	m.form.SetSectionContentID(sectionInformationID, information)
	actions := []forms.FormAction{}
	if !m.workspace.ObservedOnly {
		actions = append(actions, forms.FormAction{ID: "agents", Label: "Configure agent destinations"})
	}
	m.form.SetSectionActionsID(sectionOverviewID, actions...)
	if m.workspace.SectionID != "" && !m.form.HasSectionID(m.workspace.SectionID) {
		m.workspace.SectionID = ""
	}
	if m.workspace.SectionID == "" && !m.form.HasSection(m.workspace.Section) {
		m.workspace.Section = "Overview"
		m.workspace.SectionID = sectionOverviewID
	}
	if m.workspace.SectionID != "" {
		m.form.SelectSectionID(m.workspace.SectionID)
	} else {
		m.form.SelectSection(m.workspace.Section)
	}
	m.form.FocusSection()
}

func formSectionFields(sections []forms.FormSection, title string) []string {
	for _, section := range sections {
		if section.Title == title {
			return section.Fields
		}
	}
	return nil
}

func workspaceFormSections(preview viewmodel.SetupPreview, defs []catalog.Input, destinationField string) []forms.FormSection {
	known := make(map[string]bool, len(defs))
	ordered := make([]string, 0, len(defs))
	hasDestination := false
	for _, def := range defs {
		if def.Name == destinationField {
			hasDestination = true
			continue
		}
		known[def.Name] = true
		ordered = append(ordered, def.Name)
	}
	sections := []forms.FormSection{{ID: sectionOverviewID, Title: "Overview"}}
	if preview.HasManifestUI {
		for _, section := range preview.Sections {
			fields := make([]string, 0, len(section.Fields))
			for _, name := range section.Fields {
				if known[name] {
					fields = append(fields, name)
				}
			}
			if len(fields) > 0 {
				sections = append(sections, forms.FormSection{ID: "package:" + section.ID, Title: section.Title, Fields: fields})
			}
		}
	} else if len(ordered) > 0 {
		sections = append(sections, forms.FormSection{ID: "tool:inputs", Title: "Inputs", Fields: ordered})
	}
	var agentFields []string
	if hasDestination {
		agentFields = []string{destinationField}
	}
	sections = append(sections, forms.FormSection{ID: sectionAgentsID, Title: "Agents", Fields: agentFields})
	if preview.MCP {
		sections = append(sections, forms.FormSection{ID: sectionRuntimeID, Title: "Runtime"}, forms.FormSection{ID: sectionLogsID, Title: "Logs"})
	}
	sections = append(sections, forms.FormSection{ID: sectionInformationID, Title: "Information"})
	return sections
}
