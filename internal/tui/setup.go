package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/forms"
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
	backend, ok := m.backend.(setupBackend)
	if !ok {
		m.output = "Unified setup service unavailable"
		return nil
	}
	m.busy = true
	m.action = "load setup"
	request := viewmodel.SetupRequest{SourceID: sourceID, PackageID: packageID, Environment: environment, Target: target}
	return func() tea.Msg {
		preview, err := backend.UISetupPreview(m.ctx, request)
		return setupPreviewMsg{preview: preview, err: err}
	}
}

type setupRetryDraft struct {
	preview viewmodel.SetupPreview
	values  map[string]any
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
		if pkg.ID == preview.Key.Package && pkg.HasMCP() {
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
		case "codex":
			name = "Codex"
		case "opencode":
			name = "OpenCode"
		case "claude", "claude-code":
			name = "Claude Code"
		}
		label := name + " — " + destination.Path
		if destination.ID == "all" {
			label = "All — " + destination.Path
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
	m.pendingSetup = &preview
	m.pendingSetupField = destinationField
	m.form = forms.NewForm(m.ctx, defs, values)
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
	sectionFields := map[string][]string{}
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
	for _, def := range defs {
		name := strings.ToLower(def.Name + " " + def.Label)
		section := "Inputs"
		switch {
		case def.Name == destinationField:
			section = "Destinations"
		case strings.Contains(name, "auth") || strings.Contains(name, "token") || strings.Contains(name, "kubeconfig") || def.ExclusiveGroup != "":
			section = "Authentication"
		case strings.Contains(name, "database") || strings.Contains(name, "dbms") || strings.Contains(def.OptionsFrom, "dbms"):
			section = "Databases"
		}
		if isClusterInspector && section == "Inputs" {
			section = "Connection"
		}
		sectionFields[section] = append(sectionFields[section], def.Name)
	}
	orderedSections := []string{"Authentication", "Inputs", "Databases", "Destinations"}
	if isClusterInspector {
		orderedSections = []string{"Connection", "Authentication", "Databases", "Destinations"}
	}
	for _, section := range orderedSections {
		if len(sectionFields[section]) > 0 || (isClusterInspector && section == "Connection") {
			sections = append(sections, forms.FormSection{Title: section, Fields: sectionFields[section]})
		}
	}
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
	m.pendingSetup = nil
	destinations, _ := values[m.pendingSetupField].([]string)
	m.pendingSetupField = ""
	m.setupRetry = &setupRetryDraft{preview: preview, values: cloneSetupValues(values)}
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
	m.busy = true
	m.action = "install"
	m.home.Modal = nil
	origin := m.view
	return func() tea.Msg {
		result, err := backend.UIInstall(m.ctx, request)
		lines := []string{}
		if result.Saved {
			lines = append(lines, "Inputs saved")
		}
		if result.Message != "" {
			lines = append(lines, result.Message)
		}
		for _, change := range result.Changes {
			lines = append(lines, fmt.Sprintf("%s: %s configured", change.AgentID, change.Component))
		}
		lines = append(lines, result.Errors...)
		return operationMsg{origin: origin, output: strings.Join(lines, "\n"), err: err, failed: len(result.Errors) > 0}
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
