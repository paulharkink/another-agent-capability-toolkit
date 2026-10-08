package catalog

// Package declares the components and typed inputs of a catalog entry.
type Package struct {
	SchemaVersion int            `toml:"schema_version" json:"schema_version"`
	ID            string         `toml:"id" json:"id"`
	Name          string         `toml:"name" json:"name"`
	Dir           string         `toml:"-" json:"dir"`
	Skill         *Skill         `toml:"skill" json:"skill"`
	Skills        []Skill        `toml:"skills" json:"skills,omitempty"`
	Plugins       []Plugin       `toml:"plugins" json:"plugins,omitempty"`
	Sets          []ComponentSet `toml:"-" json:"sets,omitempty"`
	ManifestID    string         `toml:"-" json:"manifest_id,omitempty"`
	MCP           *MCP           `toml:"mcp" json:"mcp"`
	MCPs          []MCP          `toml:"mcps" json:"mcps"`
	UI            *Presentation  `toml:"ui" json:"ui"`
	Inputs        []Input        `toml:"inputs" json:"inputs"`
	Templates     []Template     `toml:"templates" json:"templates"`
	Generator     *Command       `toml:"generator" json:"generator"`
}

func (p Package) MCPDefinitions() []MCP {
	return p.MCPProfiles()
}

func (p Package) HasMCP() bool { return len(p.MCPProfiles()) > 0 }

// MCPProfiles returns every declared MCP in declaration order while retaining
// compatibility with the original single [mcp] manifest form.
func (p Package) MCPProfiles() []MCP {
	profiles := make([]MCP, 0, len(p.MCPs)+1)
	if p.MCP != nil {
		profiles = append(profiles, *p.MCP)
	}
	profiles = append(profiles, p.MCPs...)
	return profiles
}

type Presentation struct {
	Sections []Section `toml:"sections" json:"sections"`
}
type Section struct {
	ID     string   `toml:"id" json:"id"`
	Title  string   `toml:"title" json:"title"`
	Fields []string `toml:"fields" json:"fields"`
}
type Skill struct {
	Name      string     `toml:"name" json:"name"`
	Source    string     `toml:"source" json:"source"`
	Files     []string   `toml:"files" json:"files"`
	Templates []Template `toml:"templates" json:"templates,omitempty"`
	Generator *Command   `toml:"generator" json:"generator,omitempty"`
}
type Plugin struct {
	Name         string `toml:"name" json:"name"`
	Format       string `toml:"format" json:"format"`
	Source       string `toml:"source" json:"source"`
	EnabledInput string `toml:"enabled_input" json:"enabled_input,omitempty"`
}
type Input struct {
	Name           string         `toml:"name" json:"name"`
	Regex          string         `toml:"regex" json:"regex,omitempty"`
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
	CredentialFiles       []string           `toml:"credential_files" json:"credential_files,omitempty"`
	Name                  string             `toml:"name" json:"name"`
	EnabledInput          string             `toml:"enabled_input" json:"enabled_input,omitempty"`
	RegistrationNameInput string             `toml:"registration_name_input" json:"registration_name_input,omitempty"`
	TokenInput            string             `toml:"token_input" json:"token_input,omitempty"`
	TokenFileInput        string             `toml:"token_file_input" json:"token_file_input,omitempty"`
	TokenEnvInput         string             `toml:"token_env_input" json:"token_env_input,omitempty"`
	TokenHeader           string             `toml:"token_header" json:"token_header,omitempty"`
	TokenPrefix           string             `toml:"token_prefix" json:"token_prefix,omitempty"`
	TokenContainerEnv     string             `toml:"token_container_env" json:"token_container_env,omitempty"`
	Args                  []string           `toml:"args" json:"args,omitempty"`
	Env                   map[string]string  `toml:"env" json:"env,omitempty"`
	EnvInputs             map[string]string  `toml:"env_inputs" json:"env_inputs,omitempty"`
	SecretEnvInputs       map[string]string  `toml:"secret_env_inputs" json:"secret_env_inputs,omitempty"`
	Runtime               string             `toml:"runtime" json:"runtime"`
	BuildContext          string             `toml:"build_context" json:"build_context"`
	Image                 string             `toml:"image" json:"image"`
	Transport             string             `toml:"transport" json:"transport"`
	ContainerPort         int                `toml:"container_port" json:"container_port"`
	EndpointPath          string             `toml:"endpoint_path" json:"endpoint_path"`
	HostPortInput         string             `toml:"host_port_input" json:"host_port_input"`
	BindIPInput           string             `toml:"bind_ip_input" json:"bind_ip_input,omitempty"`
	AdvertisedHostInput   string             `toml:"advertised_host_input" json:"advertised_host_input,omitempty"`
	RegistrationTimeoutMS int                `toml:"registration_timeout_ms" json:"registration_timeout_ms"`
	Actions               map[string]Command `toml:"actions" json:"actions"`
}
