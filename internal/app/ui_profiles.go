package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/mcp"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

// UIProfileLogs revalidates the selected profile's runtime immediately before
// asking the runtime for logs. A stale UI row can therefore never turn a
// not-yet-created target into a Docker logs request.
func (s *Service) UIProfileLogs(ctx context.Context, key state.Key) (string, error) {
	if err := s.requireActivePack(key.Source); err != nil {
		return "", err
	}
	source, err := s.forSource(key.Source)
	if err != nil {
		return "", err
	}
	instances, err := source.Options.Runtime.List(ctx)
	if err != nil {
		return "", fmt.Errorf("refresh runtime observation before reading logs: %w", err)
	}
	for _, instance := range instances {
		if instance.Key != key || instance.Status != "running" {
			continue
		}
		if instance.Ownership != "local" {
			return "", fmt.Errorf("refusing logs: runtime ownership is %s", instance.Ownership)
		}
		result, err := source.RunInstanceMCP(ctx, "logs", instanceRequestForStateKey(key), mcpNameForStateKey(key))
		return result.Logs, err
	}
	return "", errors.New("No MCP container has been created for this target; configure this target and start it before opening logs")
}

// UIInstanceRun performs a lifecycle action for one observed MCP child definition.
// The child key is preserved so a named MCP action never affects a sibling.
func (s *Service) UIInstanceRun(ctx context.Context, action string, key state.Key) (string, error) {
	if err := s.requireActivePack(key.Source); err != nil {
		return "", err
	}
	source, err := s.forSource(key.Source)
	if err != nil {
		return "", err
	}
	result, err := source.RunInstanceMCP(ctx, action, instanceRequestForStateKey(key), mcpNameForStateKey(key))
	if err != nil {
		return "", err
	}
	if len(result.Instances) > 0 {
		return result.Instances[0].URL, nil
	}
	return result.Message, nil
}

// UIRemoveRuntimeRecord removes one exact runtime identity. A live container
// is stopped only when it is still locally owned under this exact key; stale,
// unlinked, missing, external, or otherwise unowned entries lose only their
// exact local ledger row.
func (s *Service) UIRemoveRuntimeRecord(ctx context.Context, key state.Key, runtimeID string) (string, error) {
	if runtimeID == "" {
		return "", errors.New("runtime ID is required")
	}
	if err := s.requireActivePack(key.Source); err != nil {
		return "", err
	}
	source, err := s.forSource(key.Source)
	if err != nil {
		return "", err
	}
	rows, err := source.Store.Installations()
	if err != nil {
		return "", err
	}
	var record *state.Installation
	for i := range rows {
		row := rows[i]
		if row.Component == "runtime" && row.AgentID == "docker" && row.Key == key && row.SourcePath == runtimeID {
			copy := row
			record = &copy
			break
		}
	}
	if record == nil {
		return "", errors.New("exact runtime ledger record was not found")
	}
	instances, err := source.Options.Runtime.List(ctx)
	if err != nil {
		return "", fmt.Errorf("refresh runtime observation before removing record: %w", err)
	}
	for _, instance := range instances {
		if instance.ID != runtimeID {
			continue
		}
		if instance.Key != key {
			if err := source.Store.Remove(*record); err != nil {
				return "", fmt.Errorf("forget stale runtime record %s: %w", runtimeID, err)
			}
			return "Forgot stale runtime record " + runtimeID + "; the observed runtime was left running under its current instance key", nil
		}
		if instance.Status == "missing" {
			continue
		}
		if instance.Ownership != "local" {
			if err := source.Store.Remove(*record); err != nil {
				return "", fmt.Errorf("forget runtime record %s: %w", runtimeID, err)
			}
			return "Forgot runtime record " + runtimeID + "; runtime ownership is " + instance.Ownership + " and no container was removed", nil
		}
		exact, ok := source.Options.Runtime.(interface {
			StopExact(context.Context, state.Key, string) error
		})
		if !ok {
			return "", errors.New("runtime does not support exact-ID removal; record was left unchanged")
		}
		if err := exact.StopExact(ctx, key, runtimeID); err != nil {
			return "", fmt.Errorf("remove exact runtime %s: %w", runtimeID, err)
		}
		return "Removed exact AACT-owned runtime " + runtimeID, nil
	}
	if err := source.Store.Remove(*record); err != nil {
		return "", fmt.Errorf("forget missing runtime %s: %w", runtimeID, err)
	}
	return "Forgot exact missing runtime record " + runtimeID + "; no container was removed", nil
}

