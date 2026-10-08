package tui

import (
	"fmt"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/forms"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
	"path/filepath"
	"slices"
	"strings"
)

const sectionComponentsID = "tool:components"

func componentChoiceLabel(item catalog.InstallationItem) string {
	parts := []string{}
	if len(item.Skills) > 0 {
		parts = append(parts, fmt.Sprintf("%d skill(s)", len(item.Skills)))
	}
	if len(item.MCPs) > 0 {
		parts = append(parts, fmt.Sprintf("%d MCP(s)", len(item.MCPs)))
	}
	if len(item.Plugins) > 0 {
		parts = append(parts, "native plugin")
	}
	return item.Label + " · " + strings.Join(parts, " + ")
}
func componentOptions(preview viewmodel.SetupPreview, destinations []string) []catalog.Choice {
	choices := []catalog.Choice{}
	for _, item := range preview.Items {
		choice := catalog.Choice{Value: item.ID, Label: componentChoiceLabel(item)}
		for _, name := range item.Plugins {
			format := ""
			for _, p := range preview.PluginDefinitions {
				if p.Name == name {
					format = p.Format
				}
			}
			compatible := len(destinations) > 0 && format != ""
			for _, id := range destinations {
				found := false
				for _, destination := range preview.Destinations {
					if destination.ID == id {
						found = slices.Contains(destination.Features.PluginFormats, format)
					}
				}
				compatible = compatible && found
			}
			if !compatible {
				choice.DisabledReason = "Every selected agent must support this native plugin"
			}
		}
		choices = append(choices, choice)
	}
	return choices
}
func (m *Model) refreshComponentAvailability() {
	if m.pendingSetup == nil || m.form == nil || m.pendingSetupItemsField == "" {
		return
	}
	destinations, _ := m.form.Values()[m.pendingSetupField].([]string)
	m.form.SetChoices(m.pendingSetupItemsField, componentOptions(*m.pendingSetup, destinations))
}
func (m *Model) selectAllSkills() {
	if m.pendingSetup == nil || m.form == nil || m.pendingSetupItemsField == "" {
		return
	}
	selected, _ := m.form.Values()[m.pendingSetupItemsField].([]string)
	ids := append([]string{}, selected...)
	for _, item := range m.pendingSetup.Items {
		if len(item.Skills) > 0 && !slices.Contains(ids, item.ID) {
			ids = append(ids, item.ID)
		}
	}
	if err := m.form.ApplyValues(map[string]any{m.pendingSetupItemsField: ids}); err != nil {
		m.output = err.Error()
	}
}
func (m *Model) configureComponentActions() {
	if m.form != nil && m.pendingSetupItemsField != "" {
		m.form.SetSectionActionsID(sectionComponentsID, forms.FormAction{ID: "select-all-skills", Label: "Select all skills"})
	}
}

func achievedComponentDestinations(draft *setupRetryDraft, result *viewmodel.OperationResult) []string {
	ids, _ := draft.values[draft.itemField].([]string)
	requested, _ := draft.values[draft.destinationField].([]string)
	wanted := map[string]bool{}
	for _, item := range draft.preview.Items {
		if !slices.Contains(ids, item.ID) {
			continue
		}
		for _, name := range item.Skills {
			wanted["skill:"+name] = true
		}
		for _, name := range item.MCPs {
			enabled := true
			for _, d := range draft.preview.MCPDefinitions {
				if d.Name == name && d.EnabledInput != "" {
					value := draft.values[d.EnabledInput]
					for _, input := range draft.preview.Inputs {
						if input.Definition.Name == d.EnabledInput && !input.Editable {
							value = input.Value
						}
					}
					enabled = value == true
				}
			}
			if enabled {
				wanted["mcp:"+name] = true
			}
		}
		for _, name := range item.Plugins {
			wanted["plugin:"+name] = true
		}
	}
	complete := []string{}
	for _, id := range requested {
		achieved := map[string]bool{}
		for _, row := range result.Changes {
			if row.AgentID != id || row.Key.Source != draft.preview.Key.Source || row.Key.Package != draft.preview.Key.Package || row.Key.Environment != draft.preview.Key.Environment || row.Key.Target != draft.preview.Key.Target {
				continue
			}
			switch row.Component {
			case "skill":
				achieved["skill:"+filepath.Base(row.Destination)] = true
			case "mcp":
				name := row.Key.MCP
				if name == "" {
					name = row.Key.Profile
				}
				if name == "" && len(draft.preview.MCPDefinitions) == 1 {
					name = draft.preview.MCPDefinitions[0].Name
				}
				achieved["mcp:"+name] = true
			case "plugin":
				name, _, _ := strings.Cut(row.ReleaseID, "@")
				achieved["plugin:"+name] = true
			}
		}
		all := len(wanted) > 0
		for component := range wanted {
			all = all && achieved[component]
		}
		if all {
			complete = append(complete, id)
		}
	}
	return complete
}
