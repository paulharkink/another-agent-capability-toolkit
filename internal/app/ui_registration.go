package app

import (
	"context"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/agents"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

func (s *Service) UIConfigureRegistrations(ctx context.Context, q viewmodel.RegistrationRequest) (viewmodel.OperationResult, error) {
	connection := s.CheckConnection(ctx, q.URL, q.Transport)
	targets := make([]agents.Environment, 0, len(q.AgentIDs))
	for _, id := range q.AgentIDs {
		env, err := s.uiEnvironment(id, q.Key)
		if err != nil {
			return viewmodel.OperationResult{Connection: connection}, err
		}
		targets = append(targets, env)
	}
	result, err := s.ConfigureRegistrations(ctx, RegistrationRequest{
		Key: q.Key, URL: q.URL, Transport: q.Transport, Agents: targets,
	})
	return viewmodel.OperationResult{Changes: result.Changes, Errors: result.Errors, Message: result.Message, Connection: connection}, err
}
