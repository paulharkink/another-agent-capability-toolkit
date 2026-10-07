package catalog

// Package declares the components and typed inputs of a catalog entry.
type Package struct {
	SchemaVersion int           `toml:"schema_version" json:"schema_version"`
	ID            string        `toml:"id" json:"id"`
	Name          string        `toml:"name" json:"name"`
	Dir           string        `toml:"-" json:"dir"`
	Skill         *Skill        `toml:"skill" json:"skill"`
	MCP           *MCP          `toml:"mcp" json:"mcp"`
	MCPs          []MCP         `toml:"mcps" json:"mcps"`
	UI            *Presentation `toml:"ui" json:"ui"`
	Inputs        []Input       `toml:"inputs" json:"inputs"`
	Templates     []Template    `toml:"templates" json:"templates"`
	Generator     *Command      `toml:"generator" json:"generator"`
}

func (p Package) MCPDefinitions() []MCP {
	if len(p.MCPs) > 0 {
		return append([]MCP(nil), p.MCPs...)
	}
	if p.MCP != nil {
		return []MCP{*p.MCP}
	}
	return nil
}

func (p Package) HasMCP() bool { return p.MCP != nil || len(p.MCPs) > 0 }

type Presentation struct {
	Sections []Section `toml:"sections" json:"sections"`
}
type Section struct {
	ID     string   `toml:"id" json:"id"`
	Title  string   `toml:"title" json:"title"`
	Fields []string `toml:"fields" json:"fields"`
}
type Skill struct {
	Name  string   `toml:"name" json:"name"`
	Files []string `toml:"files" json:"files"`
}
type Input struct {
	Name           string         `toml:"name" json:"name"`
	ConfigKey      string         `toml:"config_key" json:"config_key"`
	ExclusiveGroup string         `toml:"exclusive_group" json:"exclusive_group"`
	Type           string         `toml:"type" json:"type"`
	Label          string         `toml:"label" json:"label"`
	Hint           string         `toml:"hint" json:"hint"`
	Required       bool           `toml:"required" json:"required"`
	Multiple       bool           `toml:"multiple" json:"multiple"`
	Default        any            `toml:"default" json:"default"`
	Min            *float64       `toml:"min" json:"min"`
	Max            *float64       `toml:"max" json:"max"`
	MinItems       *int           `toml:"min_items" json:"min_items"`
	MaxItems       *int           `toml:"max_items" json:"max_items"`
	Options        []Choice       `toml:"options" json:"options"`
	OptionsFrom    string         `toml:"options_from" json:"options_from"`
	VisibleWhen    map[string]any `toml:"visible_when" json:"visible_when"`
}
type Choice struct {
	Value          string `toml:"value" json:"value"`
	Label          string `toml:"label" json:"label"`
	DisabledReason string `toml:"disabled_reason,omitempty" json:"disabled_reason,omitempty"`
}
type Template struct {
	Source      string `toml:"source" json:"source"`
	Destination string `toml:"destination" json:"destination"`
}
type Command struct {
	Argv           []string `toml:"command" json:"command"`
	Windows        *Command `toml:"windows" json:"windows"`
	TimeoutSeconds int      `toml:"timeout_seconds" json:"timeout_seconds"`
}
type MCP struct {
	Name                  string             `toml:"name" json:"name"`
	RegistrationNameInput string             `toml:"registration_name_input" json:"registration_name_input,omitempty"`
	Runtime               string             `toml:"runtime" json:"runtime"`
	BuildContext          string             `toml:"build_context" json:"build_context"`
	Image                 string             `toml:"image" json:"image"`
	Transport             string             `toml:"transport" json:"transport"`
	ContainerPort         int                `toml:"container_port" json:"container_port"`
	EndpointPath          string             `toml:"endpoint_path" json:"endpoint_path"`
	HostPortInput         string             `toml:"host_port_input" json:"host_port_input"`
	RegistrationTimeoutMS int                `toml:"registration_timeout_ms" json:"registration_timeout_ms"`
	Actions               map[string]Command `toml:"actions" json:"actions"`
}
