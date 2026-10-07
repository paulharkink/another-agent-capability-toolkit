package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/forms"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

// workspaceState is the cached editor context for one exact target. It keeps
// the invoking layer and profile observation beside the target draft so later
// route handling can restore Back without losing the parent selection.
type workspaceState struct {
	Key               state.Key
	Section           string
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
	m.form.SetSectionContent("Overview", []string{
		"Package configuration unavailable: this package is absent from the local catalog.",
		"Source: " + key.Source + " · Package: " + key.Package,
		"Runtime: " + nonempty(profile.RuntimeStatus, "unknown") + " · Ownership: " + nonempty(profile.Ownership, "unknown"),
		"No package settings or install action are available for this observation.",
		"g · Manage named agent registrations",
	})
	m.form.SetSectionContent("Endpoint", []string{
		"Observed endpoint: " + nonempty(profile.URL, "not reported"),
		"Transport: " + nonempty(profile.Transport, "not reported"),
		"› Check connection · Enter (or c)",
	})
	m.form.SetSectionContent("Agents", []string{
		"Registered named agents: " + registered,
		"› Configure named registrations · Enter (or g)",
	})
	m.form.SetSectionContent("Information", []string{
		"Observed target identity: " + key.Source + " / " + key.Package + " / " + nonempty(key.Environment, "(none)") + " / " + nonempty(key.Target, "default"),
		"Runtime status: " + nonempty(profile.RuntimeStatus, "unknown"),
		"Runtime ownership: " + nonempty(profile.Ownership, "unknown"),
		"This workspace reflects observed profile facts; it does not imply package installation or local runtime ownership.",
	})
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
		"Target information",
		fmt.Sprintf("Capability: %s · package %s", name, preview.Key.Package),
		fmt.Sprintf("Source identity: %s · checkout %s", preview.Key.Source, preview.SourceRoot),
		"Environment: " + preview.Key.Environment + " · Target: " + preview.Key.Target,
		"Exact TOML: " + preview.TargetPath,
	}
	if preview.TargetPath == "" {
		lines[4] = "Exact TOML: no environment file selected"
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
	lines = append(lines, "Agent destinations · planned configuration files")
	if len(preview.Destinations) == 0 {
		lines = append(lines, "No named agent destinations are available")
	} else {
		for _, destination := range preview.Destinations {
			selection := "not selected"
			if destination.Selected {
				selection = "selected for apply"
			}
			path := destination.ConfigPath
			if path == "" {
				path = destination.Path
			}
			lines = append(lines, splitDisplayLine(fmt.Sprintf("%s · %s · %s", destination.ID, selection, path), inner)...)
			if destination.Detection != "" {
				lines = append(lines, splitDisplayLine("Detection: "+destination.Detection, inner)...)
			}
			if destination.Note != "" {
				lines = append(lines, splitDisplayLine("Note: "+destination.Note, inner)...)
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

func (m *Model) workspaceOverviewAction(stroke string) (bool, tea.Cmd) {
	if m.workspace == nil || m.form == nil {
		return false, nil
	}
	switch strings.ToLower(stroke) {
	case "c":
		m.workspace.Section = "Connection"
	case "a":
		m.workspace.Section = "Authentication"
	case "d":
		m.workspace.Section = "Databases"
	case "i":
		m.workspace.Section = "Information"
	case "l":
		m.workspace.Section = "Logs"
		m.form.SelectSection(m.workspace.Section)
		m.form.FocusSection()
		profile := m.workspace.Profile
		if profile == nil {
			m.output = "Runtime observation is unavailable; refresh this target before viewing logs."
			return true, nil
		}
		m.workspace.cacheDraft(m.form.Values())
		return true, m.openProfileLogs(ProfileRow{Key: m.workspace.Key, URL: profile.URL, Name: profile.Name, Status: profile.RuntimeStatus, Profile: profile})
	case "g", "r":
		profile := m.workspace.Profile
		if profile == nil {
			m.output = "Agent registration is unavailable without an observed target profile"
			return true, nil
		}
		row := ProfileRow{Key: m.workspace.Key, URL: profile.URL, Name: profile.Name, Status: profile.RuntimeStatus, Profile: profile}
		m.workspace.cacheDraft(m.form.Values())
		if strings.ToLower(stroke) == "r" {
			m.removeRegistrationForm(row)
		} else {
			m.registrationForm(row)
		}
		return true, nil
	case "s", "x":
		shortcut := strings.ToLower(stroke)
		if shortcut == "s" {
			if m.workspace.ObservedOnly {
				m.output = "This observed target is read-only; use its existing endpoint and agent registration controls."
				return true, nil
			}
			if m.workspace.Preview == nil || (!m.workspace.Preview.MCP && (m.workspace.Profile == nil || m.workspace.Profile.Ownership == "local")) {
				return false, nil
			}
			if m.profileError != nil {
				m.output = "Runtime observation failed: " + m.profileError.Error() + "; refresh this target before building and starting it."
				return true, nil
			}
			if profile := m.workspace.Profile; profile != nil && profile.Ownership != "local" {
				m.output = nonempty(profile.StartDisabledReason, "Runtime owner is unknown")
				return true, nil
			}
			m.workspace.cacheDraft(m.form.Values())
			return true, m.applySetup(m.form.Values())
		}
		if m.workspace.Profile == nil {
			m.output = "No locally owned MCP runtime is available to stop."
			return true, nil
		}
		reason := m.profileActionReason(ProfileRow{Key: m.workspace.Key, Profile: m.workspace.Profile, URL: m.workspace.Profile.URL, Status: m.workspace.Profile.RuntimeStatus}, shortcut)
		if reason != "" {
			m.output = reason
			return true, nil
		}
		m.workspace.cacheDraft(m.form.Values())
		return true, m.run(operation{action: "stop", source: m.workspace.Key.Source, packageID: m.workspace.Key.Package, environment: m.workspace.Key.Environment, target: m.workspace.Key.Target})
	default:
		return false, nil
	}
	m.form.SelectSection(m.workspace.Section)
	m.form.FocusSection()
	return true, nil
}

func (m *Model) configureWorkspaceForm(preview viewmodel.SetupPreview) {
	if m.workspace == nil || m.workspace.Key != preview.Key || m.form == nil {
		return
	}
	stored := preview
	m.workspace.Preview = &stored
	sections := workspaceFormSections(preview.Key.Package, m.form.Definitions(), m.pendingSetupField)
	m.form.SetSections(sections...)
	m.form.SetSectionHeading("Workspace sections")
	if m.workspace.Section == "" {
		m.workspace.Section = "Overview"
	}
	if m.workspace.Profile != nil {
		m.workspace.Profile.Key = preview.Key
	}
	m.form.SetSectionContent("Overview", workspaceOverviewLines(preview, m.workspace.Installed, m.workspace.Profile))

	connection := []string{"Configure endpoint and listen settings for this target."}
	if len(formSectionFields(m.form.Sections(), "Connection")) == 0 {
		connection = []string{"No editable connection inputs are supplied by this package."}
	}
	m.form.SetSectionContent("Connection", connection)

	credentialState := strings.TrimSpace(preview.CredentialState)
	if credentialState == "" {
		credentialState = "unknown"
	}
	authentication := []string{"Imported credentials: " + credentialState}
	if note := strings.TrimSpace(preview.CredentialNote); note != "" {
		authentication = append(authentication, note)
	} else {
		authentication = append(authentication, "This is an observation of managed credential material; authentication health has not been checked.")
	}
	m.form.SetSectionContent("Authentication", authentication)

	hasDatabaseChoices := false
	for _, input := range preview.Inputs {
		name := strings.ToLower(input.Definition.Name + " " + input.Definition.Label + " " + input.Definition.OptionsFrom)
		if strings.Contains(name, "database") || strings.Contains(name, "dbms") {
			if len(input.Definition.Options) > 0 || input.Definition.OptionsFrom != "" {
				hasDatabaseChoices = true
			}
		}
	}
	if !hasDatabaseChoices {
		database := []string{"Database access is optional.", "This target supplies no database choices."}
		if preview.TargetPath != "" {
			database = append(database, "Target TOML: "+preview.TargetPath)
		} else {
			database = append(database, "Target TOML: no environment preset is selected")
		}
		m.form.SetSectionContent("Databases", database)
	}

	agents := []string{}
	for _, destination := range preview.Destinations {
		state := "Not selected"
		if destination.Selected {
			state = "Selected for Save"
		}
		path := destination.ConfigPath
		if path == "" {
			path = destination.Path
		}
		agents = append(agents, destination.ID+" · "+state+" · "+path)
	}
	if len(agents) == 0 {
		agents = []string{"No named agent destinations are available."}
	}
	m.form.SetSectionContent("Agents", agents)

	logs := []string{"No MCP container has been created for this target."}
	if m.workspace.Profile != nil {
		switch m.workspace.Profile.RuntimeStatus {
		case "running":
			logs = []string{"MCP runtime is running. Logs belong to this target and closing this section does not stop it."}
		case "never-started", "missing":
			logs = []string{"No MCP container has been created for this target.", "Configure or Start this locally owned target to create a runtime."}
		default:
			logs = []string{"MCP runtime status: " + m.workspace.Profile.RuntimeStatus, "Logs are available only for an observed runtime."}
		}
	}
	m.form.SetSectionContent("Logs", logs)
	information := workspaceInformationLines(preview, max(20, m.form.PaneWidth()/2))
	if profileInformation := workspaceProfileInformationLines(m.workspace.Profile); len(profileInformation) > 0 {
		information = append(profileInformation, information...)
	}
	m.form.SetSectionContent("Information", information)
	applyLabel := "Save and apply configuration"
	applyDisabled := ""
	if preview.MCP {
		applyLabel = "Save and apply · build, start, register"
		if profile := m.workspace.Profile; profile != nil && profile.Ownership != "local" {
			applyLabel = "Save and apply configuration"
			if strings.TrimSpace(profile.URL) == "" {
				applyDisabled = nonempty(profile.StartDisabledReason, "Runtime owner is unknown; no endpoint is available for a safe registration.")
			}
		}
	}
	stopDisabled := "No locally owned MCP runtime is available to stop."
	if profile := m.workspace.Profile; profile != nil {
		if profile.StopDisabledReason != "" {
			stopDisabled = profile.StopDisabledReason
		}
		if profile.CanStop {
			stopDisabled = ""
		}
	}
	actions := []forms.FormAction{}
	if !m.workspace.ObservedOnly {
		actions = append(actions, forms.FormAction{ID: "apply", Label: applyLabel, Disabled: applyDisabled})
	}
	if preview.MCP && !m.workspace.ObservedOnly {
		registrationDisabled := "No observed MCP endpoint is available for registration."
		if profile := m.workspace.Profile; profile != nil {
			registrationDisabled = nonempty(profile.RegistrationDisabledReason, registrationDisabled)
			if profile.CanConfigureRegistrations {
				registrationDisabled = ""
			}
		}
		actions = append(actions, forms.FormAction{ID: "registrations", Label: "Manage agent registrations", Disabled: registrationDisabled})
		actions = append(actions, forms.FormAction{ID: "stop", Label: "Stop MCP", Disabled: stopDisabled})
		if profile := m.workspace.Profile; profile != nil && profile.Ownership != "local" {
			startDisabled := nonempty(profile.StartDisabledReason, "Runtime owner is unknown")
			actions = append(actions, forms.FormAction{ID: "start", Label: "Build and start MCP", Disabled: startDisabled})
		}
	}
	m.form.SetSectionActions("Overview", actions...)
	if !m.formHasSection(m.workspace.Section) {
		m.workspace.Section = "Overview"
	}
	m.form.SelectSection(m.workspace.Section)
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

func (m *Model) formHasSection(title string) bool {
	return m.form.HasSection(title)
}

// workspaceSections groups editable inputs by the task they represent. Package
// inputs remain the source of truth; these names only shape the TUI navigation.
func workspaceSections(packageID string, defs []catalog.Input, destinationField string) []forms.FormSection {
	fields := map[string][]string{}
	for _, def := range defs {
		name := strings.ToLower(def.Name + " " + def.Label)
		section := "Inputs"
		switch {
		case def.Name == destinationField:
			section = "Destinations"
		case strings.Contains(name, "database") || strings.Contains(name, "dbms") || strings.Contains(def.OptionsFrom, "dbms"):
			section = "Databases"
		case strings.Contains(name, "tenant") || strings.Contains(name, "subscription"):
			section = "Azure"
		case strings.Contains(name, "datasource"):
			section = "Datasource"
		case workspaceAuthenticationInput(def):
			section = "Authentication"
		case workspaceConnectionInput(def):
			section = "Connection"
		case packageID == "cluster-inspector":
			section = "Connection"
		}
		fields[section] = append(fields[section], def.Name)
	}
	order := []string{"Connection", "Authentication", "Databases", "Datasource", "Azure", "Inputs", "Destinations"}
	sections := make([]forms.FormSection, 0, len(order))
	for _, title := range order {
		if len(fields[title]) > 0 {
			sections = append(sections, forms.FormSection{Title: title, Fields: fields[title]})
		}
	}
	return sections
}

func workspaceFormSections(packageID string, defs []catalog.Input, destinationField string) []forms.FormSection {
	dynamic := workspaceSections(packageID, defs, destinationField)
	fields := make(map[string][]string, len(dynamic))
	for _, section := range dynamic {
		title := section.Title
		if title == "Destinations" {
			title = "Agents"
		}
		fields[title] = section.Fields
	}
	sections := []forms.FormSection{
		{Title: "Overview"},
		{Title: "Connection", Fields: fields["Connection"]},
		{Title: "Authentication", Fields: fields["Authentication"]},
		{Title: "Databases", Fields: fields["Databases"]},
	}
	for _, title := range []string{"Datasource", "Azure", "Inputs"} {
		if len(fields[title]) > 0 {
			sections = append(sections, forms.FormSection{Title: title, Fields: fields[title]})
		}
	}
	sections = append(sections,
		forms.FormSection{Title: "Agents", Fields: fields["Agents"]},
		forms.FormSection{Title: "Logs"},
		forms.FormSection{Title: "Information"},
	)
	return sections
}

func workspaceAuthenticationInput(def catalog.Input) bool {
	name := strings.ToLower(def.Name + " " + def.Label)
	if def.Type == "secret" || def.ExclusiveGroup != "" {
		return true
	}
	for _, word := range []string{"auth", "token", "credential", "kubeconfig", "cookie", "session", "oauth", "vault"} {
		if strings.Contains(name, word) {
			return true
		}
	}
	return false
}

func workspaceConnectionInput(def catalog.Input) bool {
	name := strings.ToLower(def.Name + " " + def.Label)
	for _, word := range []string{"url", "host", "address", "endpoint", "port", "listen"} {
		if strings.Contains(name, word) {
			return true
		}
	}
	return false
}

// configureWorkspaceAuthentication keeps Grafana controls for the selected
// authentication mode conditional, using the package's existing auth_mode
// choice and credential names rather than changing package semantics.
func configureWorkspaceAuthentication(form *forms.FormModel, preview viewmodel.SetupPreview) {
	if preview.Key.Package != "grafana-inspector" {
		return
	}
	available := make(map[string]bool, len(preview.Inputs))
	for _, input := range preview.Inputs {
		available[input.Definition.Name] = input.Editable
	}
	for _, name := range []string{"token", "vault_addr", "vault_path", "vault_key"} {
		if available[name] {
			form.SetConditional(name, "auth_mode", "api_token")
		}
	}
	for _, name := range []string{"grafana_session", "session_expiry", "oauth_refresh", "refresh_cookie_name"} {
		if available[name] {
			form.SetConditional(name, "auth_mode", "session_cookie")
		}
	}
}
