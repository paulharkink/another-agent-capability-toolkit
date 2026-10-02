package app

import (
	"context"
	"time"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/mcp"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

// CheckConnection observes an endpoint from this process's network namespace.
// Registration remains a separate action, so a failed check is visible without
// silently deciding whether it must block registration.
func (*Service) CheckConnection(ctx context.Context, url, transport string) viewmodel.ConnectionObservation {
	err := mcp.Health(ctx, url, transport)
	observation := viewmodel.ConnectionObservation{URL: url, CheckedAt: time.Now().UTC(), Reachable: err == nil}
	if err != nil {
		observation.Error = err.Error()
	}
	return observation
}