func (s *Service) UIProfileRun(ctx context.Context, action string, key state.Key) (string, error) {
	return s.UIInstanceRun(ctx, action, key)
}

func instanceRequestForStateKey(key state.Key) InstanceRequest {
	return InstanceRequest{Ref: config.InstanceRef{PackID: key.Source, CapabilityID: key.Package, Name: key.Target}}
}

func mcpNameForStateKey(key state.Key) string {
	if key.MCP != "" {
		return key.MCP
	}
	return key.MCPDefinition
}

// UIInstanceSnapshot merges configured instances and registrations with a Docker
// observation. A Docker failure leaves configured rows and the last successful
// observation visible, marked stale.
func (s *Service) UIInstanceSnapshot(ctx context.Context) (viewmodel.InstanceSnapshot, error) {
	var snapshot viewmodel.InstanceSnapshot
	configured, err := s.Store.Instances()
	if err != nil {
		return snapshot, err
	}
	installed, err := s.Store.Installations()
	if err != nil {
		return snapshot, err
	}
	instances, observationErr := s.Options.Runtime.List(ctx)
	stale := observationErr != nil
	s.observationMu.Lock()
	if stale {
		instances = append([]mcp.Instance(nil), s.lastInstances...)
		snapshot.ObservedAt = s.lastObservedAt
		snapshot.DockerError = observationErr.Error()
	} else {
		s.lastInstances = nil
		for _, instance := range instances {
			if instance.Status != "external" {
				s.lastInstances = append(s.lastInstances, instance)
			}
		}
		s.lastObservedAt = time.Now()
		snapshot.ObservedAt = s.lastObservedAt
	}
	s.observationMu.Unlock()
	snapshot.ObservationStale = stale

	byKey := map[state.Key]*viewmodel.Instance{}
	add := func(k state.Key) *viewmodel.Instance {
		if profile := byKey[k]; profile != nil {
			return profile
		}
		profile := &viewmodel.Instance{Key: k, Name: k.Package, RuntimeStatus: "never-started", Ownership: "local"}
		if k.Source == s.Source.ID {
			for _, packageInfo := range s.Source.Catalog {
				if packageInfo.ID == k.Package {
					profileName := k.MCPDefinition
					if profileName == "" {
						profileName = k.MCP
					}
					for _, definition := range packageInfo.MCPDefinitions() {
						if (len(packageInfo.MCPs) == 0 && profileName == "") || definition.Name == profileName {
							profile.Transport = definition.Transport
							break
						}
					}
					break
				}
			}
		}
		byKey[k] = profile
		return profile
	}
	for _, record := range configured {
		profile := add(record.Key)
		if record.Name != "" {
			profile.Name = record.Name
		}
	}
	for _, row := range installed {
		if row.Component != "mcp" && row.Component != "runtime" {
			continue
		}
		profile := add(row.Key)
		if row.Component == "mcp" {
			profile.RegisteredAgents = append(profile.RegisteredAgents, row.AgentID)
			if row.Transport != "" {
				profile.Transport = row.Transport
			}
			if profile.URL == "" {
				profile.URL = row.URL
			}
		} else {
			if row.URL != "" && profile.URL == "" {
				profile.URL = row.URL
			}
			profile.LocalLastAction = row.LastAction
			profile.LocalLastActionAt = row.LastActionAt
		}
	}
	priority := map[state.Key]int{}
	running := map[state.Key]int{}
	for _, instance := range instances {
		profile := add(instance.Key)
		if instance.Status == "running" {
			running[instance.Key]++
		}
		if instance.Ownership == "local" && instance.LastAction != "" && !instance.LastActionAt.Before(profile.LocalLastActionAt) {
			profile.LocalLastAction = instance.LastAction
			profile.LocalLastActionAt = instance.LastActionAt
		}
		rank := 3
		switch instance.Status {
		case "running":
			rank = 4
		case "missing":
			rank = 2
		case "external":
			rank = 1
		}
		if rank < priority[instance.Key] {
			continue
		}
		priority[instance.Key] = rank
		profile.RuntimeStatus = instance.Status
		profile.Ownership = instance.Ownership
		profile.URL = instance.URL
		profile.LastAction = instance.LastAction
		profile.LastActionAt = instance.LastActionAt
		profile.ObservedAt = snapshot.ObservedAt
		profile.ObservationStale = stale
	}
	for _, profile := range byKey {
		if running[profile.Key] > 1 {
			profile.RuntimeStatus = "conflict"
			profile.Ownership = "ambiguous"
			profile.URL = ""
		}
		if stale && profile.ObservedAt.IsZero() {
			profile.RuntimeStatus = "unavailable"
			profile.ObservationStale = true
		}
		profile.CanStart = !stale && profile.Ownership == "local" && (profile.RuntimeStatus == "never-started" || profile.RuntimeStatus == "missing" || profile.RuntimeStatus == "exited")
		profile.CanStop = !stale && profile.Ownership == "local" && profile.RuntimeStatus == "running"
		profile.CanConfigureRegistrations = profile.URL != ""
		if !profile.CanStart {
			switch {
			case stale:
				profile.StartDisabledReason = "Docker observation is unavailable"
			case profile.RuntimeStatus == "conflict":
				profile.StartDisabledReason = "Multiple running containers claim this MCP definition"
			case profile.Ownership == "other-aact":
				profile.StartDisabledReason = "Runtime belongs to another AACT installation"
			case profile.Ownership != "local":
				profile.StartDisabledReason = "Runtime owner is unknown"
			default:
				profile.StartDisabledReason = "Runtime is already running"
			}
		}
		if !profile.CanStop {
			switch {
			case stale:
				profile.StopDisabledReason = "Docker observation is unavailable"
			case profile.RuntimeStatus == "conflict":
				profile.StopDisabledReason = "Multiple running containers claim this MCP definition"
			case profile.Ownership == "other-aact":
				profile.StopDisabledReason = "Runtime belongs to another AACT installation"
			case profile.Ownership != "local":
				profile.StopDisabledReason = "Runtime owner is unknown"
			default:
				profile.StopDisabledReason = "Runtime is not running"
			}
		}
		if !profile.CanConfigureRegistrations {
			if profile.RuntimeStatus == "conflict" {
				profile.RegistrationDisabledReason = "Multiple running containers claim this MCP definition"
			} else {
				profile.RegistrationDisabledReason = "No MCP endpoint is configured"
			}
		}
		sort.Strings(profile.RegisteredAgents)
		snapshot.Instances = append(snapshot.Instances, *profile)
		snapshot.Profiles = append(snapshot.Profiles, *profile)
	}
	sort.Slice(snapshot.Instances, func(i, j int) bool {
		a, b := snapshot.Instances[i].Key, snapshot.Instances[j].Key
		if a.Source != b.Source {
			return a.Source < b.Source
		}
		if a.Package != b.Package {
			return a.Package < b.Package
		}
		if a.Environment != b.Environment {
			return a.Environment < b.Environment
		}
		return a.Target < b.Target
	})
	snapshot.Profiles = append([]viewmodel.Instance(nil), snapshot.Instances...)
	return snapshot, nil
}

// UIProfileSnapshot is retained for callers of the former selected-instance API.
func (s *Service) UIProfileSnapshot(ctx context.Context) (viewmodel.ProfileSnapshot, error) {
	return s.UIInstanceSnapshot(ctx)
}
