package app

import (
	"context"
	"errors"
	"fmt"
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
	if len(q.AgentIDs) > 0 && len(q.RemoveAgentIDs) > 0 {
		return viewmodel.OperationResult{}, errors.New("choose Save desired agent registrations or Remove selected registrations, not both")
	}
	if len(q.RemoveAgentIDs) > 0 {
		result, err := s.removeUIRegistrations(ctx, q.Key, q.RemoveAgentIDs)
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
	for _, id := range q.AgentIDs {
		kind, _, _ := strings.Cut(id, ":")
		if strings.EqualFold(id, "all") || strings.EqualFold(kind, "generic") || strings.EqualFold(kind, "generic-mcp") {
			return viewmodel.OperationResult{}, fmt.Errorf("MCP registrations require a named agent destination; %q is not supported", id)
		}
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
		_, adapterErr := agents.For(kind, s.Options.Runner)
		if row.AgentID == "all" || agents.IsManual(kind) || adapterErr != nil {
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
