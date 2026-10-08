package app

import (
	"context"
	"errors"
	"os"
	"sync"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/agents"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/mcp"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

type operationProgressContextKey struct{}
type operationProgressScopeContextKey struct{}

type operationProgressScope struct {
	mu       sync.Mutex
	step     string
	observer func(viewmodel.OperationProgress)
}

func (p *operationProgressScope) setStep(step string) {
	if p == nil || step == "" || p.observer == nil {
		return
	}
	p.mu.Lock()
	p.step = step
	p.mu.Unlock()
	p.observer(viewmodel.OperationProgress{Step: step})
}

func (p *operationProgressScope) output(output []byte) {
	if p == nil || len(output) == 0 || p.observer == nil {
		return
	}
	p.mu.Lock()
	step := p.step
	p.mu.Unlock()
	p.observer(viewmodel.OperationProgress{Step: step, Output: string(output)})
}

func reportOperationStep(ctx context.Context, step string) {
	if progress, _ := ctx.Value(operationProgressContextKey{}).(*operationProgressScope); progress != nil {
		progress.setStep(step)
	}
}

func (s *Service) withOperationProgress(ctx context.Context, observer func(viewmodel.OperationProgress)) (*Service, context.Context) {
	progress := &operationProgressScope{observer: observer}
	options := s.Options
	previousOnStderr := options.OnStderr
	options.OnStderr = func(output []byte) {
		if previousOnStderr != nil {
			previousOnStderr(output)
		}
		progress.output(output)
	}
	if runtime, ok := options.Runtime.(*mcp.Runtime); ok {
		runtimeCopy := *runtime
		previousRuntimeOnStderr := runtimeCopy.OnStderr
		runtimeCopy.OnStderr = func(output []byte) {
			if previousRuntimeOnStderr != nil {
				previousRuntimeOnStderr(output)
			}
			progress.output(output)
		}
		options.Runtime = &runtimeCopy
	}
	scoped := &Service{Source: s.Source, Store: s.Store, Options: options}
	ctx = context.WithValue(ctx, operationProgressContextKey{}, progress)
	ctx = context.WithValue(ctx, operationProgressScopeContextKey{}, true)
	return scoped, ctx
}

// UISetupPreview resolves a form without validating missing required answers or
// changing installed state. Values retain the source of their winning layer.
func (s *Service) UISetupPreview(ctx context.Context, q viewmodel.SetupRequest) (viewmodel.SetupPreview, error) {
	if q.Ref.CapabilityID == "" || q.Ref.Name == "" {
		return viewmodel.SetupPreview{}, invalid(errors.New("profile reference (Capability Pack, capability, and profile) is required"))
	}
	return s.PreviewProfile(ctx, ProfileRequest{Ref: q.Ref})
}

func currentUserHome() string {
	home, _ := os.UserHomeDir()
	return home
}

func uiDiscoveryProbe() agents.DiscoveryProbe {
	probe, err := agents.DefaultDiscoveryProbe()
	if err != nil {
		return agents.DiscoveryProbe{}
	}
	return probe
}

func (s *Service) discoveryProbe() agents.DiscoveryProbe {
	if s.Options.DiscoveryProbe != nil {
		return *s.Options.DiscoveryProbe
	}
	return uiDiscoveryProbe()
}

// targetInputChoices turns wildcard table keys in a target TOML into stable
// checkbox values. For example, dbms.*.tenants.* yields dbms/tenant IDs.
func (s *Service) UIInstall(ctx context.Context, q viewmodel.SetupInstallRequest) (viewmodel.OperationResult, error) {
	if q.Ref.CapabilityID == "" || q.Ref.Name == "" {
		return viewmodel.OperationResult{}, invalid(errors.New("profile reference (Capability Pack, capability, and profile) is required"))
	}
	return s.ApplyProfile(ctx, ProfileRequest{Ref: q.Ref, Inputs: q.Inputs, ActiveInputGroups: q.ActiveInputGroups, ResetInputs: q.ResetInputs, ItemIDs: q.ItemIDs, DestinationIDs: q.DestinationIDs, ExternalURLs: q.ExternalURLs})
}
