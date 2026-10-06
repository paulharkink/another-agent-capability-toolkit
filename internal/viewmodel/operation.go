package viewmodel

import "github.com/paulharkink/another-agent-capability-toolkit/internal/state"

type RegistrationRequest struct {
	Key            state.Key
	URL            string
	Transport      string
	AgentIDs       []string
	RemoveAgentIDs []string
	// RemoveRegistrations selects exact recorded local MCP registrations by
	// their existing ledger identity. It is separate from the agent-ID-only
	// compatibility field, which is accepted only when it resolves uniquely.
	RemoveRegistrations []RegistrationIdentity
}

type RegistrationIdentity struct {
	AgentID     string
	Destination string
}

type OperationResult struct {
	Changes []state.Installation
	Errors  []string
	Saved   bool
	// SavedApplicable reports whether this operation attempted to persist the
	// package's input configuration. When false, Saved is not presented as an
	// outcome; registration-only and observation operations do not save inputs.
	SavedApplicable bool
	Message         string
	Step            string
	Target          string
	Connection      ConnectionObservation
}
