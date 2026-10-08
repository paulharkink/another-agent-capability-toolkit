package catalog

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/expressions"
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
		p := Package{SchemaVersion: 1, ID: name, ManifestID: name, Name: name, Dir: abs, Skill: &Skill{Name: name}}
		return p, Validate(p)
	}
	if err != nil {
		return Package{}, err
	}
	var document map[string]any
	if err = toml.Unmarshal(data, &document); err != nil {
		return Package{}, fmt.Errorf("%s: %w", path, err)
	}
	evaluated, err := expressions.EvaluateTreeDeferringRoot(document, expressions.DocumentEnvironment(document), path, "inputs")
	if err != nil {
		return Package{}, err
	}
	decodeTree := deferRuntimeForSchema(evaluated, reflect.TypeOf(Package{}))
	p, err := decodePackage(decodeTree)
	if err != nil {
		if strict, ok := err.(*toml.StrictMissingError); ok {
			return Package{}, fmt.Errorf("%s: %s", path, strict.String())
		}
		return Package{}, fmt.Errorf("%s: %w", path, err)
	}
	p.Dir = abs
	p.ManifestID = p.ID
	p.RawManifest = document
	evaluatedBytes, err := toml.Marshal(evaluated)
	if err != nil {
		return Package{}, fmt.Errorf("%s: %w", path, err)
	}
	if err = validateDeclaredTimeouts(evaluatedBytes); err != nil {
		// Runtime expressions are checked after a selected input map is available.
		if !hasDeferredInputTimeout(evaluated) {
			return Package{}, fmt.Errorf("%s: %w", path, err)
		}
	}
	return finalizePackage(p)
}

// deferRuntimeForSchema substitutes only a discovery-time placeholder where a
// runtime HCL string cannot be decoded into its eventual TOML type. The raw
// tree is retained and fully reevaluated before use; this is not the value the
// application receives.
func deferRuntimeForSchema(value any, typ reflect.Type) any {
	if typ.Kind() == reflect.Ptr {
		typ = typ.Elem()
	}
	if text, ok := value.(string); ok && expressions.ReferencesRoot(text, "inputs") {
		switch typ.Kind() {
		case reflect.Bool:
			return false
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			return int64(0)
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			return uint64(0)
		case reflect.Float32, reflect.Float64:
			return float64(0)
		case reflect.Slice:
			return []any{}
		case reflect.Map:
			return map[string]any{}
		case reflect.Struct:
			return map[string]any{}
		}
		return value
	}
	switch node := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(node))
		if typ.Kind() == reflect.Struct {
			fields := map[string]reflect.Type{}
			for i := 0; i < typ.NumField(); i++ {
				field := typ.Field(i)
				name := strings.Split(field.Tag.Get("toml"), ",")[0]
				if name == "" {
					name = field.Name
				}
				if name != "-" {
					fields[name] = field.Type
				}
			}
			for key, child := range node {
				fieldType := reflect.TypeOf((*any)(nil)).Elem()
				if known, ok := fields[key]; ok {
					fieldType = known
				}
				out[key] = deferRuntimeForSchema(child, fieldType)
			}
			return out
		}
		valueType := reflect.TypeOf((*any)(nil)).Elem()
		if typ.Kind() == reflect.Map {
			valueType = typ.Elem()
		}
		for key, child := range node {
			out[key] = deferRuntimeForSchema(child, valueType)
		}
		return out
	case []any:
		out := make([]any, len(node))
		elemType := reflect.TypeOf((*any)(nil)).Elem()
		if typ.Kind() == reflect.Slice || typ.Kind() == reflect.Array {
			elemType = typ.Elem()
		}
		for i, child := range node {
			out[i] = deferRuntimeForSchema(child, elemType)
		}
		return out
	default:
		return value
	}
}

