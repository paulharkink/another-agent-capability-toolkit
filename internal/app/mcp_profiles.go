package app

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
)

func selectMCPProfile(p catalog.Package, name string) (catalog.MCP, error) {
	profiles := p.MCPProfiles()
	if len(profiles) == 0 {
		return catalog.MCP{}, fmt.Errorf("capability %q has no MCP profiles", p.ID)
	}
	if name == "" {
		if len(profiles) != 1 {
			return catalog.MCP{}, fmt.Errorf("capability %q has multiple MCP profiles; choose one with --profile", p.ID)
		}
		return profiles[0], nil
	}
	for _, profile := range profiles {
		if profile.Name == name {
			return profile, nil
		}
	}
	return catalog.MCP{}, fmt.Errorf("capability %q has no MCP profile %q", p.ID, name)
}

func mcpProfileKey(base state.Key, p catalog.Package, profile catalog.MCP) state.Key {
	if p.MCP != nil && len(p.MCPs) == 0 {
		return base
	}
	profileSemantics := profile.EnabledInput != "" || profile.TokenInput != "" || profile.TokenFileInput != "" || profile.TokenEnvInput != "" || profile.TokenHeader != "" || profile.TokenContainerEnv != "" || len(profile.Args) > 0 || len(profile.EnvInputs) > 0 || len(profile.SecretEnvInputs) > 0
	if profileSemantics {
		base.Profile = profile.Name
	} else {
		// Existing [[mcps]] declarations use the original MCP child key.
		base.MCP = profile.Name
	}
	return base
}

func resolveProfileToken(profile catalog.MCP, values map[string]any) (string, error) {
	type candidate struct {
		field string
		value string
	}
	readString := func(name string) string {
		if name == "" {
			return ""
		}
		value, _ := values[name].(string)
		return strings.TrimSpace(value)
	}
	candidates := []candidate{
		{field: "raw value", value: readString(profile.TokenInput)},
		{field: "file", value: readString(profile.TokenFileInput)},
		{field: "environment variable", value: readString(profile.TokenEnvInput)},
	}
	selected := []candidate{}
	for _, option := range candidates {
		if option.value != "" {
			selected = append(selected, option)
		}
	}
	if len(selected) > 1 {
		return "", errors.New("choose exactly one provider token source: raw value, file, or environment variable")
	}
	if len(selected) == 0 {
		if profile.TokenInput != "" || profile.TokenFileInput != "" || profile.TokenEnvInput != "" {
			return "", errors.New("choose exactly one provider token source: raw value, file, or environment variable")
		}
		return "", nil
	}
	switch selected[0].field {
	case "raw value":
		return selected[0].value, nil
	case "file":
		contents, err := os.ReadFile(selected[0].value)
		if err != nil {
			return "", fmt.Errorf("read provider token file %q: %w", selected[0].value, err)
		}
		if token := strings.TrimSpace(string(contents)); token != "" {
			return token, nil
		}
		return "", errors.New("provider token file is empty")
	case "environment variable":
		token, ok := os.LookupEnv(selected[0].value)
		if !ok || strings.TrimSpace(token) == "" {
			return "", fmt.Errorf("provider token environment variable %q is unset or empty", selected[0].value)
		}
		return strings.TrimSpace(token), nil
	default:
		return "", errors.New("unknown provider token source")
	}
}

func profileRegistrationHeaders(profile catalog.MCP, values map[string]any) (map[string]string, error) {
	if profile.TokenHeader == "" {
		return nil, nil
	}
	token, err := resolveProfileToken(profile, values)
	if err != nil {
		return nil, err
	}
	return map[string]string{profile.TokenHeader: profile.TokenPrefix + token}, nil
}

func profileRegistrationName(profile catalog.MCP, key state.Key, values map[string]any) string {
	// Kept as a separate helper so package authors can choose a stable readable
	// registration name without exposing internal state hashes in agent configs.
	if profile.RegistrationNameInput != "" {
		if value, ok := values[profile.RegistrationNameInput].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return registrationName(key)
}

func hasMCPAction(p catalog.Package, action string) bool {
	for _, profile := range p.MCPProfiles() {
		if _, ok := profile.Actions[action]; ok {
			return true
		}
	}
	return false
}
