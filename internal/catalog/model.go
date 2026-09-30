package catalog

// Package declares the components and typed inputs of a catalog entry.
type Package struct {
	SchemaVersion int        `toml:"schema_version" json:"schema_version"`
	ID            string     `toml:"id" json:"id"`
	Name          string     `toml:"name" json:"name"`
	Dir           string     `toml:"-" json:"dir"`
	Skill         *Skill     `toml:"skill" json:"skill"`
	MCP           *MCP       `toml:"mcp" json:"mcp"`
	Inputs        []Input    `toml:"inputs" json:"inputs"`
	Templates     []Template `toml:"templates" json:"templates"`
	Generator     *Command   `toml:"generator" json:"generator"`
}
type Skill struct {
	Name  string   `toml:"name" json:"name"`
	Files []string `toml:"files" json:"files"`
}
type Input struct {
	Name      string   `toml:"name" json:"name"`
	ConfigKey string   `toml:"config_key" json:"config_key"`
	Type      string   `toml:"type" json:"type"`
	Label     string   `toml:"label" json:"label"`
	Required  bool     `toml:"required" json:"required"`
	Multiple  bool     `toml:"multiple" json:"multiple"`
	Default   any      `toml:"default" json:"default"`
	Min       *float64 `toml:"min" json:"min"`
	Max       *float64 `toml:"max" json:"max"`
	MinItems  *int     `toml:"min_items" json:"min_items"`
	MaxItems  *int     `toml:"max_items" json:"max_items"`
	Options   []Choice `toml:"options" json:"options"`
}
type Choice struct {
	Value string `toml:"value" json:"value"`
	Label string `toml:"label" json:"label"`
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
	Name          string             `toml:"name" json:"name"`
	Runtime       string             `toml:"runtime" json:"runtime"`
	BuildContext  string             `toml:"build_context" json:"build_context"`
	Image         string             `toml:"image" json:"image"`
	Transport     string             `toml:"transport" json:"transport"`
	ContainerPort int                `toml:"container_port" json:"container_port"`
	EndpointPath  string             `toml:"endpoint_path" json:"endpoint_path"`
	HostPortInput string             `toml:"host_port_input" json:"host_port_input"`
	Actions       map[string]Command `toml:"actions" json:"actions"`
}
