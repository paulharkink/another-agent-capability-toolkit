package agents

import (
	"context"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"time"
)

type Scope struct {
	ID, Home, ConfigPathOverride string
	ExplicitHome                 bool
}
type FeatureSet struct {
	Skills, MCPs  bool
	PluginFormats []string
}
type ConfigFile struct {
	Path, Scope, Precedence, Evidence string
	Exists                            bool
}
type Detection struct {
	// ConfigPath is the effective file the adapter would write, after native
	// format precedence and any explicit scope override are applied.
	Home, SkillsPath, ConfigPath string
	State, Evidence, Reason      string
	MCPDisabledReason            string
	Installed, CanCreateConfig   bool
	ConfigFiles                  []ConfigFile
}
type ObservationRequest struct {
	Key              state.Key
	Managed          []state.Installation
	IncludeInventory bool
}
type ComponentObservation struct {
	Kind, Name, Status, Path, RegistrationName, URL, Transport, Error string
	Managed                                                           bool
}
type Observation struct {
	Detection   Detection
	Components  []ComponentObservation
	ConfigFiles []ConfigFile
	Errors      []string
	ObservedAt  time.Time
}

type Adapter interface {
	ID() string
	Name() string
	Features() FeatureSet
	Detect(context.Context, Scope) (Detection, error)
	Observe(context.Context, Scope, ObservationRequest) (Observation, error)
}

// ConfigPathProvider reports the exact file the adapter will mutate for a
// scope, including native format precedence and an explicit owned override.
type ConfigPathProvider interface {
	PlannedConfigPath(context.Context, Scope) (string, error)
}
type SkillRequest struct {
	Key       state.Key
	Package   catalog.Package
	Skill     catalog.Skill
	StagedDir string
}
type MCPRequest struct {
	Key          state.Key
	Registration Registration
}
type PluginRequest struct {
	Key                   state.Key
	Plugin                catalog.Plugin
	StagedDir             string
	MarketplaceConfigured bool
	Existing              *state.Installation
}

// PluginInstallResult reports the installed plugin separately from any
// marketplace effect that survived a later installation failure.
type PluginInstallResult struct {
	Installation state.Installation
	Effects      []state.Installation
	Removed      []state.Installation
}
type SkillManager interface {
	InstallSkill(context.Context, Scope, SkillRequest) (state.Installation, error)
	RemoveSkill(context.Context, Scope, state.Installation) error
}

// MCPRegistrationResult reports the desired installation and any external
// effects that completed before registration failed.
type MCPRegistrationResult struct {
	Installation state.Installation
	Effects      []state.Installation
	Removed      []state.Installation
}
type MCPManager interface {
	Register(context.Context, Scope, MCPRequest) (MCPRegistrationResult, error)
	Unregister(context.Context, Scope, state.Installation) error
}
type PluginManager interface {
	InstallPlugin(context.Context, Scope, PluginRequest) (PluginInstallResult, error)
	RemovePlugin(context.Context, Scope, state.Installation) error
}
