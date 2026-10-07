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
	for _, profile := range p.MCPProfiles() {
		for name, c := range profile.Actions {
			defaultCommand(&c)
			profile.Actions[name] = c
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
	profiles := p.MCPProfiles()
	if p.Skill == nil && len(profiles) == 0 {
		return fmt.Errorf("package must declare skill or mcp")
	}
	if p.Skill != nil && !identifier.MatchString(p.Skill.Name) {
		return fmt.Errorf("invalid skill name %q", p.Skill.Name)
	}
	seen := map[string]bool{}
	for _, in := range p.Inputs {
		if !identifier.MatchString(in.Name) {
			return fmt.Errorf("invalid input name %q", in.Name)
		}
		if seen[in.Name] {
			return fmt.Errorf("duplicate input %q", in.Name)
		}
		seen[in.Name] = true
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
	profileNames := map[string]bool{}
	inputsByName := make(map[string]Input, len(p.Inputs))
	for _, in := range p.Inputs {
		inputsByName[in.Name] = in
	}
	for _, profile := range profiles {
		if !identifier.MatchString(profile.Name) {
			return fmt.Errorf("invalid mcp name %q", profile.Name)
		}
		if profileNames[profile.Name] {
			return fmt.Errorf("duplicate mcp profile name %q", profile.Name)
		}
		profileNames[profile.Name] = true
		if profile.RegistrationTimeoutMS < 0 {
			return fmt.Errorf("mcp %s registration_timeout_ms must be positive", profile.Name)
		}
		if profile.EnabledInput != "" {
			in, ok := inputsByName[profile.EnabledInput]
			if !ok {
				return fmt.Errorf("mcp %s enabled_input %q does not name a package input", profile.Name, profile.EnabledInput)
			}
			if in.Type != "boolean" {
				return fmt.Errorf("mcp %s enabled_input %q must be boolean", profile.Name, profile.EnabledInput)
			}
		}
		for _, ref := range []struct{ field, name, wantType string }{
			{"registration_name_input", profile.RegistrationNameInput, "string"},
			{"token_input", profile.TokenInput, "secret"},
			{"token_file_input", profile.TokenFileInput, "file"},
			{"token_env_input", profile.TokenEnvInput, "string"},
		} {
			if ref.name == "" {
				continue
			}
			in, ok := inputsByName[ref.name]
			if !ok {
				return fmt.Errorf("mcp %s %s %q does not name a package input", profile.Name, ref.field, ref.name)
			}
			if in.Type != ref.wantType {
				return fmt.Errorf("mcp %s %s %q must reference a %s input", profile.Name, ref.field, ref.name, ref.wantType)
			}
		}
		if profile.TokenHeader != "" && profile.TokenContainerEnv != "" {
			return fmt.Errorf("mcp %s cannot set both token_header and token_container_env", profile.Name)
		}
		if profile.TokenHeader != "" && profile.TokenInput == "" && profile.TokenFileInput == "" && profile.TokenEnvInput == "" {
			return fmt.Errorf("mcp %s token_header requires a token source input", profile.Name)
		}
		if profile.TokenHeader != "" && (strings.TrimSpace(profile.TokenHeader) != profile.TokenHeader || strings.ContainsAny(profile.TokenHeader, ":\r\n\x00")) {
			return fmt.Errorf("mcp %s token_header must be a valid HTTP header name", profile.Name)
		}
		if strings.ContainsAny(profile.TokenPrefix, "\r\n\x00") {
			return fmt.Errorf("mcp %s token_prefix cannot contain line breaks or NUL", profile.Name)
		}
		if profile.TokenContainerEnv != "" && profile.TokenInput == "" && profile.TokenFileInput == "" && profile.TokenEnvInput == "" {
			return fmt.Errorf("mcp %s token_container_env requires a token source input", profile.Name)
		}
		if profile.HostPortInput != "" {
			in, ok := inputsByName[profile.HostPortInput]
			if !ok {
				return fmt.Errorf("mcp %s host_port_input %q does not name a package input", profile.Name, profile.HostPortInput)
			}
			if in.Type != "integer" {
				return fmt.Errorf("mcp %s host_port_input %q must be integer", profile.Name, profile.HostPortInput)
			}
		}
		usedEnvironment := map[string]bool{}
		for name := range profile.Env {
			if !environmentName.MatchString(name) {
				return fmt.Errorf("mcp %s env has invalid environment variable name %q", profile.Name, name)
			}
			usedEnvironment[name] = true
		}
		for name, inputName := range profile.EnvInputs {
			if !environmentName.MatchString(name) {
				return fmt.Errorf("mcp %s env_inputs has invalid environment variable name %q", profile.Name, name)
			}
			if usedEnvironment[name] {
				return fmt.Errorf("mcp %s environment variable %q is declared more than once", profile.Name, name)
			}
			usedEnvironment[name] = true
			input, ok := inputsByName[inputName]
			if !ok {
				return fmt.Errorf("mcp %s env_inputs %s references unknown input %q", profile.Name, name, inputName)
			}
			if input.Type == "secret" || input.Type == "file" || input.Type == "directory" || input.Type == "multichoice" || input.Type == "multiple-choice" {
				return fmt.Errorf("mcp %s env_inputs %s must reference a non-secret scalar input", profile.Name, name)
			}
		}
		for name, inputName := range profile.SecretEnvInputs {
			if !environmentName.MatchString(name) {
				return fmt.Errorf("mcp %s secret_env_inputs has invalid environment variable name %q", profile.Name, name)
			}
			if usedEnvironment[name] {
				return fmt.Errorf("mcp %s environment variable %q is declared more than once", profile.Name, name)
			}
			usedEnvironment[name] = true
			input, ok := inputsByName[inputName]
			if !ok {
				return fmt.Errorf("mcp %s secret_env_inputs %s references unknown input %q", profile.Name, name, inputName)
			}
			if input.Type != "secret" {
				return fmt.Errorf("mcp %s secret_env_inputs %s must reference a secret input", profile.Name, name)
			}
		}
		if profile.TokenContainerEnv != "" && !environmentName.MatchString(profile.TokenContainerEnv) {
			return fmt.Errorf("mcp %s token_container_env has invalid environment variable name %q", profile.Name, profile.TokenContainerEnv)
		}
		for name, c := range profile.Actions {
			if err := validateCommand(c); err != nil {
				return fmt.Errorf("mcp %s action %s: %w", profile.Name, name, err)
			}
		}
	}
	if p.Generator != nil {
		if err := validateCommand(*p.Generator); err != nil {
			return fmt.Errorf("generator: %w", err)
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
		for index, value := range mcps {
			profile, ok := value.(map[string]any)
			if !ok {
				continue
			}
			if actions, ok := profile["actions"].(map[string]any); ok {
				for name, value := range actions {
					if command, ok := value.(map[string]any); ok {
						if err := declaredTimeout(command); err != nil {
							return fmt.Errorf("mcp profile %d action %s: %w", index+1, name, err)
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
