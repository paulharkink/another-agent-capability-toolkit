package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/mcp"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

// observedForeignInstall selects the active Pack declaration's exact children
// from a fresh Docker observation. It deliberately does not consult local
// instance files, answer files, or private authentication state.
func (s *Service) observedForeignInstall(ctx context.Context, p catalog.Package, target, environment string) (state.Key, map[string]mcp.Instance, bool, error) {
	parent, children, found, err := s.observedForeignParent(ctx, s.Source.ID, p.ID, target, environment)
	if err != nil || !found {
		return parent, nil, found, err
	}
	runtimes := make(map[string]mcp.Instance, len(p.MCPDefinitions()))
	for _, definition := range p.MCPDefinitions() {
		childKey := mcpDefinitionKey(parent, p, definition)
		var match *mcp.Instance
		for i := range children {
			if children[i].Key != childKey {
				continue
			}
			if match != nil {
				return state.Key{}, nil, false, fmt.Errorf("multiple running foreign runtimes match MCP %q; refresh the Capability Instance and resolve the duplicate", definition.Name)
			}
			match = &children[i]
		}
		if match == nil {
			continue
		}
		if strings.TrimSpace(match.RegistrationName) == "" {
			return state.Key{}, nil, false, fmt.Errorf("Docker did not report a registration_name for MCP %q; refresh the foreign Capability Instance before installing agent bindings", definition.Name)
		}
		if err := validateEndpointURL(match.URL); err != nil {
			return state.Key{}, nil, false, fmt.Errorf("Docker reported an invalid endpoint for MCP %q: %w", definition.Name, err)
		}
		runtimes[definition.Name] = *match
	}
	if len(runtimes) == 0 {
		return state.Key{}, nil, false, fmt.Errorf("running foreign Capability Instance has no running MCP declared by the selected Pack; refresh before installing agent bindings")
	}
	return parent, runtimes, true, nil
}

// observedForeignParent resolves a running foreign parent solely from current
// Docker observations. It never consults another installation's Store files.
func (s *Service) observedForeignParent(ctx context.Context, sourceID, packageID, target, environment string) (state.Key, []mcp.Instance, bool, error) {
	if sourceID == "" || packageID == "" || target == "" {
		return state.Key{}, nil, false, nil
	}
	observed, err := s.Options.Runtime.List(ctx)
	if err != nil {
		return state.Key{}, nil, false, err
	}
	parents := map[state.Key][]mcp.Instance{}
	for _, runtime := range observed {
		if runtime.Status != "running" || runtime.Ownership != "other-aact" || runtime.Key.Source != sourceID || runtime.Key.Package != packageID || runtime.Key.Target != target {
			continue
		}
		if environment != "" && runtime.Key.Environment != environment {
			continue
		}
		parent := runtime.Key
		parent.MCP = ""
		parent.MCPDefinition = ""
		parents[parent] = append(parents[parent], runtime)
	}
	if len(parents) == 0 {
		return state.Key{}, nil, false, nil
	}
	if len(parents) > 1 {
		return state.Key{}, nil, false, errors.New("multiple running instances with this name are managed by another AACT installation; select the exact observed instance")
	}
	for key, runtimes := range parents {
		return key, runtimes, true, nil
	}
	return state.Key{}, nil, false, fmt.Errorf("cannot resolve observed instance %s/%s/%s", sourceID, packageID, target)
}

func foreignRuntimeChildName(runtime mcp.Instance) string {
	if runtime.Key.MCPDefinition != "" {
		return runtime.Key.MCPDefinition
	}
	if runtime.Key.MCP != "" {
		return runtime.Key.MCP
	}
	return strings.TrimSpace(runtime.RegistrationName)
}

func (s *Service) observedForeignSetupPreview(ctx context.Context, p catalog.Package, parent state.Key) (viewmodel.SetupPreview, error) {
	items, err := catalog.InstallationItems(p, p.Sets)
	if err != nil {
		return viewmodel.SetupPreview{}, err
	}
	preview := viewmodel.SetupPreview{
		Key: parent, PackageName: p.Name, PackRoot: s.Source.Root,
		InstanceOrigin: "observed", ProfileOrigin: "observed",
		MCP: p.HasMCP(), MCPDefinitions: p.MCPDefinitions(), PluginDefinitions: p.PluginDefinitions(),
		MCPDefinitionKeys: map[string]state.Key{}, Items: items,
	}
	for _, item := range items {
		preview.SelectedItemIDs = append(preview.SelectedItemIDs, item.ID)
	}
	for _, definition := range p.MCPDefinitions() {
		preview.MCPDefinitionKeys[definition.Name] = mcpDefinitionKey(parent, p, definition)
	}
	rows, err := s.Store.Installations()
	if err != nil {
		return preview, err
	}
	registry := s.adapterRegistry()
	for _, adapter := range registry.Adapters() {
		features := adapter.Features()
		destination := viewmodel.SetupDestination{ID: adapter.ID(), Kind: adapter.ID(), Name: adapter.Name(), Features: features}
		if adapter.ID() == "generic" && p.HasMCP() {
			continue
		}
		scope := s.agentScope(adapter.ID())
		scope.ID = adapter.ID()
		detection, detectErr := adapter.Detect(ctx, scope)
		if detectErr != nil {
			destination.Detection = "unavailable"
			destination.DisabledReason = detectErr.Error()
		} else {
			destination.Home = detection.Home
			destination.SkillsPath = detection.SkillsPath
			destination.ConfigPath = detection.ConfigPath
			destination.Detection = detection.State
			destination.Note = detection.Reason
			destination.Detected = detection.Installed
			destination.MCPRegistrationAvailable = detection.Installed && features.MCPs && detection.MCPDisabledReason == "" && (detection.CanCreateConfig || detection.ConfigPath != "")
			if !detection.Installed {
				destination.DisabledReason = "Agent is not detected"
			} else if p.HasMCP() && !features.MCPs {
				destination.DisabledReason = "This agent does not support MCP registration"
			} else if p.HasMCP() && detection.MCPDisabledReason != "" {
				destination.DisabledReason = detection.MCPDisabledReason
			}
		}
		for _, row := range rows {
			if row.Key.Source == parent.Source && row.Key.Package == parent.Package && row.Key.Environment == parent.Environment && row.Key.Target == parent.Target && row.AgentID == adapter.ID() && row.Component != "runtime" {
				destination.Selected = true
				break
			}
		}
		preview.Destinations = append(preview.Destinations, destination)
	}
	return preview, nil
}
