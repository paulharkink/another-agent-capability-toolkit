package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/agents"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
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

func reportOperationOutput(ctx context.Context, output string) {
	if progress, _ := ctx.Value(operationProgressContextKey{}).(*operationProgressScope); progress != nil && output != "" {
		progress.output([]byte(output + "\n"))
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
		return viewmodel.SetupPreview{}, invalid(errors.New("instance reference (Capability Pack, capability, and instance) is required"))
	}
	return s.PreviewInstance(ctx, InstanceRequest{Ref: q.Ref})
}

// UISaveInputs persists a capability instance's input answers without
// installing agent links, changing MCP registrations, or starting runtimes.
func (s *Service) UISaveInputs(ctx context.Context, q viewmodel.SetupInstallRequest) error {
	if q.Ref.CapabilityID == "" || q.Ref.Name == "" {
		return invalid(errors.New("instance reference (Capability Pack, capability, and instance) is required"))
	}
	return s.Store.WithLock(ctx, func() error {
		p, instance, key, err := s.loadInstance(q.Ref)
		if err != nil {
			return err
		}
		p, instance, values, _, err := s.instanceValuesForPreview(p, instance, key, InstanceRequest{
			Ref: q.Ref, Inputs: q.Inputs, ActiveInputGroups: q.ActiveInputGroups, ResetInputs: q.ResetInputs,
		})
		if err != nil {
			return err
		}
		fixed, err := fixedTargetInputs(p.Inputs, config.Target{Path: instance.Path, InputPolicy: instance.InputPolicy}, instance.Raw)
		if err != nil {
			return invalid(err)
		}
		previous, err := s.Store.ActiveInputGroups(key)
		if err != nil {
			return err
		}
		groups := effectiveActiveInputGroups(p.Inputs, values, fixed, q.ActiveInputGroups, previous)
		if err := validateActiveInputGroups(p.Inputs, groups); err != nil {
			return invalid(err)
		}
		reset := make(map[string]bool, len(q.ResetInputs))
		for _, name := range q.ResetInputs {
			reset[name] = true
		}
		safe := make(map[string]any, len(values))
		for _, input := range p.Inputs {
			if input.Type == "secret" || reset[input.Name] {
				continue
			}
			if value, ok := values[input.Name]; ok {
				safe[input.Name] = value
			}
		}
		return s.Store.SaveAnswersWithActiveInputGroups(key, safe, groups)
	})
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
// UIInstall preserves the pre-workspace combined operation for API callers
// that rely on its save/apply behavior. The staged TUI calls UIInstallAgents.
func (s *Service) UIInstall(ctx context.Context, q viewmodel.SetupInstallRequest) (viewmodel.OperationResult, error) {
	if q.Ref.CapabilityID == "" || q.Ref.Name == "" {
		return viewmodel.OperationResult{}, invalid(errors.New("instance reference (Capability Pack, capability, and instance) is required"))
	}
	if q.ExternalURL != "" && len(q.ExternalURLs) == 0 {
		p, _, _, err := s.loadInstance(q.Ref)
		if err != nil {
			return viewmodel.OperationResult{}, err
		}
		definitions := p.MCPDefinitions()
		if len(definitions) != 1 {
			return viewmodel.OperationResult{}, invalid(errors.New("a single external endpoint requires exactly one MCP definition"))
		}
		q.ExternalURLs = map[string]string{definitions[0].Name: q.ExternalURL}
	}
	return s.ApplyInstance(ctx, InstanceRequest{
		Ref: q.Ref, Inputs: q.Inputs, ActiveInputGroups: q.ActiveInputGroups,
		ResetInputs: q.ResetInputs, ItemIDs: q.ItemIDs, DestinationIDs: q.DestinationIDs,
		Uninstall: q.Uninstall, ExternalURLs: q.ExternalURLs,
	})
}

// UIInstallAgents installs agent bindings without saving inputs or starting
// MCP runtimes. Inputs must be saved first; secret values currently entered
// on the Inputs screen are supplied transiently for registration generation.
func (s *Service) UIInstallAgents(ctx context.Context, q viewmodel.SetupInstallRequest) (viewmodel.OperationResult, error) {
	if q.Ref.CapabilityID == "" || q.Ref.Name == "" {
		return viewmodel.OperationResult{}, invalid(errors.New("instance reference (Capability Pack, capability, and instance) is required"))
	}
	if q.Ref.PackID != s.Source.ID {
		return viewmodel.OperationResult{}, invalid(fmt.Errorf("Capability Pack %q is not selected", q.Ref.PackID))
	}
	p, _, key, err := s.loadInstance(q.Ref)
	if err != nil {
		return viewmodel.OperationResult{}, err
	}
	groups, err := s.Store.ActiveInputGroups(key)
	if err != nil {
		return viewmodel.OperationResult{}, err
	}
	externalURLs := q.ExternalURLs
	if q.ExternalURL != "" && len(externalURLs) == 0 {
		definitions := p.MCPDefinitions()
		if len(definitions) != 1 {
			return viewmodel.OperationResult{}, invalid(errors.New("a single external endpoint requires exactly one MCP definition"))
		}
		externalURLs = map[string]string{definitions[0].Name: q.ExternalURL}
	}
	// Secret values are never stored in ordinary answer files. Keep a secret
	// entered in the open Inputs form available to configure the agent's MCP
	// registration, while all other values come from the last explicit Save.
	secrets := map[string]any{}
	for _, input := range p.Inputs {
		if input.Type == "secret" {
			if value, ok := q.Inputs[input.Name]; ok {
				secrets[input.Name] = value
			}
		}
	}
	return s.ApplyInstance(ctx, InstanceRequest{
		Ref: q.Ref, Inputs: secrets, ActiveInputGroups: groups,
		ItemIDs: q.ItemIDs, DestinationIDs: q.DestinationIDs,
		Uninstall: q.Uninstall, ExternalURLs: externalURLs, InstallOnly: true,
	})
}

// UIUninstallSkillOnly is retained for source compatibility. It translates
// the old request into the ordinary Pack-owned default Instance lifecycle.
func (s *Service) UIUninstallSkillOnly(ctx context.Context, q viewmodel.SkillOnlyUninstallRequest) (viewmodel.OperationResult, error) {
	ref, err := defaultSkillInstanceRef(s, q.SourceID, q.PackageID)
	if err != nil {
		return viewmodel.OperationResult{}, err
	}
	key, err := s.Store.ResolveInstanceKey(ref.PackID, ref.CapabilityID, ref.Name)
	if err != nil {
		return viewmodel.OperationResult{}, err
	}
	rows, err := s.Store.Installations()
	if err != nil {
		return viewmodel.OperationResult{}, err
	}
	installed := map[string]bool{}
	for _, row := range rows {
		if row.Key.Source == key.Source && row.Key.Package == key.Package && row.Key.Target == key.Target && row.Component == "skill" {
			installed[row.AgentID] = true
		}
	}
	remove := map[string]bool{}
	for _, id := range q.DestinationIDs {
		remove[id] = true
	}
	remaining := make([]string, 0, len(installed))
	for id := range installed {
		if !remove[id] {
			remaining = append(remaining, id)
		}
	}
	return s.UIInstallAgents(ctx, viewmodel.SetupInstallRequest{SetupRequest: viewmodel.SetupRequest{Ref: ref}, DestinationIDs: remaining, Uninstall: true})
}

// UIInstallSkillOnly is retained for source compatibility, but uses the same
// default Capability Instance and ordinary install path as every other skill.
func (s *Service) UIInstallSkillOnly(ctx context.Context, q viewmodel.SkillOnlyInstallRequest) (viewmodel.OperationResult, error) {
	ref, err := defaultSkillInstanceRef(s, q.SourceID, q.PackageID)
	if err != nil {
		return viewmodel.OperationResult{}, err
	}
	return s.UIInstall(ctx, viewmodel.SetupInstallRequest{SetupRequest: viewmodel.SetupRequest{Ref: ref}, DestinationIDs: q.DestinationIDs})
}

func defaultSkillInstanceRef(s *Service, packID, capabilityID string) (config.InstanceRef, error) {
	if packID == "" || capabilityID == "" {
		return config.InstanceRef{}, invalid(errors.New("Capability Pack and capability are required"))
	}
	if err := s.requireActivePack(packID); err != nil {
		return config.InstanceRef{}, invalid(err)
	}
	p, err := s.packageByID(capabilityID)
	if err != nil {
		return config.InstanceRef{}, err
	}
	if !p.HasSkill() || p.HasMCP() || len(p.Inputs) != 0 || len(p.Plugins) != 0 {
		return config.InstanceRef{}, invalid(errors.New("default instance compatibility applies only to a zero-input skill capability"))
	}
	return config.InstanceRef{PackID: packID, CapabilityID: capabilityID, Name: "default"}, nil
}
