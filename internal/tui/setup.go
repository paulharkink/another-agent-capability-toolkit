package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/forms"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

type setupBackend interface {
	UISetupPreview(context.Context, viewmodel.SetupRequest) (viewmodel.SetupPreview, error)
	UIInstall(context.Context, viewmodel.SetupInstallRequest) (viewmodel.OperationResult, error)
}

type setupPreviewMsg struct {
	preview viewmodel.SetupPreview
	err     error
}

func (m *Model) beginSetup(sourceID, packageID, environment, target string) tea.Cmd {
	return m.openTargetWorkspace(viewmodel.SetupRequest{SourceID: sourceID, PackageID: packageID, Environment: environment, Target: target}, "Connection")
}

func (m *Model) openTargetWorkspace(request viewmodel.SetupRequest, selectedSection string) tea.Cmd {
	backend, ok := m.backend.(setupBackend)
	if !ok {
		m.output = "Unified setup service unavailable"
		return nil
	}
	key := state.Key{Source: request.SourceID, Package: request.PackageID, Environment: request.Environment, Target: request.Target}
	priorDraft := map[string]any(nil)
	if m.workspace != nil && m.workspace.Key == key {
		priorDraft = m.workspace.cachedDraft()
	}
	m.retryOperation = func() tea.Cmd { return m.openTargetWorkspace(request, selectedSection) }
	workspace := &workspaceState{
		Key: key, Section: selectedSection, InvokingView: m.view, Active: true,
		InvokingSelection: m.workspaceInvokingSelection(), Draft: priorDraft,
		Profile: m.profileForWorkspace(key), ProfileSnapshot: copyProfileSnapshot(m.profileSnapshot),
	}
	workspace.Existing = workspace.Profile != nil || m.targetHasSavedRecord(key)
	workspace.Installed = m.targetHasInstallRecord(key)
	m.workspace = workspace
	m.home.Modal = nil
	m.management.Modal = ""
	m.busy = true
	m.action = "load setup"
	ctx := m.ctx
	return func() tea.Msg {
		preview, err := backend.UISetupPreview(ctx, request)
		return setupPreviewMsg{preview: preview, err: err}
	}
}

func (m *Model) workspaceInvokingSelection() int {
	if m.view == "Environments" {
		return m.management.TargetIndex
	}
	if m.view == "Catalog" {
		return m.home.Context.Index
	}
	return m.selected
}

func (m *Model) targetHasSavedRecord(key state.Key) bool {
	for _, installation := range m.inventory {
		if installation.Key == key && installation.Component != "runtime" && installation.Component != "skill" {
			return true
		}
	}
	return false
}

func (m *Model) targetHasInstallRecord(key state.Key) bool {
	for _, installation := range m.inventory {
		if installation.Key == key && (installation.Component == "runtime" || installation.Component == "skill") {
			return true
		}
	}
	return false
}

func (m *Model) profileForWorkspace(key state.Key) *viewmodel.Profile {
	if m.profileSnapshot != nil {
		for index := range m.profileSnapshot.Profiles {
			profile := m.profileSnapshot.Profiles[index]
			if profile.Key == key {
				profile.RegisteredAgents = append([]string(nil), profile.RegisteredAgents...)
				return &profile
			}
		}
	}
	profile := &viewmodel.Profile{Key: key, RuntimeStatus: "unknown", Ownership: "unknown"}
	for _, installation := range m.inventory {
		if installation.Key == key && installation.Component == "mcp" && installation.AgentID != "" {
			profile.RegisteredAgents = append(profile.RegisteredAgents, installation.AgentID)
			if profile.URL == "" {
				profile.URL = installation.URL
			}
		}
	}
	if len(profile.RegisteredAgents) > 0 || profile.URL != "" {
		return profile
	}
	for _, candidate := range m.profiles() {
		if candidate.Key == key {
			if candidate.Profile != nil {
				return candidate.Profile
			}
			profile := &viewmodel.Profile{Key: key, Name: candidate.Name, URL: candidate.URL, RuntimeStatus: candidate.Status, Ownership: candidate.Instance.Ownership}
			if profile.Ownership == "local" {
				switch strings.ToLower(profile.RuntimeStatus) {
				case "never-started", "missing", "exited":
					profile.CanStart = true
				case "running":
					profile.CanStop = true
				default:
					profile.StartDisabledReason = "Runtime state does not permit Start"
					profile.StopDisabledReason = "Runtime state does not permit Stop"
				}
			} else if profile.Ownership != "" {
				profile.StartDisabledReason = "Runtime belongs to another installation"
				profile.StopDisabledReason = "Runtime belongs to another installation"
			} else {
				profile.Ownership = "unknown"
				profile.StartDisabledReason = "Runtime owner is unknown"
				profile.StopDisabledReason = "Runtime owner is unknown"
			}
			return profile
		}
	}
	return nil
}

