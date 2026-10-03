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
	if p.Skill == nil && p.MCP == nil {
		return fmt.Errorf("package must declare skill or mcp")
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
