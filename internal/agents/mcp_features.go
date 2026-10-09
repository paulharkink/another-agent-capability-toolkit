package agents

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/pelletier/go-toml/v2"
	"os"
)

type mcpAdapter struct{ *skillAdapter }

func (a *mcpAdapter) Features() FeatureSet { return FeatureSet{Skills: true, MCPs: true} }

func (a *mcpAdapter) PlannedConfigPath(_ context.Context, scope Scope) (string, error) {
	e, err := a.environment(scope)
	if err != nil {
		return "", err
	}
	return ResolveConfigWritePath(e)
}

func (a *mcpAdapter) registrationScope(scope Scope, key state.Key) (Environment, error) {
	e, err := a.environment(scope)
	if err != nil {
		return e, err
	}
	path, err := ResolveConfigWritePath(e)
	if err != nil {
		return e, err
	}
	e.ConfigPath = path
	e.Owned = map[string]Registration{}
	if a.deps.Store != nil {
		rows, err := a.deps.Store.Installations()
		if err != nil {
			return e, err
		}
		for _, row := range rows {
			if row.Component == "mcp" && row.AgentID == e.ID && row.Destination == path && row.Key == key {
				owned := Registration{Name: row.RegistrationName, URL: row.URL, Transport: row.Transport, TimeoutMS: row.TimeoutMS}
				if row.Digest != "" {
					entries, err := a.readMCPEntries(path)
					if err != nil {
						return e, err
					}
					if entry, ok := entries[row.RegistrationName]; ok {
						if nativeEntryDigest(entry) != row.Digest {
							return e, fmt.Errorf("owned MCP registration %q changed in %s", row.RegistrationName, path)
						}
						if headers, ok := entry["headers"].(map[string]any); ok {
							owned.Headers = map[string]string{}
							for name, value := range headers {
								if text, ok := value.(string); ok {
									owned.Headers[name] = text
								}
							}
						}
					}
				}
				e.Owned[row.RegistrationName] = owned
			}
		}
	}
	return e, nil
}
func (a *mcpAdapter) Register(ctx context.Context, scope Scope, request MCPRequest) (MCPRegistrationResult, error) {
	d, err := a.Detect(ctx, scope)
	if err != nil {
		return MCPRegistrationResult{}, err
	}
	if d.MCPDisabledReason != "" {
		return MCPRegistrationResult{}, fmt.Errorf("%s", d.MCPDisabledReason)
	}
	if !d.Installed {
		return MCPRegistrationResult{}, fmt.Errorf("agent %s is not installed: %s", a.Name(), d.Reason)
	}
	e, err := a.registrationScope(scope, request.Key)
	if err != nil {
		return MCPRegistrationResult{}, err
	}
	var ownedRows []state.Installation
	if a.deps.Store != nil {
		rows, readErr := a.deps.Store.Installations()
		if readErr != nil {
			return MCPRegistrationResult{}, readErr
		}
		for _, row := range rows {
			if row.Component == "mcp" && row.Key == request.Key && row.AgentID == e.ID && row.Destination == e.ConfigPath {
				ownedRows = append(ownedRows, row)
			}
		}
	}
	if err := a.registerMCPConfig(ctx, e, request.Registration); err != nil {
		result := MCPRegistrationResult{}
		var partial *partialCLIRegistrationError
		if errors.As(err, &partial) {
			for _, row := range ownedRows {
				if row.RegistrationName == partial.registration.Name && row.URL == partial.registration.URL {
					result.Removed = append(result.Removed, row)
				}
			}
		}
		return result, err
	}
	reg := request.Registration
	row := state.Installation{Key: request.Key, AgentID: e.ID, AgentHome: e.Home, AgentKind: e.Kind, Component: "mcp", Destination: e.ConfigPath, RegistrationName: reg.Name, URL: reg.URL, Transport: reg.Transport, TimeoutMS: reg.TimeoutMS, Mode: "registration"}
	if entries, err := a.readMCPEntries(e.ConfigPath); err == nil {
		if entry, ok := entries[reg.Name]; ok {
			row.Digest = nativeEntryDigest(entry)
		}
	}
	return MCPRegistrationResult{Installation: row}, nil
}