func copyProfileSnapshot(snapshot *viewmodel.ProfileSnapshot) *viewmodel.ProfileSnapshot {
	if snapshot == nil {
		return nil
	}
	copySnapshot := *snapshot
	copySnapshot.Profiles = append([]viewmodel.Profile(nil), snapshot.Profiles...)
	for index := range copySnapshot.Profiles {
		copySnapshot.Profiles[index].RegisteredAgents = append([]string(nil), snapshot.Profiles[index].RegisteredAgents...)
	}
	return &copySnapshot
}

type setupRetryDraft struct {
	preview          viewmodel.SetupPreview
	values           map[string]any
	destinationField string
	origin           string
	section          string
	step             string
	failure          string
}

func (m *Model) openSetupForm(preview viewmodel.SetupPreview) {
	m.openSetupFormWithValues(preview, nil)
}

func (m *Model) openSetupFormWithValues(preview viewmodel.SetupPreview, overrides map[string]any) {
	defs := make([]catalog.Input, 0, len(preview.Inputs)+1)
	values := make(map[string]any, len(preview.Inputs)+1)
	destinationField := "__aact_destinations"
	for {
		collision := false
		for _, input := range preview.Inputs {
			if input.Definition.Name == destinationField {
				collision = true
				break
			}
		}
		if !collision {
			break
		}
		destinationField = "_" + destinationField
	}
	groups := map[string][]int{}
	fixedGroups := map[string]string{}
	activeAuth := ""
	usedNames := map[string]bool{destinationField: true}
	for i, input := range preview.Inputs {
		usedNames[input.Definition.Name] = true
		if !input.Editable && input.Definition.ExclusiveGroup != "" && input.HasValue {
			if value, ok := input.Value.(string); ok && strings.TrimSpace(value) != "" {
				fixedGroups[input.Definition.ExclusiveGroup] = input.Definition.Label
				if fixedGroups[input.Definition.ExclusiveGroup] == "" {
					fixedGroups[input.Definition.ExclusiveGroup] = input.Definition.Name
				}
			}
		}
		if input.Editable && input.Definition.ExclusiveGroup != "" {
			groups[input.Definition.ExclusiveGroup] = append(groups[input.Definition.ExclusiveGroup], i)
			name := strings.ToLower(input.Definition.Name + " " + input.Definition.Label)
			if strings.Contains(name, "token") || strings.Contains(name, "kubeconfig") {
				if input.HasValue && strings.TrimSpace(fmt.Sprint(input.Value)) != "" {
					activeAuth = input.Definition.Name
				}
			}
		}
	}
	for _, input := range preview.Inputs {
		if !input.Editable {
			continue
		}
		def := input.Definition
		if preview.Key.Package == "cluster-inspector" {
			switch def.Name {
			case "host":
				def.Label = "Listen address"
			case "local_port":
				def.Label = "Listen port"
			}
		}
		if preview.Key.Package == "grafana-inspector" && def.Name == "auth_mode" {
			def.Label = "Authentication mode"
		}
		if def.OptionsFrom != "" && input.HasValue {
			def = withUnavailableSavedChoices(def, input.Value)
		}
		label := def.Label
		if label == "" {
			label = def.Name
		}
		if input.HasValue {
			values[def.Name] = input.Value
			if setupDisplayValue(input) {
				origin := input.Provenance
				def.Label = label + " [" + origin + "]"
			}
		}
		defs = append(defs, def)
	}
	choices := make([]catalog.Choice, 0, len(preview.Destinations))
	selected := []string{}
	requiresNamedDestination := false
	for _, pkg := range m.catalog {
		if pkg.ID == preview.Key.Package && pkg.MCP != nil {
			requiresNamedDestination = true
			break
		}
	}
	for _, destination := range preview.Destinations {
		if requiresNamedDestination && strings.EqualFold(destination.ID, "all") {
			continue
		}
		name := destination.ID
		switch strings.ToLower(name) {
		case "all":
			name = "All"
		case "codex":
			name = "Codex"
		case "opencode":
			name = "OpenCode"
		case "claude", "claude-code":
			name = "Claude Code"
		}
		path := destination.ConfigPath
		if path == "" {
			path = destination.Path
		}
		label := name
		if path != "" {
			label += " — " + path
		}
		choices = append(choices, catalog.Choice{Value: destination.ID, Label: label})
		if destination.Selected {
			selected = append(selected, destination.ID)
		}
	}
	defs = append(defs, catalog.Input{Name: destinationField, Label: "Destinations", Type: "multichoice", Options: choices, Required: true})
	values[destinationField] = selected
	for name, value := range overrides {
		values[name] = value
	}
	if m.workspace != nil && m.workspace.Key == preview.Key {
		for name, value := range m.workspace.cachedDraft() {
			values[name] = value
		}
	}
	m.pendingSetup = &preview
	m.pendingSetupField = destinationField
	m.form = forms.NewForm(m.ctx, defs, values)
	if m.workspace != nil && m.workspace.Key == preview.Key {
		m.form.SetBackNavigation(true)
	}
	configureWorkspaceAuthentication(m.form, preview)
	name := preview.PackageName
	if name == "" {
		name = preview.Key.Package
	}
	title := "Install · " + name
	if preview.Key.Environment != "" && preview.Key.Target != "" {
		title += " · " + preview.Key.Environment + " / " + preview.Key.Target
	} else if preview.Key.Environment != "" {
		title += " · " + preview.Key.Environment
	} else if preview.Key.Target != "" && preview.Key.Target != "default" {
		title += " · " + preview.Key.Target
	}
	m.form.SetTitle(title)
	sections := []forms.FormSection{}
	hasClusterAuth := false
	for _, input := range preview.Inputs {
		name := strings.ToLower(input.Definition.Name + " " + input.Definition.Label)
		if strings.Contains(name, "token") {
			for _, candidate := range preview.Inputs {
				candidateName := strings.ToLower(candidate.Definition.Name + " " + candidate.Definition.Label)
				if strings.Contains(candidateName, "kubeconfig") {
					hasClusterAuth = true
					break
				}
			}
		}
	}
	isClusterInspector := preview.Key.Package == "cluster-inspector" && hasClusterAuth
	sections = workspaceSections(preview.Key.Package, defs, destinationField)
	m.form.SetSections(sections...)
	if isClusterInspector {
		m.form.SetSectionHeading("Setup sections")
		m.form.SelectSection("Authentication")
	}
	for group, indices := range groups {
		if fixedGroups[group] != "" || len(indices) < 2 {
			continue
		}
		names := make([]string, 0, len(indices))
		for _, index := range indices {
			names = append(names, preview.Inputs[index].Definition.Name)
		}
		m.form.SetExclusiveFields(names...)
	}
	environment := preview.Key.Environment
	if environment == "" {
		environment = "No environment file"
	}
	target := preview.Key.Target
	if target == "" {
		target = "Package default"
	}
	runtime := "New setup"
	if m.view == "Catalog" && m.home.Focus == ProfilesPane {
		if profiles := m.profiles(); len(profiles) > 0 {
			runtime = profiles[m.home.Profiles.Index].Status
		}
	}
	m.form.SetContext("Environment: " + environment + " · Target: " + target + " · Runtime: " + runtime)
	for _, input := range preview.Inputs {
		if !input.Editable {
			continue
		}
		if fixed := fixedGroups[input.Definition.ExclusiveGroup]; fixed != "" {
			m.form.SetDisabled(input.Definition.Name, fixed+" is fixed by target")
			continue
		}
		if setupDisplayValue(input) && input.ProvenancePath != "" {
			m.form.SetHint(input.Definition.Name, setupProvenanceHint(input.Provenance, input.ProvenancePath))
		}
		if input.Definition.ExclusiveGroup != "" && input.Definition.Type == "file" {
			m.form.SetHint(input.Definition.Name, "Import source; AACT uses managed credential material at runtime")
		}
		if isClusterInspector && input.Definition.ExclusiveGroup != "" {
			cue := "Type here to switch to " + input.Definition.Label
			if activeAuth == input.Definition.Name {
				cue = "Active method"
			} else if activeAuth == "" {
				cue = "Enter a " + input.Definition.Label
			}
			if input.Definition.Type == "file" {
				if activeAuth == input.Definition.Name {
					cue += " · imported, not a live path"
				} else if activeAuth == "" {
					cue = "Enter a source kubeconfig path"
				} else {
					cue = "Type a path to switch to kubeconfig"
				}
				cue += " · Import source; AACT uses managed credential material at runtime"
			}
			m.form.SetHint(input.Definition.Name, cue)
		}
	}
	if m.workspace != nil && m.workspace.Key == preview.Key {
		m.configureWorkspaceForm(preview)
	}
	_, _, width, height, ok := m.setupOverlayBounds()
	if !ok {
		width, height = m.width, m.height
	}
	m.form.Update(tea.WindowSizeMsg{Width: width, Height: height})
}

