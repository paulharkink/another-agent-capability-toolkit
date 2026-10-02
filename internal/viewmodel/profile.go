package viewmodel

import (
	"time"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
)

// Profile keeps configured identity, local registrations, and live runtime
// observation separate for the terminal UI.
type Profile struct {
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

type ConnectionObservation struct {
	URL       string
	CheckedAt time.Time
	Reachable bool
	Error     string
}

type ProfileSnapshot struct {
	Profiles         []Profile
	DockerError      string
	ObservedAt       time.Time
	ObservationStale bool
}
