package viewmodel

import (
	"context"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
)

type operationProgressObserverContextKey struct{}

func WithOperationProgress(ctx context.Context, observer func(OperationProgress)) context.Context {
	return context.WithValue(ctx, operationProgressObserverContextKey{}, observer)
}

func OperationProgressObserver(ctx context.Context) func(OperationProgress) {
	observer, _ := ctx.Value(operationProgressObserverContextKey{}).(func(OperationProgress))
	return observer
}

type RegistrationRequest struct {
	Ref            config.ProfileRef
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
	// AuthenticationStatus reports only authentication established during this
	// apply operation. Saved credentials and credential-file presence do not
	// imply completion.
	AuthenticationStatus string
	Message              string
	Step                 string
	Target               string
	Connection           ConnectionObservation
}

const (
	AuthenticationComplete     = "complete"
	AuthenticationNotConfirmed = "not confirmed"
	AuthenticationNotRequired  = "not required"
)

// OperationProgress contains only information reported by a running operation.
// Output is already passed through the operation's redaction path.
type OperationProgress struct {
	Step   string
	Output string
}