func setupDisplayValue(input viewmodel.SetupInput) bool {
	if !input.HasValue {
		return false
	}
	if value, ok := input.Value.(string); ok {
		return strings.TrimSpace(value) != ""
	}
	return true
}

func setupProvenanceHint(origin, path string) string {
	file := filepath.Base(path)
	switch origin {
	case "saved":
		return "Saved override · editable"
	case "environment":
		return "Environment default · editable · " + file
	case "source":
		return "Source default · editable · " + file
	case "package":
		return "Package default · editable · " + file
	default:
		return origin + " · editable · " + file
	}
}

func withUnavailableSavedChoices(def catalog.Input, value any) catalog.Input {
	known := map[string]bool{}
	for _, option := range def.Options {
		known[option.Value] = true
	}
	selected := []string{}
	switch entries := value.(type) {
	case []string:
		selected = entries
	case []any:
		for _, entry := range entries {
			if text, ok := entry.(string); ok {
				selected = append(selected, text)
			}
		}
	}
	for _, entry := range selected {
		if known[entry] {
			continue
		}
		label := "Unavailable saved choice " + entry + " — deselect to remove"
		if entry == "" {
			label = "Empty saved entry — deselect to remove"
		}
		def.Options = append(def.Options, catalog.Choice{Value: entry, Label: label})
		known[entry] = true
	}
	return def
}

