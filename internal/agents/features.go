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
	Home, SkillsPath           string
	State, Evidence, Reason    string
	Installed, CanCreateConfig bool
	ConfigFiles                []ConfigFile
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
	Key       state.Key
	Plugin    catalog.Plugin
	StagedDir string
}
type SkillManager interface {
	InstallSkill(context.Context, Scope, SkillRequest) (state.Installation, error)
	RemoveSkill(context.Context, Scope, state.Installation) error
}
type MCPManager interface {
	Register(context.Context, Scope, MCPRequest) (state.Installation, error)
	Unregister(context.Context, Scope, state.Installation) error
}
type PluginManager interface {
	InstallPlugin(context.Context, Scope, PluginRequest) (state.Installation, error)
	RemovePlugin(context.Context, Scope, state.Installation) error
}
