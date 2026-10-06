package viewmodel

type AgentConfigFile struct {
	Path       string
	Scope      string
	Precedence string
	Evidence   string
	Profile    string
	Exists     bool
}

// AgentManagementRow reports client evidence separately from configuration
// presence. It does not imply a license, authentication, or write support.
type AgentManagementRow struct {
	ID, Name, Detection, Evidence, Note        string
	EffectiveConfigPath, WriteConfigPath, Home string
	ConfigFiles                                []AgentConfigFile
	Registrations                              []string
}