func (m *Model) applySetup(values map[string]any) tea.Cmd {
	backend, ok := m.backend.(setupBackend)
	if !ok || m.pendingSetup == nil {
		m.output = "Unified setup service unavailable"
		return nil
	}
	preview := *m.pendingSetup
	destinationField := m.pendingSetupField
	section := ""
	if m.workspace != nil && m.workspace.Key == preview.Key {
		section = m.workspace.Section
	} else if m.form != nil {
		section = m.form.SectionTitle()
	}
	origin := m.view
	m.pendingSetup = nil
	destinations, _ := values[destinationField].([]string)
	m.pendingSetupField = ""
	m.setupRetry = &setupRetryDraft{preview: preview, values: cloneSetupValues(values), destinationField: destinationField, origin: origin, section: section}
	m.setupOperationPending = true
	inputs := make(map[string]any, len(preview.Inputs))
	for _, input := range preview.Inputs {
		if !input.Editable {
			continue
		}
		if value, exists := values[input.Definition.Name]; exists {
			inputs[input.Definition.Name] = value
		}
	}
	request := viewmodel.SetupInstallRequest{
		SetupRequest: viewmodel.SetupRequest{SourceID: preview.Key.Source, PackageID: preview.Key.Package, Environment: preview.Key.Environment, Target: preview.Key.Target},
		Inputs:       inputs, DestinationIDs: destinations,
	}
	if workspace := m.workspace; workspace != nil && workspace.Active && workspace.Key == preview.Key && workspace.Profile != nil && workspace.Profile.Ownership != "local" {
		// Selecting an observed foreign/unknown profile is the user's explicit
		// choice of registration endpoint. Preserve that endpoint for the
		// registration-only install path; never derive one from runtime inventory.
		request.ExternalURL = strings.TrimSpace(workspace.Profile.URL)
	}
	m.busy = true
	m.action = "install"
	m.home.Modal = nil
	m.setupOperationID++
	setupID := m.setupOperationID
	ctx := m.ctx
	request.Inputs = cloneSetupValues(request.Inputs)
	request.DestinationIDs = append([]string(nil), request.DestinationIDs...)
	return func() tea.Msg {
		result, err := backend.UIInstall(ctx, request)
		structured := result
		structured.SavedApplicable = true
		lines := []string{}
		if result.Saved {
			lines = append(lines, "Inputs saved")
		}
		if result.Message != "" {
			lines = append(lines, result.Message)
		}
		if len(result.Errors) > 0 || err != nil {
			if result.Step != "" {
				lines = append(lines, "Step: "+result.Step)
			}
			if result.Target != "" {
				lines = append(lines, "Target: "+result.Target)
			}
		}
		for _, change := range result.Changes {
			lines = append(lines, fmt.Sprintf("%s: %s configured", change.AgentID, change.Component))
		}
		lines = append(lines, result.Errors...)
		return operationMsg{origin: origin, output: strings.Join(lines, "\n"), err: err, failed: len(result.Errors) > 0, step: result.Step, target: result.Target, setupID: setupID, result: &structured}
	}
}

func cloneSetupValues(values map[string]any) map[string]any {
	copyValues := make(map[string]any, len(values))
	for name, value := range values {
		if list, ok := value.([]string); ok {
			copyValues[name] = append([]string(nil), list...)
		} else {
			copyValues[name] = value
		}
	}
	return copyValues
}

func setupTargetLabel(key state.Key) string {
	parts := []string{}
	if key.Source != "" && key.Package != "" {
		parts = append(parts, key.Source+" / "+key.Package)
	} else if key.Package != "" {
		parts = append(parts, key.Package)
	}
	if key.Environment != "" || key.Target != "" {
		environment, target := key.Environment, key.Target
		if environment == "" {
			environment = "default environment"
		}
		if target == "" {
			target = "default target"
		}
		parts = append(parts, environment+" / "+target)
	}
	return strings.Join(parts, " · ")
}
