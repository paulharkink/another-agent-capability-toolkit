package app

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
)

func hasAllMCPRegistrations(p catalog.Package, rows []state.Installation, key state.Key, agentID string) bool {
	for _, definition := range p.MCPDefinitions() {
		mcpID := ""
		if len(p.MCPs) > 0 {
			mcpID = definition.Name
		}
		found := false
		for _, row := range rows {
			if sameCapabilityKey(row.Key, key) && row.Key.MCP == mcpID && row.AgentID == agentID && row.Component == "mcp" {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return len(p.MCPDefinitions()) > 0
}

// activeRegistrationName returns a currently registered name for a sole MCP
// only when all matching registrations agree. A sibling MCP is never used as
// an input fallback for the selected profile.
func activeRegistrationName(p catalog.Package, rows []state.Installation, key state.Key) string {
	definitions := p.MCPDefinitions()
	if len(definitions) != 1 || definitions[0].RegistrationNameInput == "" {
		return ""
	}
	name := ""
	for _, row := range rows {
		if !sameCapabilityKey(row.Key, key) || row.Component != "mcp" || row.RegistrationName == "" ||
			(row.Key.MCP != "" && row.Key.MCP != definitions[0].Name) {
			continue
		}
		if name != "" && name != row.RegistrationName {
			return ""
		}
		name = row.RegistrationName
	}
	return name
}

func profileRegistrationConfig(rows []state.Installation, key state.Key, agentID string) (state.Installation, bool) {
	var selected state.Installation
	found := false
	for _, row := range rows {
		if !sameCapabilityKey(row.Key, key) || row.AgentID != agentID || row.Component != "mcp" || row.Destination == "" {
			continue
		}
		if found && (filepath.Clean(selected.Destination) != filepath.Clean(row.Destination) ||
			(selected.AgentHome != "" && row.AgentHome != "" && filepath.Clean(selected.AgentHome) != filepath.Clean(row.AgentHome))) {
			return state.Installation{}, false
		}
		if !found || selected.AgentHome == "" {
			selected = row
		}
		found = true
	}
	return selected, found
}

func targetInputChoices(raw map[string]any, path string) ([]catalog.Choice, error) {
	parts := strings.Split(path, ".")
	choices := []catalog.Choice{}
	var walk func(any, int, []string) error
	walk = func(node any, index int, keys []string) error {
		if index == len(parts) {
			if len(keys) == 0 {
				return fmt.Errorf("options_from %q must contain a wildcard", path)
			}
			value := strings.Join(keys, "/")
			label := value
			if table, ok := node.(map[string]any); ok {
				if named, exists := table["label"]; exists {
					name, ok := named.(string)
					if !ok || strings.TrimSpace(name) == "" {
						return fmt.Errorf("options_from %q: label for %s must be a non-empty string", path, value)
					}
					label = name + " — " + value
				}
			}
			choices = append(choices, catalog.Choice{Value: value, Label: label})
			return nil
		}
		table, ok := node.(map[string]any)
		if !ok {
			return fmt.Errorf("options_from %q expects a table at %s", path, strings.Join(parts[:index], "."))
		}
		if part := parts[index]; part != "*" {
			child, exists := table[part]
			if !exists {
				return nil
			}
			return walk(child, index+1, keys)
		}
		names := make([]string, 0, len(table))
		for name := range table {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if err := walk(table[name], index+1, append(append([]string(nil), keys...), name)); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(raw, 0, nil); err != nil {
		return nil, err
	}
	return choices, nil
}