func (a *mcpAdapter) registerMCPConfig(ctx context.Context, scope Environment, registration Registration) error {
	switch a.kind {
	case "codex", "copilot-cli":
		return a.registerWithCLI(ctx, scope, registration)
	case "opencode", "claude", "copilot-intellij":
		return a.registerWithJSON(ctx, scope, registration)
	default:
		return fmt.Errorf("adapter %s does not support MCP registration", a.kind)
	}
}
func (a *mcpAdapter) Unregister(ctx context.Context, scope Scope, row state.Installation) error {
	e, err := a.registrationScope(scope, row.Key)
	if err != nil {
		return err
	}
	if row.AgentID != e.ID || row.Destination != e.ConfigPath {
		return fmt.Errorf("MCP registration does not belong to agent scope %s", e.ID)
	}
	if _, ok := e.Owned[row.RegistrationName]; !ok {
		e.Owned[row.RegistrationName] = Registration{Name: row.RegistrationName, URL: row.URL, Transport: row.Transport, TimeoutMS: row.TimeoutMS}
	}
	return a.unregisterMCPConfig(ctx, e, row.RegistrationName)
}

func (a *mcpAdapter) unregisterMCPConfig(ctx context.Context, scope Environment, name string) error {
	switch a.kind {
	case "codex", "copilot-cli":
		return a.unregisterWithCLI(ctx, scope, name)
	case "opencode", "claude", "copilot-intellij":
		return a.unregisterWithJSON(ctx, scope, name)
	default:
		return fmt.Errorf("adapter %s does not support MCP registration", a.kind)
	}
}

// readMCPInventory interprets only this adapter's native config format.
func nativeEntryDigest(entry map[string]any) string {
	data, _ := json.Marshal(entry)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
func (a *mcpAdapter) readMCPEntries(path string) (map[string]map[string]any, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	raw := map[string]any{}
	if a.kind == "codex" {
		err = toml.Unmarshal(data, &raw)
	} else {
		raw, err = standardJSON(data)
	}
	if err != nil {
		return nil, fmt.Errorf("agent config %s: %w", path, err)
	}
	parent := "mcpServers"
	switch a.kind {
	case "codex":
		parent = "mcp_servers"
	case "opencode":
		parent = "mcp"
	case "copilot-intellij":
		parent = "servers"
	}
	if raw[parent] == nil {
		return map[string]map[string]any{}, nil
	}
	entries, ok := raw[parent].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("agent config %s: %s must be an object", path, parent)
	}
	result := map[string]map[string]any{}
	for name, value := range entries {
		fields, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("agent config %s: MCP %s must be an object", path, name)
		}
		result[name] = fields
	}
	return result, nil
}
func (a *mcpAdapter) readMCPInventory(path string) (map[string]Registration, error) {
	entries, err := a.readMCPEntries(path)
	if err != nil {
		return nil, err
	}
	result := map[string]Registration{}
	for name, fields := range entries {
		url, _ := fields["url"].(string)
		transport := "streamable-http"
		if url == "" {
			transport = "stdio"
		} else if fields["type"] == "sse" {
			transport = "sse"
		}
		result[name] = Registration{Name: name, URL: url, Transport: transport}
	}
	return result, nil
}
func (a *mcpAdapter) Observe(ctx context.Context, scope Scope, request ObservationRequest) (Observation, error) {
	result, err := a.skillAdapter.Observe(ctx, scope, request)
	if err != nil {
		return result, err
	}
	e, err := a.environment(scope)
	if err != nil {
		return result, err
	}
	e.ConfigPath, err = ResolveConfigWritePath(e)
	if err != nil {
		return result, err
	}
	entries, readErr := a.readMCPInventory(e.ConfigPath)
	rawEntries, _ := a.readMCPEntries(e.ConfigPath)
	seen := map[string]bool{}
	for _, row := range request.Managed {
		if row.Component != "mcp" || row.AgentID != e.ID {
			continue
		}
		seen[row.RegistrationName] = true
		component := ComponentObservation{Kind: "mcp", Name: row.RegistrationName, RegistrationName: row.RegistrationName, Path: e.ConfigPath, Status: "absent", Managed: true}
		if readErr != nil {
			component.Status = "unavailable"
			component.Error = readErr.Error()
		} else if reg, ok := entries[row.RegistrationName]; ok {
			component.Status = "installed"
			component.URL = reg.URL
			component.Transport = reg.Transport
			if reg.URL != row.URL || (row.Digest != "" && nativeEntryDigest(rawEntries[row.RegistrationName]) != row.Digest) || (row.Transport != "" && reg.Transport != row.Transport) {
				component.Status = "modified"
				component.Error = "Agent registration endpoint differs from recorded endpoint"
			}
		}
		result.Components = append(result.Components, component)
	}
	if readErr != nil {
		result.Errors = append(result.Errors, readErr.Error())
	}
	if request.IncludeInventory {
		for name, reg := range entries {
			if !seen[name] {
				result.Components = append(result.Components, ComponentObservation{Kind: "mcp", Name: name, RegistrationName: name, URL: reg.URL, Transport: reg.Transport, Path: e.ConfigPath, Status: "installed"})
			}
		}
	}
	return result, nil
}
