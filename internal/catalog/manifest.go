package catalog

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

var identifier = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)
var environmentName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func Load(dir string) (Package, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Package{}, err
	}
	path := filepath.Join(abs, "package.toml")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		info, e := os.Stat(filepath.Join(abs, "SKILL.md"))
		if e != nil || info.IsDir() {
			return Package{}, fmt.Errorf("%s: neither package.toml nor SKILL.md found", abs)
		}
		name := filepath.Base(abs)
		p := Package{SchemaVersion: 1, ID: name, Name: name, Dir: abs, Skill: &Skill{Name: name}}
		return p, Validate(p)
	}
	if err != nil {
		return Package{}, err
	}
	var p Package
	if err = toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields().Decode(&p); err != nil {
		if strict, ok := err.(*toml.StrictMissingError); ok {
			return Package{}, fmt.Errorf("%s: %s", path, strict.String())
		}
		return Package{}, fmt.Errorf("%s: %w", path, err)
	}
	p.Dir = abs
	if err = validateDeclaredTimeouts(data); err != nil {
		return Package{}, fmt.Errorf("%s: %w", path, err)
	}
	if err = Validate(p); err != nil {
		return Package{}, fmt.Errorf("%s: %w", path, err)
	}
	if p.Generator != nil {
		defaultCommand(p.Generator)
	}
	if p.MCP != nil {
		for name, c := range p.MCP.Actions {
			defaultCommand(&c)
			p.MCP.Actions[name] = c
		}
	}
	for i := range p.MCPs {
		for name, c := range p.MCPs[i].Actions {
			defaultCommand(&c)
			p.MCPs[i].Actions[name] = c
		}
	}
	return p, nil
}
func defaultCommand(c *Command) {
	if c.TimeoutSeconds == 0 {
		c.TimeoutSeconds = 300
	}
	if c.Windows != nil {
		defaultCommand(c.Windows)
	}
}
func Validate(p Package) error {
	if p.SchemaVersion != 1 {
		return fmt.Errorf("unsupported schema_version %d", p.SchemaVersion)
	}
	if !identifier.MatchString(p.ID) {
		return fmt.Errorf("invalid package id %q", p.ID)
	}
	if p.Skill == nil && !p.HasMCP() {
		return fmt.Errorf("package must declare skill or mcp")
	}
	if p.MCP != nil && len(p.MCPs) > 0 {
		return fmt.Errorf("cannot mix [mcp] and [[mcps]] declarations")
	}
	if p.Skill != nil && !identifier.MatchString(p.Skill.Name) {
		return fmt.Errorf("invalid skill name %q", p.Skill.Name)
	}
	if p.MCP != nil && !identifier.MatchString(p.MCP.Name) {
		return fmt.Errorf("invalid mcp name %q", p.MCP.Name)
	}
	if p.MCP != nil && p.MCP.RegistrationTimeoutMS < 0 {
		return fmt.Errorf("mcp registration_timeout_ms must be positive")
	}
	mcpNames := map[string]bool{}
	for _, mcp := range p.MCPDefinitions() {
		if !identifier.MatchString(mcp.Name) {
			return fmt.Errorf("invalid mcp name %q", mcp.Name)
		}
		if mcpNames[mcp.Name] {
			return fmt.Errorf("duplicate mcp name %q", mcp.Name)
		}
		mcpNames[mcp.Name] = true
		if mcp.RegistrationTimeoutMS < 0 {
			return fmt.Errorf("mcp registration_timeout_ms must be positive")
		}
	}
	seen := map[string]bool{}
	inputsByName := map[string]Input{}
	for _, in := range p.Inputs {
		if !identifier.MatchString(in.Name) {
			return fmt.Errorf("invalid input name %q", in.Name)
		}
		if seen[in.Name] {
			return fmt.Errorf("duplicate input %q", in.Name)
		}
		seen[in.Name] = true
		inputsByName[in.Name] = in
		if in.ExclusiveGroup != "" && !identifier.MatchString(in.ExclusiveGroup) {
			return fmt.Errorf("input %s: invalid exclusive_group %q", in.Name, in.ExclusiveGroup)
		}
		switch in.Type {
		case "string", "secret", "integer", "number", "float", "boolean", "choice", "multichoice", "multiple-choice", "file", "directory":
		default:
			return fmt.Errorf("input %s: unsupported type %q", in.Name, in.Type)
		}
		if in.OptionsFrom != "" {
			if in.Type != "multichoice" && in.Type != "multiple-choice" {
				return fmt.Errorf("input %s: options_from requires multichoice type", in.Name)
			}
			hasWildcard := false
			validPath := true
			for _, part := range strings.Split(in.OptionsFrom, ".") {
				if part == "*" {
					hasWildcard = true
				} else if part == "" || strings.Contains(part, "*") {
					validPath = false
				}
			}
			if !hasWildcard || !validPath {
				return fmt.Errorf("input %s: options_from must be a dotted target path with a wildcard", in.Name)
			}
		}
		if in.Min != nil && in.Max != nil && *in.Min > *in.Max {
			return fmt.Errorf("input %s: min exceeds max", in.Name)
		}
		if in.MinItems != nil && *in.MinItems < 0 || in.MaxItems != nil && *in.MaxItems < 0 || in.MinItems != nil && in.MaxItems != nil && *in.MinItems > *in.MaxItems {
			return fmt.Errorf("input %s: invalid item bounds", in.Name)
		}
	}
	for _, mcp := range p.MCPDefinitions() {
		if mcp.EnabledInput != "" {
			input, ok := inputsByName[mcp.EnabledInput]
			if !ok {
				return fmt.Errorf("mcp %s enabled_input %q does not name a package input", mcp.Name, mcp.EnabledInput)
			}
			if input.Type != "boolean" {
				return fmt.Errorf("mcp %s enabled_input %q must be boolean", mcp.Name, mcp.EnabledInput)
			}
		}
		for _, ref := range []struct{ field, name, wantType string }{
			{"token_input", mcp.TokenInput, "secret"},
			{"token_file_input", mcp.TokenFileInput, "file"},
			{"token_env_input", mcp.TokenEnvInput, "string"},
		} {
			if ref.name == "" {
				continue
			}
			input, ok := inputsByName[ref.name]
			if !ok || input.Type != ref.wantType {
				return fmt.Errorf("mcp %s %s %q must reference a declared %s input", mcp.Name, ref.field, ref.name, ref.wantType)
			}
		}
		if mcp.TokenHeader != "" && mcp.TokenContainerEnv != "" {
			return fmt.Errorf("mcp %s cannot set both token_header and token_container_env", mcp.Name)
		}
		if mcp.TokenHeader != "" && mcp.TokenInput == "" && mcp.TokenFileInput == "" && mcp.TokenEnvInput == "" {
			return fmt.Errorf("mcp %s token_header requires a token source input", mcp.Name)
		}
		if mcp.TokenHeader != "" && (strings.TrimSpace(mcp.TokenHeader) != mcp.TokenHeader || strings.ContainsAny(mcp.TokenHeader, ":\r\n\x00")) {
			return fmt.Errorf("mcp %s token_header must be a valid HTTP header name", mcp.Name)
		}
		if strings.ContainsAny(mcp.TokenPrefix, "\r\n\x00") {
			return fmt.Errorf("mcp %s token_prefix cannot contain line breaks or NUL", mcp.Name)
		}
		if mcp.TokenContainerEnv != "" {
			if mcp.TokenInput == "" && mcp.TokenFileInput == "" && mcp.TokenEnvInput == "" {
				return fmt.Errorf("mcp %s token_container_env requires a token source input", mcp.Name)
			}
			if !environmentName.MatchString(mcp.TokenContainerEnv) {
				return fmt.Errorf("mcp %s token_container_env has invalid environment variable name %q", mcp.Name, mcp.TokenContainerEnv)
			}
		}
		usedEnvironment := map[string]bool{}
		for name := range mcp.Env {
			if !environmentName.MatchString(name) {
				return fmt.Errorf("mcp %s env has invalid environment variable name %q", mcp.Name, name)
			}
			usedEnvironment[name] = true
		}
		for name, inputName := range mcp.EnvInputs {
			if !environmentName.MatchString(name) || usedEnvironment[name] {
				return fmt.Errorf("mcp %s env_inputs has invalid or duplicate environment variable %q", mcp.Name, name)
			}
			usedEnvironment[name] = true
			input, ok := inputsByName[inputName]
			if !ok || input.Type == "secret" || input.Type == "file" || input.Type == "directory" || input.Type == "multichoice" || input.Type == "multiple-choice" {
				return fmt.Errorf("mcp %s env_inputs %s must reference a declared non-secret scalar input", mcp.Name, name)
			}
		}
		for name, inputName := range mcp.SecretEnvInputs {
			if !environmentName.MatchString(name) || usedEnvironment[name] {
				return fmt.Errorf("mcp %s secret_env_inputs has invalid or duplicate environment variable %q", mcp.Name, name)
			}
			usedEnvironment[name] = true
			input, ok := inputsByName[inputName]
			if !ok || input.Type != "secret" {
				return fmt.Errorf("mcp %s secret_env_inputs %s must reference a declared secret input", mcp.Name, name)
			}
		}
		if mcp.RegistrationNameInput == "" {
			continue
		}
		input, ok := inputsByName[mcp.RegistrationNameInput]
		if !ok {
			return fmt.Errorf("mcp %s: registration_name_input %q is not declared", mcp.Name, mcp.RegistrationNameInput)
		}
		if input.Type != "string" {
			return fmt.Errorf("mcp %s: registration_name_input %q must reference a string input", mcp.Name, mcp.RegistrationNameInput)
		}
		if len(input.VisibleWhen) > 0 {
			return fmt.Errorf("mcp %s: registration_name_input %q must be unconditionally visible", mcp.Name, mcp.RegistrationNameInput)
		}
	}
	for _, in := range p.Inputs {
		for controller, expected := range in.VisibleWhen {
			if !seen[controller] {
				return fmt.Errorf("input %s: unknown controller %q in visible_when", in.Name, controller)
			}
			switch expected.(type) {
			case string, int64, float64, bool:
			default:
				return fmt.Errorf("input %s: visible_when values must be scalar equality values", in.Name)
			}
		}
	}
	if p.UI != nil {
		sectionIDs := map[string]bool{}
		assigned := map[string]bool{}
		for _, section := range p.UI.Sections {
			if section.ID == "" || section.Title == "" {
				return fmt.Errorf("ui section id and title are required")
			}
			if sectionIDs[section.ID] {
				return fmt.Errorf("duplicate ui section %q", section.ID)
			}
			sectionIDs[section.ID] = true
			for _, field := range section.Fields {
				if !seen[field] {
					return fmt.Errorf("ui section %s: unknown input %q", section.ID, field)
				}
				if assigned[field] {
					return fmt.Errorf("ui input %q is assigned more than once", field)
				}
				assigned[field] = true
			}
		}
		for name := range seen {
			if !assigned[name] {
				return fmt.Errorf("ui input %q is unassigned", name)
			}
		}
	}
	if p.Generator != nil {
		if err := validateCommand(*p.Generator); err != nil {
			return fmt.Errorf("generator: %w", err)
		}
	}
	if p.MCP != nil {
		for name, c := range p.MCP.Actions {
			if err := validateCommand(c); err != nil {
				return fmt.Errorf("mcp action %s: %w", name, err)
			}
		}
	}
	for _, mcp := range p.MCPs {
		for name, c := range mcp.Actions {
			if err := validateCommand(c); err != nil {
				return fmt.Errorf("mcp %s action %s: %w", mcp.Name, name, err)
			}
		}
	}
	return nil
}
func validateCommand(c Command) error {
	if len(c.Argv) == 0 || c.Argv[0] == "" {
		return fmt.Errorf("command is required")
	}
	if c.TimeoutSeconds < 0 {
		return fmt.Errorf("timeout_seconds must be positive")
	}
	if c.Windows != nil {
		return validateCommand(*c.Windows)
	}
	return nil
}

