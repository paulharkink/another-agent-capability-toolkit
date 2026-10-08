package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/agents"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/picker"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

func (s *Service) UIConfigureRegistrations(ctx context.Context, q viewmodel.RegistrationRequest) (viewmodel.OperationResult, error) {
	if errors.Is(ctx.Err(), context.Canceled) {
		return viewmodel.OperationResult{}, picker.ErrCancelled
	}
	hasRemoval := len(q.RemoveAgentIDs) > 0 || len(q.RemoveRegistrations) > 0
	if len(q.AgentIDs) > 0 && hasRemoval {
		return viewmodel.OperationResult{}, errors.New("choose Save desired agent registrations or Remove selected registrations, not both")
	}
	if len(q.RemoveAgentIDs) > 0 && len(q.RemoveRegistrations) > 0 {
		return viewmodel.OperationResult{}, errors.New("mix exact registration identities or legacy agent IDs in a removal request, not both")
	}
	for _, id := range q.AgentIDs {
		kind, _, _ := strings.Cut(id, ":")
		if strings.EqualFold(id, "all") || strings.EqualFold(kind, "generic") || strings.EqualFold(kind, "generic-mcp") {
			return viewmodel.OperationResult{}, fmt.Errorf("MCP registrations require a named agent destination; %q is not supported", id)
		}
	}
	source, sourceErr := s.forSource(q.Key.Source)
	if sourceErr != nil {
		result := viewmodel.OperationResult{}
		if q.URL != "" {
			result.Connection = s.CheckConnection(ctx, q.URL, q.Transport)
		}
		return result, fmt.Errorf("cannot configure this capability without its Capability Pack catalog; locate the pack directory first: %w", sourceErr)
	}
	p, packageErr := source.packageByID(q.Key.Package)
	if packageErr != nil {
		result := viewmodel.OperationResult{}
		if q.URL != "" {
			result.Connection = s.CheckConnection(ctx, q.URL, q.Transport)
		}
		return result, fmt.Errorf("cannot configure this capability without its Capability Pack catalog; locate the pack directory first: %w", packageErr)
	}
	if p.HasMCP() {
		if p.Skill == nil {
			return viewmodel.OperationResult{}, errors.New("this MCP has no companion skill in its Capability Pack catalog; locate the pack directory")
		}
		if len(p.MCPDefinitions()) != 1 {
			return viewmodel.OperationResult{}, errors.New("multi-MCP capabilities must be configured through the capability setup form so every endpoint is supplied")
		}
		if hasRemoval {
			rows, err := source.Store.Installations()
			if err != nil {
				return viewmodel.OperationResult{}, err
			}
			removed := map[string]bool{}
			for _, id := range q.RemoveAgentIDs {
				removed[id] = true
			}
			for _, identity := range q.RemoveRegistrations {
				removed[identity.AgentID] = true
			}
			envs := map[string]agents.Environment{}
			for _, row := range rows {
				if !sameCapabilityKey(row.Key, q.Key) || (row.Component != "skill" && row.Component != "mcp") || row.AgentID == "" || !removed[row.AgentID] {
					continue
				}
				kind := row.AgentKind
				if kind == "" {
					kind, _, _ = strings.Cut(row.AgentID, ":")
				}
				env := envs[row.AgentID]
				env.ID, env.Kind, env.Home = row.AgentID, kind, row.AgentHome
				if row.Component == "skill" {
					env.SkillsDir = filepath.Dir(row.Destination)
				}
				if row.Component == "mcp" {
					env.ConfigPath = row.Destination
				}
				envs[row.AgentID] = env
			}
			result, err := source.removeCapabilityBindings(ctx, p, q.Key, envs)
			return result, normalizeRegistrationCancellation(ctx, err)
		}
		if q.URL == "" && len(q.AgentIDs) > 0 {
			return viewmodel.OperationResult{}, errors.New("an external MCP endpoint URL is required to configure this capability")
		}
		externalURLs := map[string]string(nil)
		externalURL := q.URL
		if len(p.MCPs) > 0 && q.URL != "" {
			externalURLs = map[string]string{p.MCPs[0].Name: q.URL}
			externalURL = ""
		}
		request := viewmodel.SetupInstallRequest{
			SetupRequest:   viewmodel.SetupRequest{PackageID: p.ID, Environment: q.Key.Environment, Target: q.Key.Target},
			DestinationIDs: q.AgentIDs, ExternalURL: externalURL, ExternalURLs: externalURLs,
		}
		result, err := source.UIInstall(ctx, request)
		return result, normalizeRegistrationCancellation(ctx, err)
	}
	if hasRemoval {
		identities := make([]registrationIdentity, 0, len(q.RemoveRegistrations))
		for _, identity := range q.RemoveRegistrations {
			identities = append(identities, registrationIdentity{AgentID: identity.AgentID, Destination: identity.Destination})
		}
		result, err := s.removeUIRegistrations(ctx, q.Key, identities, q.RemoveAgentIDs)
		step, target := result.Step, result.Target
		if step == "" {
			step = "remove agent registration"
		}
		if target == "" {
			target = registrationOperationTarget(q.Key)
		}
		return viewmodel.OperationResult{
			Changes: result.Changes, Errors: result.Errors, Saved: result.Saved, Message: result.Message,
			Step: step, Target: target,
		}, normalizeRegistrationCancellation(ctx, err)
	}
	if q.URL == "" {
		return viewmodel.OperationResult{}, errors.New("an explicit MCP endpoint URL is required to register named agents")
	}
	preserved, err := s.preservedUIRegistrations(q.Key, q.AgentIDs)
	if err != nil {
		return viewmodel.OperationResult{}, err
	}
	connection := s.CheckConnection(ctx, q.URL, q.Transport)
	if errors.Is(ctx.Err(), context.Canceled) {
		return viewmodel.OperationResult{Connection: connection}, picker.ErrCancelled
	}
	targets := make([]agents.Environment, 0, len(q.AgentIDs))
	for _, id := range q.AgentIDs {
		env, err := s.uiEnvironment(id, q.Key)
		if err != nil {
			return viewmodel.OperationResult{Connection: connection}, err
		}
		targets = append(targets, env)
	}
	result, err := s.ConfigureRegistrations(ctx, RegistrationRequest{
		Key: q.Key, URL: q.URL, Transport: q.Transport, Agents: targets, preserve: preserved,
	})
	step, target := result.Step, result.Target
	if step == "" && len(result.Errors) > 0 {
		step = "update agent registrations"
		if agentID, _, ok := strings.Cut(result.Errors[0], ":"); ok {
			step = "remove agent registration"
			for _, requestedID := range q.AgentIDs {
				if requestedID == agentID {
					step = "register agent"
					break
				}
			}
		}
	}
	if target == "" {
		target = registrationOperationTarget(q.Key)
	}
	if step == "" && err != nil {
		step = "update agent registrations"
	}
	return viewmodel.OperationResult{
		Changes: result.Changes, Errors: result.Errors, Message: result.Message,
		Step: step, Target: target, Connection: connection,
	}, normalizeRegistrationCancellation(ctx, err)
}

func registrationOperationTarget(key state.Key) string {
	if key.Target != "" {
		return key.Target
	}
	return key.Package
}

func (s *Service) preservedUIRegistrations(key state.Key, requestedIDs []string) ([]agents.Environment, error) {
	rows, err := s.Store.Installations()
	if err != nil {
		return nil, err
	}
	requested := make(map[string]bool, len(requestedIDs))
	for _, id := range requestedIDs {
		requested[id] = true
	}
	preserved := []agents.Environment{}
	for _, row := range rows {
		if row.Key != key || row.Component != "mcp" || requested[row.AgentID] {
			continue
		}
		kind := row.AgentKind
		if kind == "" {
			kind, _, _ = strings.Cut(row.AgentID, ":")
		}
		adapter, adapterErr := s.adapterFor(row.AgentID, kind)
		if adapterErr != nil || !adapter.Features().MCPs {
			preserved = append(preserved, agents.Environment{ID: row.AgentID, Kind: kind, Home: row.AgentHome, ConfigPath: row.Destination})
		}
	}
	return preserved, nil
}

func normalizeRegistrationCancellation(ctx context.Context, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		return picker.ErrCancelled
	}
	return err
}
