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
	ids, err := s.UIAgents(ctx)
	if err != nil {
		return nil, err
	}
	ids = append(ids, agents.DesktopDiscoveryIDs()...)
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
		for _, record := range installed {
			if record.AgentID == id && record.AgentKind != "" {
				kind = record.AgentKind
				break
			}
		}
		adapter, adapterErr := s.adapterFor(id, kind)
		if adapterErr != nil {
			continue
		}
		scope := s.agentScope(id)
		scope.ID = id
		detection, detectErr := adapter.Detect(ctx, scope)
		if detectErr != nil {
			return nil, detectErr
		}
		if adapter.ID() == "generic" && id != "all" && id != "generic" {
			detection.State = "unverified"
			detection.Installed = false
			detection.Evidence = ""
			detection.ConfigPath = ""
		}
		writePath := detection.ConfigPath
		if writePath == "" {
			for _, file := range detection.ConfigFiles {
				if file.Precedence == "effective" {
					writePath = file.Path
					break
				}
			}
		}
		row := viewmodel.AgentManagementRow{
			ID: id, Name: adapter.Name(), Detection: detection.State,
			Evidence: detection.Evidence, Note: detection.Reason, Home: detection.Home,
			EffectiveConfigPath: writePath, WriteConfigPath: writePath,
		}
		if kind != id {
			row.Name = id
		}
		registrations := map[string]bool{}
		recordedHomes := map[string]bool{}
		recordedPaths := map[string]bool{}
		recordedProfilePaths := map[string]bool{}
		for _, file := range detection.ConfigFiles {
			row.ConfigFiles = append(row.ConfigFiles, viewmodel.AgentConfigFile{Path: file.Path, Scope: file.Scope, Precedence: file.Precedence, Evidence: file.Evidence, Home: detection.Home, Exists: file.Exists})
		}
		for _, record := range installed {
			if record.AgentID != id || (record.Component != "mcp" && record.Component != "skill") {
				continue
			}
			if record.AgentHome != "" {
				recordedHomes[filepath.Clean(record.AgentHome)] = true
			}
			if record.Component != "mcp" {
				if filepath.Clean(record.Destination) == filepath.Clean(detection.SkillsPath) {
					if row.Note != "" {
						row.Note += "; "
					}
					row.Note += "Recorded skill destination shares adapter skill directory " + detection.SkillsPath
				}
				continue
			}
			profile := strings.Join([]string{record.Key.Source, record.Key.Package, record.Key.Environment, record.Key.Target}, " / ")
			if !registrations[profile] {
				row.Registrations = append(row.Registrations, profile)
				registrations[profile] = true
			}
			if record.Destination == "" {
				continue
			}
			cleanPath := filepath.Clean(record.Destination)
			recordedPaths[cleanPath] = true
			profilePath := profile + "\x00" + cleanPath
			if recordedProfilePaths[profilePath] {
				continue
			}
			recordedProfilePaths[profilePath] = true
			_, statErr := os.Stat(record.Destination)
			row.ConfigFiles = append(row.ConfigFiles, viewmodel.AgentConfigFile{
				Path: record.Destination, Scope: "AACT registration", Precedence: "recorded",
				Evidence: "AACT installation ledger", Profile: profile, Home: record.AgentHome, Exists: statErr == nil,
			})
		}
		if len(recordedHomes) == 1 {
			for home := range recordedHomes {
				if home != filepath.Clean(detection.Home) {
					row.Home = home
					if row.Note != "" {
						row.Note += "; "
					}
					row.Note += "AACT has profile-specific records using custom home " + home + "; effective config shown above is the process-native candidate"
				}
			}
		} else if len(recordedHomes) > 1 {
			if row.Note != "" {
				row.Note += "; "
			}
			row.Note += "AACT profile-specific records span multiple homes; Home shows the process-native home, and each recorded profile's home and config path are listed below"
		}
		if len(recordedPaths) == 1 {
			for path := range recordedPaths {
				row.WriteConfigPath = path
				if path != writePath {
					if row.Note != "" {
						row.Note += "; "
					}
					row.Note += "Recorded AACT registration path differs from the adapter-reported planned write file"
				}
			}
		} else if len(recordedPaths) > 1 {
			row.Note += "; multiple recorded MCP paths are listed below by AACT registration"
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
