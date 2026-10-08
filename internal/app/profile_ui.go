package app

import (
	"context"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/mcp"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
	"io"
)

type observedRuntime struct {
	MCPRuntime
	instances []mcp.Instance
	err       error
}

func (r observedRuntime) List(context.Context) ([]mcp.Instance, error) { return r.instances, r.err }

// UI snapshots share one Docker observation; selecting profiles never starts a process.
func (s *Service) UICapabilityProfiles(ctx context.Context) (map[string]viewmodel.CapabilityProfileSnapshot, error) {
	result := map[string]viewmodel.CapabilityProfileSnapshot{}
	instances := []mcp.Instance{}
	var runtimeErr error
	needsRuntime := false
	for _, p := range s.Source.Catalog {
		needsRuntime = needsRuntime || p.HasMCP()
	}
	if needsRuntime {
		instances, runtimeErr = s.Options.Runtime.List(ctx)
	}
	options := s.Options
	options.Runtime = observedRuntime{MCPRuntime: s.Options.Runtime, instances: instances, err: runtimeErr}
	scoped := New(s.Source, s.Store, options)
	s.observationMu.Lock()
	scoped.lastInstances = append([]mcp.Instance(nil), s.lastInstances...)
	scoped.lastObservedAt = s.lastObservedAt
	s.observationMu.Unlock()
	for _, p := range s.Source.Catalog {
		snapshot, err := scoped.ProfileSnapshot(ctx, p.ID)
		if err != nil {
			snapshot.Errors = append(snapshot.Errors, err.Error())
		}
		result[p.ID] = snapshot
	}
	s.observationMu.Lock()
	s.lastInstances = scoped.lastInstances
	s.lastObservedAt = scoped.lastObservedAt
	s.observationMu.Unlock()
	return result, nil
}
func (s *Service) UICreateProfile(ctx context.Context, ref config.ProfileRef) error {
	return s.CreateProfile(ctx, ref)
}
func (s *Service) UIConfigurationProfileRun(ctx context.Context, action string, ref config.ProfileRef, mcpID string) (string, error) {
	result, err := s.RunProfileMCP(ctx, action, ProfileRequest{Ref: ref}, mcpID)
	if result.Logs != "" {
		return result.Logs, err
	}
	if len(result.Instances) > 0 {
		return result.Instances[0].URL, err
	}
	return result.Message, err
}

// Compile-time guard for the injected runtime facade's lifecycle methods.
var _ interface {
	Logs(context.Context, state.Key) (io.ReadCloser, error)
} = observedRuntime{}
