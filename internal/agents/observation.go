package agents

import (
	"context"
	"time"
)

func (a *registeredAdapter) Observe(ctx context.Context, scope Scope, request ObservationRequest) (Observation, error) {
	d, err := a.Detect(ctx, scope)
	return Observation{Detection: d, ConfigFiles: d.ConfigFiles, ObservedAt: time.Now().UTC()}, err
}
