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

func (m *Model) openSetupForm(preview viewmodel.SetupPreview) {
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
		}
	}
	selectors := map[string]string{}
	for group, indices := range groups {
		if fixedGroups[group] != "" {
			continue
		}
		if len(indices) != 2 {
			continue
		}
		a, b := preview.Inputs[indices[0]].Definition.Type, preview.Inputs[indices[1]].Definition.Type
		if !((a == "secret" && b == "file") || (a == "file" && b == "secret")) {
			continue
		}
		name := "__aact_auth_method_" + group
		for usedNames[name] {
			name = "_" + name
		}
		selectors[group] = name
		usedNames[name] = true
	}
	for i, input := range preview.Inputs {
		if !input.Editable {
			continue
		}
		def := input.Definition
		if def.OptionsFrom != "" && input.HasValue {
			def = withUnavailableSavedChoices(def, input.Value)
		}
		if selector := selectors[def.ExclusiveGroup]; selector != "" && groups[def.ExclusiveGroup][0] == i {
			options := []catalog.Choice{}
			selected := preview.Inputs[groups[def.ExclusiveGroup][0]].Definition.Name
			for _, index := range groups[def.ExclusiveGroup] {
				candidate := preview.Inputs[index]
				choiceLabel := candidate.Definition.Label
				if choiceLabel == "" {
					choiceLabel = candidate.Definition.Name
				}
				options = append(options, catalog.Choice{Value: candidate.Definition.Name, Label: choiceLabel})
				if candidate.HasValue {
					if text, ok := candidate.Value.(string); ok && strings.TrimSpace(text) != "" {
						selected = candidate.Definition.Name
					}
				}
			}
			defs = append(defs, catalog.Input{Name: selector, Label: "Authentication", Type: "choice", Options: options, Required: true})
			values[selector] = selected
		}
		label := def.Label
		if label == "" {
			label = def.Name
		}
		if input.HasValue {
			origin := input.Provenance
			def.Label = label + " [" + origin + "]"
			values[def.Name] = input.Value
		}
		defs = append(defs, def)
	}
	choices := make([]catalog.Choice, 0, len(preview.Destinations))
	selected := []string{}
	for _, destination := range preview.Destinations {
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
		if input.HasValue && input.ProvenancePath != "" {
			m.form.SetHint(input.Definition.Name, input.Provenance+" · "+filepath.Base(input.ProvenancePath)+" · "+input.ProvenancePath)
		}
		if selector := selectors[input.Definition.ExclusiveGroup]; selector != "" {
			m.form.SetConditional(input.Definition.Name, selector, input.Definition.Name)
			if input.Definition.Type == "file" {
				m.form.SetHint(input.Definition.Name, "Import source; AACT uses managed credential material at runtime")
			}
		}
	}
	m.form.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
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
		return operationMsg{origin: origin, output: strings.Join(lines, "\n"), err: err}
	}
}
