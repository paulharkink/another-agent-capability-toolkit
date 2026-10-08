package app

import (
	"context"
	"fmt"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/mcp"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
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
func (s *Service) UICapabilityProfiles(ctx context.Context) (map[string]viewmodel.CapabilityProfileSnapshot, error) {
	result := map[string]viewmodel.CapabilityProfileSnapshot{}
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
		snapshot, err := scoped.ProfileSnapshot(ctx, p.ID)
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
func (s *Service) UICreateProfile(ctx context.Context, ref config.ProfileRef) error {
	return s.CreateProfile(ctx, ref)
}
func (s *Service) UIConfigurationProfileRun(ctx context.Context, action string, ref config.ProfileRef, mcpID string) (string, error) {
	result, err := s.RunProfileMCP(ctx, action, ProfileRequest{Ref: ref}, mcpID)
	if result.Logs != "" {
		return result.Logs, err
	}
	if len(result.Instances) > 0 {
		return result.Instances[0].URL, err
	}
	return result.Message, err
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
