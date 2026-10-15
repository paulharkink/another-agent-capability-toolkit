package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/agents"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/mcp"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/render"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
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
		return viewmodel.OperationResult{}, invalid(errors.New("instance reference (Capability Pack, capability, and instance) is required"))
	}
	return s.ApplyInstance(ctx, InstanceRequest{Ref: q.Ref, Inputs: q.Inputs, ActiveInputGroups: q.ActiveInputGroups, ResetInputs: q.ResetInputs, ItemIDs: q.ItemIDs, DestinationIDs: q.DestinationIDs, ExternalURLs: q.ExternalURLs})
}

// UIInstallSkillOnly installs a zero-input skill capability without creating
// an instance record. The request is deliberately separate from normal instance
// apply so regular setup can never opt out of its instance ledger.
func (s *Service) UIInstallSkillOnly(ctx context.Context, q viewmodel.SkillOnlyInstallRequest) (viewmodel.OperationResult, error) {
	if q.SourceID == "" || q.PackageID == "" {
		return viewmodel.OperationResult{}, invalid(errors.New("Capability Pack and capability are required"))
	}
	if q.SourceID != s.Source.ID {
		return viewmodel.OperationResult{}, invalid(fmt.Errorf("Capability Pack %q is not selected", q.SourceID))
	}
	p, err := s.packageByID(q.PackageID)
	if err != nil {
		return viewmodel.OperationResult{}, err
	}
	if !p.HasSkill() || p.HasMCP() || len(p.Inputs) != 0 || len(p.Plugins) != 0 {
		return viewmodel.OperationResult{}, invalid(errors.New("direct skill installation requires a skill-only capability with no inputs"))
	}
	items, selected, err := componentSelection(p, nil, nil, true)
	if err != nil {
		return viewmodel.OperationResult{}, invalid(err)
	}
	if len(q.DestinationIDs) == 0 {
		return viewmodel.OperationResult{}, invalid(errors.New("select an agent destination"))
	}
	key := state.Key{Source: s.Source.ID, Package: p.ID, Target: "default"}
	var out viewmodel.OperationResult
	err = s.Store.WithLock(ctx, func() error {
		out = viewmodel.OperationResult{SavedApplicable: true, Changes: []state.Installation{}, Errors: []string{}, Step: "validate", Target: s.Source.ID + "/" + p.ID}
		registry := s.adapterRegistry()
		adapters := make(map[string]agents.Adapter, len(q.DestinationIDs))
		for _, id := range q.DestinationIDs {
			adapter, e := registry.Adapter(id)
			if e != nil {
				return invalid(e)
			}
			if !adapter.Features().Skills {
				return invalid(fmt.Errorf("agent %s cannot install skills", id))
			}
			if _, ok := adapter.(agents.SkillManager); !ok {
				return invalid(fmt.Errorf("agent %s does not support skill installation", id))
			}
			detection, e := adapter.Detect(ctx, s.agentScope(id))
			if e != nil {
				return e
			}
			if !detection.Installed {
				return invalid(fmt.Errorf("agent %s is not detected: %s", id, detection.Reason))
			}
			adapters[id] = adapter
		}
		stages := make(map[string]string, len(selected))
		out.Step = "stage"
		for _, skill := range p.SkillDefinitions() {
			stage, e := (render.Renderer{Generator: &render.Generator{Executor: s.Options.Runner, OnStderr: s.Options.OnStderr}}).StageSkill(ctx, p, skill, nil, config.Profile{Ref: config.ProfileRef{Name: "default"}}, s.Store.GeneratedDir(key))
			if e != nil {
				return e
			}
			published := filepath.Join(filepath.Dir(stage), "output-"+strings.TrimPrefix(filepath.Base(stage), ".aact-stage-"))
			if e = render.Publish(stage, published); e != nil {
				_ = os.RemoveAll(stage)
				return e
			}
			stages[skill.Name] = published
		}
		out.Step = "skill"
		for _, id := range q.DestinationIDs {
			adapter := adapters[id]
			for _, item := range items {
				for _, name := range item.Skills {
					var definition *catalog.Skill
					for i := range p.SkillDefinitions() {
						skill := p.SkillDefinitions()[i]
						if skill.Name == name {
							definition = &skill
							break
						}
					}
					if definition == nil {
						return fmt.Errorf("selected skill %q is missing from capability", name)
					}
					row, e := adapter.(agents.SkillManager).InstallSkill(ctx, s.agentScope(id), agents.SkillRequest{Key: key, Package: p, Skill: *definition, StagedDir: stages[name]})
					if e != nil {
						out.Errors = append(out.Errors, fmt.Sprintf("%s: %v", id, e))
						continue
					}
					if e = s.Store.Record(row); e != nil {
						out.Errors = append(out.Errors, fmt.Sprintf("%s: record installation: %v", id, e))
						continue
					}
					out.Changes = append(out.Changes, row)
				}
			}
		}
		out.Message = "Skill installation completed"
		return nil
	})
	if err != nil {
		out.Errors = append(out.Errors, err.Error())
	}
	return out, err
}
