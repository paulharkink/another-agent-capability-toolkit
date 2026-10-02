package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/agents"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

// UIAgentManagement only observes local installation evidence and candidate
// config files. AACT's ledger supplies its own registrations and exact paths;
// it never makes the client itself count as installed.
func (s *Service) UIAgentManagement(ctx context.Context) ([]viewmodel.AgentManagementRow, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	probe, err := agents.DefaultDiscoveryProbe()
	if err != nil {
		return nil, err
	}
	ids, err := s.UIAgents(ctx)
	if err != nil {
		return nil, err
	}
	ids = append(ids, "claude-desktop", "opencode-desktop")
	installed, err := s.Store.Installations()
	if err != nil {
		return nil, err
	}
	rows := make([]viewmodel.AgentManagementRow, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "all" || seen[id] {
			continue
		}
		seen[id] = true
		kind, _, _ := strings.Cut(id, ":")
		discovery := agents.DiscoverAgent(ctx, kind, probe)
		row := viewmodel.AgentManagementRow{
			ID: id, Name: discovery.Name, Detection: discovery.Detection,
			Evidence: discovery.Evidence, Note: discovery.Note,
		}
		if kind != id {
			row.Name = id
		}
		paths := map[string]bool{}
		registrations := map[string]bool{}
		for _, file := range discovery.ConfigFiles {
			row.ConfigFiles = append(row.ConfigFiles, viewmodel.AgentConfigFile(file))
			paths[filepath.Clean(file.Path)] = true
		}
		for _, record := range installed {
			if record.AgentID != id || record.Component != "mcp" {
				continue
			}
			profile := strings.Join([]string{record.Key.Source, record.Key.Package, record.Key.Environment, record.Key.Target}, " / ")
			if !registrations[profile] {
				row.Registrations = append(row.Registrations, profile)
				registrations[profile] = true
			}
			if record.Destination == "" || paths[filepath.Clean(record.Destination)] {
				continue
			}
			paths[filepath.Clean(record.Destination)] = true
			_, statErr := os.Stat(record.Destination)
			row.ConfigFiles = append(row.ConfigFiles, viewmodel.AgentConfigFile{
				Path: record.Destination, Scope: "AACT registration", Precedence: "recorded",
				Evidence: "AACT installation ledger", Exists: statErr == nil,
			})
		}
		sort.Strings(row.Registrations)
		rows = append(rows, row)
	}
	return rows, nil
}

// UIAgentConfig returns the exact file contents, including secrets and line
// breaks, from a candidate shown for the selected agent. It never writes.
func (s *Service) UIAgentConfig(ctx context.Context, id, path string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if path == "" {
		return "", fmt.Errorf("select a discovered configuration file")
	}
	rows, err := s.UIAgentManagement(ctx)
	if err != nil {
		return "", err
	}
	allowed := false
	for _, row := range rows {
		if row.ID != id {
			continue
		}
		for _, file := range row.ConfigFiles {
			if filepath.Clean(file.Path) == filepath.Clean(path) {
				allowed = true
				break
			}
		}
		break
	}
	if !allowed {
		return "", fmt.Errorf("%s is not a discovered config file for agent %s", path, id)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read agent config %s: %w", path, err)
	}
	return string(contents), nil
}
