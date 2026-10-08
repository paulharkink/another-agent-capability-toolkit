package viewmodel

import (
	"github.com/paulharkink/another-agent-capability-toolkit/internal/agents"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
)

type SetupRequest struct {
	Ref         config.ProfileRef
	SourceID    string
	PackageID   string
	Environment string
	Target      string
}

type SetupInput struct {
	Definition        catalog.Input
	Value             any
	HasValue          bool
	Provenance        string
	ProvenancePath    string
	InheritedValue    any
	HasInheritedValue bool
	InheritedOrigin   string
	InheritedPath     string
	Editable          bool
}

type SetupDestination struct {
	Features                                                      agents.FeatureSet
	Name, ID, Kind, Home, SkillsPath, ConfigPath, Detection, Note string
	DisabledReason                                                string
	Path                                                          string
	Selected                                                      bool
}

type SetupPreview struct {
	ItemFieldName                                     string
	PluginDefinitions                                 []catalog.Plugin
	PackRoot, ProfilePath, ProfileTOML, ProfileOrigin string
	Items                                             []catalog.InstallationItem
	SelectedItemIDs, ValidationIssues                 []string
	Key                                               state.Key
	PackageName                                       string
	SourceRoot                                        string
	TargetPath                                        string
	TargetTOML                                        string
	Configured                                        bool
	MCP                                               bool
	Sections                                          []catalog.Section
	HasManifestUI                                     bool
	MCPDefinitions                                    []catalog.MCP
	CredentialState                                   string
	CredentialNote                                    string
	Inputs                                            []SetupInput
	Destinations                                      []SetupDestination
}

type SetupInstallRequest struct {
	ItemIDs []string
	SetupRequest
	Inputs         map[string]any
	ResetInputs    []string
	DestinationIDs []string
	ExternalURL    string
	ExternalURLs   map[string]string
}
