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
	for _, input := range preview.Inputs {
		def := input.Definition
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
		label := destination.ID + " — " + destination.Path
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
	for _, input := range preview.Inputs {
		if input.HasValue && input.ProvenancePath != "" {
			m.form.SetHint(input.Definition.Name, input.Provenance+" · "+filepath.Base(input.ProvenancePath)+" · "+input.ProvenancePath)
		}
	}
	m.form.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
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
