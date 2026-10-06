package viewmodel

import (
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
)

type SetupRequest struct {
	SourceID    string
	PackageID   string
	Environment string
	Target      string
}

type SetupInput struct {
	Definition     catalog.Input
	Value          any
	HasValue       bool
	Provenance     string
	ProvenancePath string
	Editable       bool
}

type SetupDestination struct {
	ID, Kind, Home, SkillsPath, ConfigPath, Detection, Note string
	Path                                                    string
	Selected                                                bool
}

type SetupPreview struct {
	Key          state.Key
	PackageName  string
	SourceRoot   string
	TargetPath   string
	Inputs       []SetupInput
	Destinations []SetupDestination
}

type SetupInstallRequest struct {
	SetupRequest
	Inputs         map[string]any
	DestinationIDs []string
}
