package viewmodel

import "github.com/paulharkink/another-agent-capability-toolkit/internal/state"

type RegistrationRequest struct {
	Key       state.Key
	URL       string
	Transport string
	AgentIDs  []string
}

type OperationResult struct {
	Changes    []state.Installation
	Errors     []string
	Saved      bool
	Message    string
	Step       string
	Target     string
	Connection ConnectionObservation
}