// A missing timeout receives a default, while an explicitly declared timeout must be positive.
func validateDeclaredTimeouts(data []byte) error {
	var raw map[string]any
	if err := toml.Unmarshal(data, &raw); err != nil {
		return err
	}
	if generator, ok := raw["generator"].(map[string]any); ok {
		if err := declaredTimeout(generator); err != nil {
			return fmt.Errorf("generator: %w", err)
		}
	}
	if mcp, ok := raw["mcp"].(map[string]any); ok {
		if actions, ok := mcp["actions"].(map[string]any); ok {
			for name, value := range actions {
				if command, ok := value.(map[string]any); ok {
					if err := declaredTimeout(command); err != nil {
						return fmt.Errorf("mcp action %s: %w", name, err)
					}
				}
			}
		}
	}
	if mcps, ok := raw["mcps"].([]any); ok {
		for i, value := range mcps {
			mcp, ok := value.(map[string]any)
			if !ok {
				continue
			}
			if actions, ok := mcp["actions"].(map[string]any); ok {
				for name, value := range actions {
					if command, ok := value.(map[string]any); ok {
						if err := declaredTimeout(command); err != nil {
							return fmt.Errorf("mcp %d action %s: %w", i+1, name, err)
						}
					}
				}
			}
		}
	}
	return nil
}
func declaredTimeout(command map[string]any) error {
	if value, ok := command["timeout_seconds"]; ok {
		if seconds, ok := value.(int64); !ok || seconds <= 0 {
			return fmt.Errorf("timeout_seconds must be positive")
		}
	}
	if windows, ok := command["windows"].(map[string]any); ok {
		return declaredTimeout(windows)
	}
	return nil
}
