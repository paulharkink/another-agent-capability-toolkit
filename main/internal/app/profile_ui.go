package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/mcp"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/viewmodel"
	"io"
	"os"
	"strings"
)

type observedRuntime struct {
	MCPRuntime
	instances []mcp.Instance
	err       error
}

func (r observedRuntime) List(context.Context) ([]mcp.Instance, error) { return r.instances, r.err }

// UI snapshots share one Docker observation; selecting profiles never starts a process.
func (s *Service) UICapabilityInstances(ctx context.Context) (map[string]viewmodel.CapabilityInstanceSnapshot, error) {
	result := map[string]viewmodel.CapabilityInstanceSnapshot{}
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
		snapshot, err := scoped.InstanceSnapshot(ctx, p.ID)
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
func (s *Service) UICapabilityProfiles(ctx context.Context) (map[string]viewmodel.CapabilityProfileSnapshot, error) {
	return s.UICapabilityInstances(ctx)
}
func (s *Service) UICreateInstance(ctx context.Context, ref config.InstanceRef) error {
	return s.CreateInstance(ctx, ref)
}
func (s *Service) UIRemoveInstance(ctx context.Context, ref config.InstanceRef) (viewmodel.OperationResult, error) {
	return s.RemoveInstance(ctx, InstanceRequest{Ref: ref})
}
func (s *Service) UIResetInstanceToPreset(ctx context.Context, ref config.InstanceRef) (viewmodel.OperationResult, error) {
	return s.ResetInstanceToPreset(ctx, InstanceRequest{Ref: ref})
}
func (s *Service) UICreateProfile(ctx context.Context, ref config.ProfileRef) error {
	return s.UICreateInstance(ctx, ref)
}
func (s *Service) UIConfigurationInstanceRun(ctx context.Context, action string, ref config.InstanceRef, mcpID string) (string, error) {
	if action == "browser-login" {
		p, instance, key, err := s.loadInstance(ref)
		if err != nil {
			return "", err
		}
		definition, err := selectMCPProfile(p, mcpID)
		if err != nil {
			return "", err
		}
		if definition.BrowserController == nil {
			return "", errors.New("this capability does not declare a browser login helper")
		}
		if len(definition.BrowserController.When) > 0 {
			_, _, values, _, err := s.instanceValues(p, instance, key, InstanceRequest{Ref: ref})
			if err != nil {
				return "", err
			}
			if catalog.InputVisible(catalog.Input{VisibleWhen: definition.BrowserController.When}, values) {
				action = "authenticate"
			}
		}
		if action == "browser-login" {
			starter, ok := s.Options.Runtime.(interface {
				StartBrowser(context.Context, state.Key) (string, error)
			})
			if !ok {
				return "", errors.New("browser login is unavailable in this runtime")
			}
			return starter.StartBrowser(ctx, mcpDefinitionKey(key, p, definition))
		}
	}
	result, err := s.RunInstanceMCP(ctx, action, InstanceRequest{Ref: ref}, mcpID)
	if result.Logs != "" {
		return result.Logs, err
	}
	if result.LoginURL != "" {
		return "Login URL: " + result.LoginURL, err
	}
	if len(result.Instances) > 0 {
		return result.Instances[0].URL, err
	}
	return result.Message, err
}
func (s *Service) UIConfigurationProfileRun(ctx context.Context, action string, ref config.ProfileRef, mcpID string) (string, error) {
	return s.UIConfigurationInstanceRun(ctx, action, ref, mcpID)
}

// Compile-time guard for the injected runtime facade's lifecycle methods.
var _ interface {
	Logs(context.Context, state.Key) (io.ReadCloser, error)
} = observedRuntime{}

func (s *Service) UIPackAgentManagement(ctx context.Context) ([]viewmodel.AgentManagementRow, error) {
	rows := []viewmodel.AgentManagementRow{}
	for _, adapter := range s.adapterRegistry().Adapters() {
		d, err := adapter.Detect(ctx, s.agentScope(adapter.ID()))
		row := viewmodel.AgentManagementRow{ID: adapter.ID(), Name: adapter.Name(), Detection: d.State, Evidence: d.Evidence, Note: d.Reason, Home: d.Home}
		if err != nil {
			row.Note = err.Error()
		}
		if d.SkillsPath != "" {
			row.Note = strings.TrimSpace(row.Note + " Skills directory: " + d.SkillsPath)
		}
		for _, f := range d.ConfigFiles {
			row.ConfigFiles = append(row.ConfigFiles, viewmodel.AgentConfigFile{Path: f.Path, Scope: f.Scope, Precedence: f.Precedence, Evidence: f.Evidence, Exists: f.Exists, Home: d.Home})
			if f.Precedence == "effective" || row.EffectiveConfigPath == "" {
				row.EffectiveConfigPath = f.Path
				row.WriteConfigPath = f.Path
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}
func (s *Service) UIPackAgentConfig(ctx context.Context, id, path string) (string, error) {
	rows, err := s.UIPackAgentManagement(ctx)
	if err != nil {
		return "", err
	}
	for _, row := range rows {
		if row.ID == id {
			for _, f := range row.ConfigFiles {
				if f.Path == path {
					b, e := os.ReadFile(path)
					return string(b), e
				}
			}
		}
	}
	return "", fmt.Errorf("agent %s did not report config path %q", id, path)
}
