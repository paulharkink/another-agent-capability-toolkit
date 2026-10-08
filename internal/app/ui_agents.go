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
		for _, record := range installed {
			if record.AgentID == id && record.AgentKind != "" {
				kind = record.AgentKind
				break
			}
		}
		discovery := agents.DiscoverAgent(ctx, kind, probe)
		native, nativeErr := agents.ResolveEnvironment(id, kind, probe.Home)
		if nativeErr != nil {
			continue
		}
		native, nativeErr = agents.ApplyNativeConfigOverrides(native)
		if nativeErr != nil {
			return nil, nativeErr
		}
		writePath, pathErr := agents.ResolveConfigWritePath(native)
		if pathErr != nil {
			return nil, pathErr
		}
		effectivePath := writePath
		if kind == "opencode" {
			for _, file := range discovery.ConfigFiles {
				if file.Exists && file.Path == writePath {
					effectivePath = file.Path
				}
			}
		}
		row := viewmodel.AgentManagementRow{
			ID: id, Name: discovery.Name, Detection: discovery.Detection,
			Evidence: discovery.Evidence, Note: discovery.Note, Home: native.Home,
			EffectiveConfigPath: effectivePath, WriteConfigPath: writePath,
		}
		if override := nativeConfigOverride(kind, native); override != "" {
			if row.Note != "" {
				row.Note += "; "
			}
			row.Note += override
		}
		if kind != id {
			row.Name = id
		}
		registrations := map[string]bool{}
		recordedHomes := map[string]bool{}
		recordedPaths := map[string]bool{}
		recordedProfilePaths := map[string]bool{}
		for _, file := range discovery.ConfigFiles {
			row.ConfigFiles = append(row.ConfigFiles, viewmodel.AgentConfigFile{Path: file.Path, Scope: file.Scope, Precedence: file.Precedence, Evidence: file.Evidence, Home: native.Home, Exists: file.Exists})
		}
		for _, record := range installed {
			if record.AgentID != id || (record.Component != "mcp" && record.Component != "skill") {
				continue
			}
			if record.AgentHome != "" {
				recordedHomes[filepath.Clean(record.AgentHome)] = true
			}
			if record.Component != "mcp" {
				if filepath.Clean(record.Destination) == filepath.Clean(native.SkillsDir) {
					if row.Note != "" {
						row.Note += "; "
					}
					row.Note += "Recorded skill destination shares native skill directory " + native.SkillsDir
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
				if home != filepath.Clean(native.Home) {
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
					row.Note += "Recorded AACT registration path differs from the process-native planned write file"
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