// ResolveExpressions evaluates retained manifest expressions against selected
// profile inputs and returns a resolved copy. Package discovery can therefore
// finish before a profile exists without losing expressions in runtime fields.
func ResolveExpressions(p Package, inputs map[string]any) (Package, error) {
	if len(p.RawManifest) == 0 {
		return p, nil
	}
	environment := expressions.DocumentEnvironment(p.RawManifest)
	environment["inputs"] = inputs
	evaluated, err := expressions.EvaluateTree(p.RawManifest, environment, filepath.Join(p.Dir, "package.toml"))
	if err != nil {
		return Package{}, err
	}
	resolved, err := decodePackage(evaluated)
	if err != nil {
		return Package{}, fmt.Errorf("%s: %w", filepath.Join(p.Dir, "package.toml"), err)
	}
	resolved.Dir, resolved.ManifestID, resolved.RawManifest = p.Dir, p.ManifestID, p.RawManifest
	resolved.ID, resolved.Sets = p.ID, p.Sets
	evaluatedBytes, err := toml.Marshal(evaluated)
	if err != nil {
		return Package{}, err
	}
	if err := validateDeclaredTimeouts(evaluatedBytes); err != nil {
		return Package{}, fmt.Errorf("%s: %w", filepath.Join(p.Dir, "package.toml"), err)
	}
	return finalizePackage(resolved)
}

// ResolveInputDefinitions evaluates input declarations that depend on the
// selected profile's already-resolved values, without evaluating runtime
// capability fields prematurely.
func ResolveInputDefinitions(p Package, inputs map[string]any) ([]Input, error) {
	if len(p.RawManifest) == 0 {
		return p.Inputs, nil
	}
	rawInputs, ok := p.RawManifest["inputs"]
	if !ok {
		return p.Inputs, nil
	}
	working, ok := rawInputs.([]any)
	if !ok {
		return nil, fmt.Errorf("%s: inputs must be an array", filepath.Join(p.Dir, "package.toml"))
	}
	working = cloneManifestValue(working).([]any)
	values := make(map[string]any, len(inputs))
	for name, value := range inputs {
		values[name] = value
	}
	pendingError := error(nil)
	for attempts := 0; attempts <= len(working); attempts++ {
		progress := false
		for i, item := range working {
			field, ok := item.(map[string]any)
			if !ok {
				continue
			}
			name, _ := field["name"].(string)
			text, isText := field["default"].(string)
			if !isText || !expressions.ReferencesRoot(text, "inputs") {
				continue
			}
			if _, supplied := values[name]; supplied {
				field["default"] = nil
				continue
			}
			environment := expressions.DocumentEnvironment(p.RawManifest)
			environment["inputs"] = values
			value, evalErr := expressions.Evaluate(text, environment, filepath.Join(p.Dir, "package.toml"), fmt.Sprintf("inputs[%d].default", i))
			if evalErr != nil {
				pendingError = evalErr
				continue
			}
			field["default"], values[name], progress = value, value, true
		}
		if !progress {
			break
		}
	}
	for _, item := range working {
		if field, ok := item.(map[string]any); ok {
			if text, isText := field["default"].(string); isText && expressions.ReferencesRoot(text, "inputs") {
				if pendingError != nil {
					return nil, pendingError
				}
				return nil, fmt.Errorf("%s: unresolved input default expression %q", filepath.Join(p.Dir, "package.toml"), text)
			}
		}
	}
	environment := expressions.DocumentEnvironment(p.RawManifest)
	environment["inputs"] = values
	resolved, err := expressions.EvaluateTree(working, environment, filepath.Join(p.Dir, "package.toml"))
	if err != nil {
		return nil, err
	}
	encoded, err := toml.Marshal(map[string]any{"inputs": resolved})
	if err != nil {
		return nil, err
	}
	var decoded struct {
		Inputs []Input `toml:"inputs"`
	}
	if err := toml.Unmarshal(encoded, &decoded); err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Join(p.Dir, "package.toml"), err)
	}
	return decoded.Inputs, nil
}

func cloneManifestValue(value any) any {
	switch node := value.(type) {
	case map[string]any:
		copy := make(map[string]any, len(node))
		for key, child := range node {
			copy[key] = cloneManifestValue(child)
		}
		return copy
	case []any:
		copy := make([]any, len(node))
		for index, child := range node {
			copy[index] = cloneManifestValue(child)
		}
		return copy
	default:
		return value
	}
}

func hasDeferredInputTimeout(value any) bool {
	// This is only the discovery-time exception for an expression whose runtime
	// input is not selected yet. The resolved copy always gets strict validation.
	data, err := toml.Marshal(value)
	if err != nil {
		return false
	}
	var raw map[string]any
	if toml.Unmarshal(data, &raw) != nil {
		return false
	}
	var walk func(any) bool
	walk = func(node any) bool {
		switch n := node.(type) {
		case map[string]any:
			if text, ok := n["timeout_seconds"].(string); ok && expressions.ReferencesRoot(text, "inputs") {
				return true
			}
			for _, child := range n {
				if walk(child) {
					return true
				}
			}
		case []any:
			for _, child := range n {
				if walk(child) {
					return true
				}
			}
		}
		return false
	}
	return walk(raw)
}

