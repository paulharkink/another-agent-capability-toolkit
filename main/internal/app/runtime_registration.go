package app

import (
	"context"
	"fmt"

	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/agents"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/state"
)

// refreshRuntimeRegistrations updates only agent bindings that AACT already
// manages for this exact MCP child. Starting a container can change its
// dynamically assigned host port, so persisted agent URLs must follow it.
func (s *Service) refreshRuntimeRegistrations(ctx context.Context, key state.Key, definition catalog.MCP, values map[string]any, endpoint string) error {
	rows, err := s.Store.Installations()
	if err != nil {
		return err
	}
	name, err := declaredRegistrationName(definition, key, values)
	if err != nil {
		return err
	}
	headers, err := profileRegistrationHeaders(definition, values)
	if err != nil {
		return err
	}
	registration := agents.Registration{Name: name, URL: endpoint, Transport: definition.Transport, TimeoutMS: registrationTimeoutMS(&definition), Headers: headers}
	refreshedAgents := map[string]bool{}
	for _, old := range rows {
		if old.Key != key || old.Component != "mcp" || old.ExternalRegistration || refreshedAgents[old.AgentID] {
			continue
		}
		refreshedAgents[old.AgentID] = true
		adapter, adapterErr := s.adapterRegistry().Adapter(old.AgentID)
		if adapterErr != nil {
			return fmt.Errorf("refresh MCP registration for agent %s: %w", old.AgentID, adapterErr)
		}
		manager, ok := adapter.(agents.MCPManager)
		if !ok {
			return fmt.Errorf("agent %s cannot manage MCP registrations", old.AgentID)
		}
		scope := s.agentScope(old.AgentID)
		if old.RegistrationName != name {
			if err := manager.Unregister(ctx, scope, old); err != nil {
				return fmt.Errorf("remove prior MCP registration %q for agent %s: %w", old.RegistrationName, old.AgentID, err)
			}
			if err := s.removeRegistration(old); err != nil {
				return fmt.Errorf("forget prior MCP registration %q for agent %s: %w", old.RegistrationName, old.AgentID, err)
			}
		}
		result, registerErr := manager.Register(ctx, scope, agents.MCPRequest{Key: key, Registration: registration})
		for _, removed := range result.Removed {
			if err := s.removeRegistration(removed); err != nil {
				return fmt.Errorf("forget removed MCP registration for agent %s: %w", old.AgentID, err)
			}
		}
		for _, effect := range result.Effects {
			if err := s.recordRegistration(effect); err != nil {
				return fmt.Errorf("record MCP registration effect for agent %s: %w", old.AgentID, err)
			}
		}
		if registerErr != nil {
			return fmt.Errorf("refresh MCP registration for agent %s: %w", old.AgentID, registerErr)
		}
		if err := s.recordRegistration(result.Installation); err != nil {
			return fmt.Errorf("record refreshed MCP registration for agent %s: %w", old.AgentID, err)
		}
	}
	return nil
}
