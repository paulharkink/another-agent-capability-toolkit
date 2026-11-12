package tui

import (
	"strings"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

// minimalGroupProfileAuthInputs returns missing editable fields declared in an
// Authentication section. Required fields, auth-required fields, and fields
// explicitly marked for auth prompts are eligible; visibility and saved
// secrets are respected.
func minimalGroupProfileAuthInputs(sections []catalog.Section, inputs []viewmodel.SetupInput, values map[string]any) []viewmodel.SetupInput {
	authFields := make(map[string]bool)
	for _, section := range sections {
		if !strings.EqualFold(strings.TrimSpace(section.ID), "authentication") && !strings.EqualFold(strings.TrimSpace(section.Title), "authentication") {
			continue
		}
		for _, name := range section.Fields {
			authFields[name] = true
		}
	}
	if len(authFields) == 0 {
		return nil
	}

	context := make(map[string]any, len(values)+len(inputs))
	for name, value := range values {
		context[name] = value
	}
	for _, input := range inputs {
		if _, exists := context[input.Definition.Name]; !exists && input.HasValue {
			context[input.Definition.Name] = input.Value
		}
	}

	selected := make([]viewmodel.SetupInput, 0)
	for _, input := range inputs {
		definition := input.Definition
		if !authFields[definition.Name] || !input.Editable {
			continue
		}
		missing := !input.HasValue
		if input.SecretSaved && definition.AuthRequired {
			missing = true
		}
		if !definition.AuthPrompt && !missing {
			continue
		}
		if !definition.Required && !definition.AuthRequired && !definition.AuthPrompt {
			continue
		}
		if !catalog.InputVisible(definition, context) {
			continue
		}
		selected = append(selected, input)
	}
	return selected
}