func decodePackage(value any) (Package, error) {
	data, err := toml.Marshal(value)
	if err != nil {
		return Package{}, err
	}
	var p Package
	err = toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields().Decode(&p)
	return p, err
}

func finalizePackage(p Package) (Package, error) {
	if err := Validate(p); err != nil {
		return Package{}, fmt.Errorf("%s: %w", filepath.Join(p.Dir, "package.toml"), err)
	}
	if p.Generator != nil {
		defaultCommand(p.Generator)
	}
	for i := range p.Skills {
		if p.Skills[i].Source == "" {
			p.Skills[i].Source = p.Dir
		} else if !strings.Contains(p.Skills[i].Source, "${") && !filepath.IsAbs(p.Skills[i].Source) {
			p.Skills[i].Source = filepath.Join(p.Dir, p.Skills[i].Source)
		}
		if p.Skills[i].Generator != nil {
			defaultCommand(p.Skills[i].Generator)
		}
	}
	for i := range p.Plugins {
		if !strings.Contains(p.Plugins[i].Source, "${") && !filepath.IsAbs(p.Plugins[i].Source) {
			p.Plugins[i].Source = filepath.Join(p.Dir, p.Plugins[i].Source)
		}
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
	if !p.HasSkill() && !p.HasMCP() && len(p.Plugins) == 0 {
		return fmt.Errorf("package must declare skill, mcp or plugin")
	}
	if p.Skill != nil && len(p.Skills) > 0 {
		return fmt.Errorf("cannot mix [skill] and [[skills]] declarations")
	}
	skillNames := map[string]bool{}
	for _, s := range p.SkillDefinitions() {
		if !identifier.MatchString(s.Name) {
			return fmt.Errorf("invalid skill name %q", s.Name)
		}
		if skillNames[s.Name] {
			return fmt.Errorf("duplicate skill name %q", s.Name)
		}
		skillNames[s.Name] = true
		if s.Generator != nil {
			if err := validateCommand(*s.Generator); err != nil {
				return fmt.Errorf("skill %s generator: %w", s.Name, err)
			}
		}
	}
	pluginNames := map[string]bool{}
	for _, plugin := range p.Plugins {
		if !identifier.MatchString(plugin.Name) || !identifier.MatchString(plugin.Format) {
			return fmt.Errorf("invalid plugin name or format %q", plugin.Name)
		}
		if pluginNames[plugin.Name] {
			return fmt.Errorf("duplicate plugin name %q", plugin.Name)
		}
		pluginNames[plugin.Name] = true
		if plugin.Source == "" {
			return fmt.Errorf("plugin %s source is required", plugin.Name)
		}
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
		if in.Regex != "" {
			if _, err := regexp.Compile(in.Regex); err != nil {
				return fmt.Errorf("input %s regex %q: %w", in.Name, in.Regex, err)
			}
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
			{"bind_ip_input", mcp.BindIPInput, "string"},
			{"advertised_host_input", mcp.AdvertisedHostInput, "string"},
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
		if mcp.TokenHeader != "" && !strings.Contains(mcp.TokenHeader, "${") && (strings.TrimSpace(mcp.TokenHeader) != mcp.TokenHeader || strings.ContainsAny(mcp.TokenHeader, ":\r\n\x00")) {
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
	for _, plugin := range p.Plugins {
		if plugin.EnabledInput != "" {
			in, ok := inputsByName[plugin.EnabledInput]
			if !ok || in.Type != "boolean" {
				return fmt.Errorf("plugin %s enabled_input %q must reference a declared boolean input", plugin.Name, plugin.EnabledInput)
			}
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
	if skills, ok := raw["skills"].([]any); ok {
		for i, value := range skills {
			if skill, ok := value.(map[string]any); ok {
				if generator, ok := skill["generator"].(map[string]any); ok {
					if err := declaredTimeout(generator); err != nil {
						return fmt.Errorf("skill %d generator: %w", i+1, err)
					}
				}
			}
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
