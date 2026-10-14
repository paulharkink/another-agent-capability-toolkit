package viewmodel

import (
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"time"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
)

// Instance keeps configured identity, local registrations, and live runtime
// observation separate for the terminal UI.
type Instance struct {
	Key                        state.Key
	Name                       string
	URL                        string
	Transport                  string
	RuntimeStatus              string
	Ownership                  string
	LastAction                 string
	LastActionAt               time.Time
	LocalLastAction            string
	LocalLastActionAt          time.Time
	ObservedAt                 time.Time
	ObservationStale           bool
	RegisteredAgents           []string
	CanStart                   bool
	CanStop                    bool
	CanConfigureRegistrations  bool
	StartDisabledReason        string
	StopDisabledReason         string
	RegistrationDisabledReason string
}

// Profile is a source-compatibility alias for the former selected-instance model.
type Profile = Instance

type ConnectionObservation struct {
	URL       string
	CheckedAt time.Time
	Reachable bool
	Error     string
}

type InstanceSnapshot struct {
	Instances []Instance
	// Profiles is populated for source compatibility with old callers.
	Profiles         []Instance
	DockerError      string
	ObservedAt       time.Time
	ObservationStale bool
}

type ProfileSnapshot = InstanceSnapshot

type CapabilityInstanceSnapshot struct {
	Instances []CapabilityInstance
	// Profiles is populated for source compatibility with old callers.
	Profiles   []CapabilityInstance
	Errors     []string
	ObservedAt time.Time
}
type CapabilityInstance struct {
	Ref                               config.InstanceRef
	Key                               state.Key
	Origin, ConfigStatus, ConfigError string
	Components                        []ComponentStatus
	MCPs                              []RuntimeStatus
	// Completion scope records saved/default selection intent separately from
	// ComponentStatus observations. Only selected component/destination pairs
	// that the adapter can handle belong in Installed/Partial aggregation.
	CompletionScopeKnown    bool
	CompletionScopeRelevant bool
	CompletionScopeError    string
	SelectedComponents      []SelectedComponent
	SelectedDestinations    []string
	EligibleDestinations    []string
}
type CapabilityProfileSnapshot = CapabilityInstanceSnapshot
type CapabilityProfile = CapabilityInstance
type SelectedComponent struct {
	Kind, Name string
}
type ComponentStatus struct {
	AgentID, Kind, Name, Status, Error string
	Managed                            bool
}
type RuntimeStatus struct {
	MCPID, Status, Ownership, URL, Error string
	ObservedAt                           time.Time
	Stale                                bool
}
