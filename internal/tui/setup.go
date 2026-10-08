package tui

import (
	"context"
	"fmt"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/agents"
	"path/filepath"
	"strings"
	"time"

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

type setupProgressEmitter struct {
	ctx    context.Context
	events chan viewmodel.OperationProgress
	done   <-chan struct{}
}

func (e setupProgressEmitter) emit(progress viewmodel.OperationProgress) {
	select {
	case <-e.done:
		return
	case <-e.ctx.Done():
		return
	default:
	}
	select {
	case e.events <- progress:
	default:
		select {
		case <-e.events:
		default:
		}
		select {
		case e.events <- progress:
		default:
		}
	}
}

func (m *Model) beginSetup(sourceID, packageID, environment, target string) tea.Cmd {
	return m.openTargetWorkspace(viewmodel.SetupRequest{SourceID: sourceID, PackageID: packageID, Environment: environment, Target: target}, "Overview")
}

func (m *Model) openTargetWorkspace(request viewmodel.SetupRequest, selectedSection string) tea.Cmd {
	backend, ok := m.backend.(setupBackend)
	if !ok {
		m.output = "Unified setup service unavailable"
		return nil
	}
	key := state.Key{Source: request.SourceID, Package: request.PackageID, Environment: request.Environment, Target: request.Target}
	if request.Ref.CapabilityID != "" {
		key = state.Key{Source: request.Ref.PackID, Package: request.Ref.CapabilityID, Target: request.Ref.Name}
	}
	priorDraft := map[string]any(nil)
	if m.workspace != nil && m.workspace.Key == key {
		priorDraft = m.workspace.cachedDraft()
	}
	m.retryOperation = func() tea.Cmd { return m.openTargetWorkspace(request, selectedSection) }
	workspace := &workspaceState{
		Reference: request.Ref, Key: key, Section: selectedSection, InvokingView: m.view, Active: true,
		InvokingSelection: m.workspaceInvokingSelection(), Draft: priorDraft,
		Profile: m.profileForWorkspace(key), ProfileSnapshot: copyProfileSnapshot(m.profileSnapshot),
	}
	switch selectedSection {
	case "Overview":
		workspace.SectionID = sectionOverviewID
	case "Agents":
		workspace.SectionID = sectionAgentsID
	case "Runtime":
		workspace.SectionID = sectionRuntimeID
	case "Logs":
		workspace.SectionID = sectionLogsID
	case "Information":
		workspace.SectionID = sectionInformationID
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
	if _, ok := m.backend.(capabilityProfilesBackend); ok {
		for _, p := range m.capabilityProfiles[key.Package].Profiles {
			if p.Key == key {
				if len(p.MCPs) == 1 {
					return configurationRuntimeProfile(p, p.MCPs[0], nil)
				}
				return nil
			}
		}
	}

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
	itemField        string
	preview          viewmodel.SetupPreview
	values           map[string]any
	resetInputs      []string
	returnValues     map[string]any
	destinationField string
	origin           string
	section          string
	step             string
	failure          string
}

func (m *Model) achievedDestinationIDs(draft *setupRetryDraft, result *viewmodel.OperationResult) []string {
	if draft == nil || result == nil || draft.destinationField == "" {
		return nil
	}
	if draft.itemField != "" {
		return achievedComponentDestinations(draft, result)
	}
	requested, _ := draft.values[draft.destinationField].([]string)
	if len(requested) == 0 {
		return nil
	}
	requiresSkill := false
	for _, capability := range m.capabilities() {
		if capability.Package == draft.preview.Key.Package && capability.Source == draft.preview.Key.Source {
			requiresSkill = capability.Skill
			break
		}
	}
	definitions := draft.preview.MCPDefinitions
	if len(definitions) == 0 && draft.preview.MCP {
		definitions = []catalog.MCP{{Name: ""}}
	}
	if !requiresSkill && len(definitions) == 0 {
		return nil
	}
	completed := map[string]map[string]bool{}
	requestedIDs := make(map[string]bool, len(requested))
	for _, id := range requested {
		requestedIDs[id] = true
	}
	parent := draft.preview.Key
	parent.MCP = ""
	for _, change := range result.Changes {
		if change.Component != "skill" && change.Component != "mcp" {
			continue
		}
		changeParent := change.Key
		changeParent.MCP = ""
		if !requestedIDs[change.AgentID] || changeParent.Source != parent.Source || changeParent.Package != parent.Package || changeParent.Environment != parent.Environment || changeParent.Target != parent.Target {
			continue
		}
		if completed[change.AgentID] == nil {
			completed[change.AgentID] = map[string]bool{}
		}
		if change.Component == "skill" {
			completed[change.AgentID]["skill"] = true
		} else {
			completed[change.AgentID]["mcp:"+change.Key.MCP] = true
			if change.Key.MCP == "" && len(definitions) == 1 {
				completed[change.AgentID]["mcp:"+definitions[0].Name] = true
			}
		}
	}
	complete := []string{}
	for _, id := range requested {
		components := completed[id]
		if requiresSkill && !components["skill"] {
			continue
		}
		allMCPs := true
		for _, definition := range definitions {
			if !components["mcp:"+definition.Name] {
				allMCPs = false
				break
			}
		}
		if allMCPs {
			complete = append(complete, id)
		}
	}
	return complete
}

func (m *Model) openSetupForm(preview viewmodel.SetupPreview) {
	m.openSetupFormWithValues(preview, nil)
}

func (m *Model) openSetupFormWithValues(preview viewmodel.SetupPreview, overrides map[string]any) {
	defs := make([]catalog.Input, 0, len(preview.Inputs)+1)
	values := make(map[string]any, len(preview.Inputs)+1)
	visibilityContext := map[string]any{}
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
	for i, input := range preview.Inputs {
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
		}
	}
	for _, input := range preview.Inputs {
		if !input.Editable && input.HasValue {
			visibilityContext[input.Definition.Name] = input.Value
		}
		if !input.Editable {
			continue
		}
		def := input.Definition
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
	requiresNamedDestination := preview.MCP
	for _, destination := range workspaceDestinations(preview) {
		if requiresNamedDestination && strings.EqualFold(destination.ID, "all") {
			continue
		}
		name := destination.Name
		if name == "" {
			name = destination.ID
		}
		name = agents.DisplayName(name)
		path := destinationDisplayPath(preview, destination)
		label := name
		if path != "" {
			label += " — " + path
		}
		choices = append(choices, catalog.Choice{Value: destination.ID, Label: label, DisabledReason: destination.DisabledReason})
		if destination.Selected {
			selected = append(selected, destination.ID)
		}
	}
	if len(choices) > 0 {
		defs = append(defs, catalog.Input{Name: destinationField, Label: "Destinations", Type: "multichoice", Options: choices})
		values[destinationField] = selected
	} else {
		destinationField = ""
	}
	m.pendingSetupItemsField = ""
	if len(preview.Items) > 0 {
		field := "__aact_components"
		for {
			collision := false
			for _, def := range defs {
				if def.Name == field {
					collision = true
				}
			}
			if !collision {
				break
			}
			field = "_" + field
		}
		m.pendingSetupItemsField = field
		preview.ItemFieldName = field
		defs = append(defs, catalog.Input{Name: field, Label: "Capability components", Type: "multichoice", Options: componentOptions(preview, selected)})
		values[field] = append([]string{}, preview.SelectedItemIDs...)
	}
	replayValues := make(map[string]any, len(overrides))
	for name, value := range overrides {
		replayValues[name] = value
	}
	if m.workspace != nil && m.workspace.Key == preview.Key {
		for name, value := range m.workspace.cachedDraft() {
			replayValues[name] = value
		}
	}
	m.pendingSetup = &preview
	m.pendingSetupField = destinationField
	m.form = forms.NewFormWithContext(m.ctx, defs, values, visibilityContext)
	if m.workspace != nil && m.workspace.Key == preview.Key {
		m.form.SetBackNavigation(true)
	}
	name := preview.PackageName
	if name == "" {
		name = preview.Key.Package
	}
	title := "Install · " + name
	m.form.SetTitle(title)
	sections := workspaceFormSections(preview, defs, destinationField)
	m.form.SetSections(sections...)
	if m.workspace == nil || m.workspace.Key != preview.Key {
		selectedID := sectionAgentsID
		for _, section := range sections {
			if len(section.Fields) > 0 {
				selectedID = section.ID
				break
			}
		}
		m.form.SelectSectionID(selectedID)
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
	for _, input := range preview.Inputs {
		if !input.Editable {
			continue
		}
		if fixed := fixedGroups[input.Definition.ExclusiveGroup]; fixed != "" {
			m.form.SetDisabled(input.Definition.Name, fixed+" is fixed by target")
			continue
		}
		if input.Provenance == "saved" || input.Provenance == "Saved override" {
			name := input.Definition.Name
			m.form.SetResetValue(name, input.InheritedValue, input.HasInheritedValue)
			inheritedOrigin := input.InheritedOrigin
			if inheritedOrigin == "" {
				inheritedOrigin = "unset"
			}
			baseLabel := input.Definition.Label
			if baseLabel == "" {
				baseLabel = name
			}
			m.form.SetResetPresentation(name, baseLabel+" ["+inheritedOrigin+"]", setupProvenanceHint(inheritedOrigin, input.InheritedPath))
			m.form.SetOverridePresentation(name, baseLabel+" [unsaved override]", "Unsaved override · Will save as an override · Ctrl+R restore inherited value")
			m.form.SetHint(name, setupProvenanceHint("saved", input.ProvenancePath)+" · Ctrl+R restore inherited value")
		} else if setupDisplayValue(input) && input.ProvenancePath != "" {
			m.form.SetHint(input.Definition.Name, setupProvenanceHint(input.Provenance, input.ProvenancePath))
		}
	}
	if m.workspace != nil && m.workspace.Key == preview.Key {
		m.configureWorkspaceForm(preview)
	}
	// Preview values and synthetic destination controls define the initial
	// resolved state. Replayed overrides/drafts are the only changes that should
	// appear in the unsaved summary.
	m.configureComponentActions()
	m.form.MarkClean()
	if len(replayValues) > 0 {
		if err := m.form.ApplyValues(replayValues); err != nil {
			m.output = "Could not restore configuration draft: " + err.Error()
		}
	}
	m.form.SetSubmitLabel("Save and apply")
	m.form.SetCancelHidden(true)
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
	case "unset":
		return "No lower-precedence value"
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
	var resetInputs []string
	if m.form != nil {
		resetInputs = m.form.ResetFields()
	}
	return m.applySetupWithReset(values, resetInputs)
}

func (m *Model) applySetupWithReset(values map[string]any, resetInputs []string) tea.Cmd {
	backend, ok := m.backend.(setupBackend)
	if !ok || m.pendingSetup == nil {
		m.output = "Unified setup service unavailable"
		return nil
	}
	if workspace := m.workspace; workspace != nil && workspace.Active && workspace.Preview != nil && workspace.Preview.MCP && m.profileError != nil {
		m.output = "Runtime observation failed; refresh this target before applying an MCP configuration."
		return nil
	}
	if workspace := m.workspace; workspace != nil && workspace.Active && workspace.Profile != nil && workspace.Profile.Ownership != "local" && strings.TrimSpace(workspace.Profile.URL) == "" {
		m.output = nonempty(workspace.Profile.StartDisabledReason, "Cannot safely apply configuration: runtime ownership is unknown and no observed endpoint is available.")
		return nil
	}
	preview := *m.pendingSetup
	preview.Inputs = append([]viewmodel.SetupInput(nil), m.pendingSetup.Inputs...)
	reset := make(map[string]bool, len(resetInputs))
	for _, name := range resetInputs {
		reset[name] = true
		for i := range preview.Inputs {
			input := &preview.Inputs[i]
			if input.Definition.Name != name {
				continue
			}
			input.Value = input.InheritedValue
			input.HasValue = input.HasInheritedValue
			input.Provenance = input.InheritedOrigin
			if input.Provenance == "" {
				input.Provenance = "unset"
			}
			input.ProvenancePath = input.InheritedPath
		}
	}

	if workspace := m.workspace; workspace != nil && workspace.Active && workspace.Key == preview.Key {
		for _, definition := range preview.MCPDefinitions {
			profile := m.profileForMCPName(definition.Name)
			if profile == nil || profile.Ownership == "local" || strings.TrimSpace(profile.URL) != "" {
				continue
			}
			owner := nonempty(profile.Ownership, "unknown")
			m.output = fmt.Sprintf("Cannot safely attach MCP %q: %s ownership has no observed endpoint URI.", definition.Name, owner)
			return nil
		}
	}
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
	m.setupRetry = &setupRetryDraft{preview: preview, values: cloneSetupValues(values), resetInputs: append([]string(nil), resetInputs...), destinationField: destinationField, itemField: m.pendingSetupItemsField, origin: origin, section: section}
	m.setupOperationPending = true
	inputs := make(map[string]any, len(preview.Inputs))
	for _, input := range preview.Inputs {
		if !input.Editable {
			continue
		}
		if value, exists := values[input.Definition.Name]; exists && !reset[input.Definition.Name] {
			inputs[input.Definition.Name] = value
		}
	}
	request := viewmodel.SetupInstallRequest{
		SetupRequest: viewmodel.SetupRequest{SourceID: preview.Key.Source, PackageID: preview.Key.Package, Environment: preview.Key.Environment, Target: preview.Key.Target},
		Inputs:       inputs, ResetInputs: append([]string(nil), resetInputs...), DestinationIDs: destinations,
	}
	if m.profileMode() {
		request.SetupRequest = m.packProfileRequest(preview.Key)
	}
	if m.pendingSetupItemsField != "" {
		items, _ := values[m.pendingSetupItemsField].([]string)
		request.ItemIDs = make([]string, len(items))
		copy(request.ItemIDs, items)
	}
	if workspace := m.workspace; workspace != nil && workspace.Active && workspace.Key == preview.Key {
		var profiles []viewmodel.Profile
		if workspace.ProfileSnapshot != nil {
			profiles = workspace.ProfileSnapshot.Profiles
		}
		if len(profiles) == 0 && workspace.Profile != nil {
			profiles = []viewmodel.Profile{*workspace.Profile}
		}
		packageDef := catalog.Package{}
		for _, candidate := range m.catalog {
			if candidate.ID == preview.Key.Package && m.sourceLabels[candidate.Dir] == preview.Key.Source {
				packageDef = candidate
				break
			}
		}
		matchedExternal := map[string]string{}
		for _, definition := range preview.MCPDefinitions {
			for _, profile := range profiles {
				matchingMCP := profile.Key.MCP == definition.Name || (packageDef.MCP != nil && len(preview.MCPDefinitions) == 1 && profile.Key.MCP == "")
				if profile.Key.Source != preview.Key.Source || profile.Key.Package != preview.Key.Package || profile.Key.Environment != preview.Key.Environment || profile.Key.Target != preview.Key.Target || !matchingMCP || profile.Ownership == "local" || strings.TrimSpace(profile.URL) == "" {
					continue
				}
				matchedExternal[definition.Name] = strings.TrimSpace(profile.URL)
			}
		}
		if len(matchedExternal) > 0 {
			if packageDef.MCP != nil && len(preview.MCPDefinitions) == 1 {
				request.ExternalURL = matchedExternal[preview.MCPDefinitions[0].Name]
			} else {
				request.ExternalURLs = matchedExternal
			}
		}
	}
	m.busy = true
	m.action = "install"
	m.progressStep = ""
	m.progressOutput = ""
	m.progressFrame = 0
	m.progressStarted = time.Now()
	m.progressLastOutput = time.Time{}
	m.progressOffset = 0
	m.home.Modal = nil
	m.setupOperationID++
	setupID := m.setupOperationID
	ctx := m.ctx
	request.Inputs = cloneSetupValues(request.Inputs)
	request.DestinationIDs = append([]string(nil), request.DestinationIDs...)
	if len(request.ExternalURLs) > 0 {
		urls := make(map[string]string, len(request.ExternalURLs))
		for name, endpoint := range request.ExternalURLs {
			urls[name] = endpoint
		}
		request.ExternalURLs = urls
	}
	progressEvents := make(chan viewmodel.OperationProgress, 32)
	progressDone := make(chan struct{})
	m.setupProgressEvents = progressEvents
	m.setupProgressDone = progressDone
	emitter := setupProgressEmitter{ctx: ctx, events: progressEvents, done: progressDone}
	ctx = viewmodel.WithOperationProgress(ctx, emitter.emit)
	operationCmd := func() tea.Msg {
		defer close(progressDone)
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
	return tea.Batch(operationCmd, waitSetupProgress(setupID, progressEvents, progressDone, ctx), setupProgressTick(setupID))
}

func waitSetupProgress(setupID uint64, events <-chan viewmodel.OperationProgress, done <-chan struct{}, ctx context.Context) tea.Cmd {
	return func() tea.Msg {
		select {
		case progress := <-events:
			return setupProgressMsg{setupID: setupID, progress: progress}
		case <-done:
			return setupProgressClosedMsg{setupID: setupID}
		case <-ctx.Done():
			return setupProgressClosedMsg{setupID: setupID}
		}
	}
}

func setupProgressTick(setupID uint64) tea.Cmd {
	return tea.Tick(150*time.Millisecond, func(time.Time) tea.Msg {
		return setupProgressTickMsg{setupID: setupID}
	})
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
	// Profile references reuse the legacy Target storage slot for identity.
	// Keep that compatibility detail out of user-facing operation progress.
	if key.Environment == "" && key.Source != "" && key.Target != "" {
		parts = append(parts, "Profile "+key.Target)
		return strings.Join(parts, " · ")
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
