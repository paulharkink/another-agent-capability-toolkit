package app

import (
	"context"
	"sort"
	"time"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/mcp"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

// UIProfileSnapshot merges configured profiles and registrations with a Docker
// observation. A Docker failure leaves configured rows and the last successful
// observation visible, marked stale.
func (s *Service) UIProfileSnapshot(ctx context.Context) (viewmodel.ProfileSnapshot, error) {
	var snapshot viewmodel.ProfileSnapshot
	configured, err := s.Store.Profiles()
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

	byKey := map[state.Key]*viewmodel.Profile{}
	add := func(k state.Key) *viewmodel.Profile {
		if profile := byKey[k]; profile != nil {
			return profile
		}
		profile := &viewmodel.Profile{Key: k, Name: k.Package, RuntimeStatus: "never-started", Ownership: "local"}
		if k.Source == s.Source.ID {
			for _, packageInfo := range s.Source.Catalog {
				if packageInfo.ID == k.Package {
					for _, mcpProfile := range packageInfo.MCPProfiles() {
						if k.Profile == "" || mcpProfile.Name == k.Profile {
							profile.Transport = mcpProfile.Transport
							break
						}
					}
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
				profile.StartDisabledReason = "Multiple running containers claim this profile"
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
				profile.StopDisabledReason = "Multiple running containers claim this profile"
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
				profile.RegistrationDisabledReason = "Multiple running containers claim this profile"
			} else {
				profile.RegistrationDisabledReason = "No MCP endpoint is configured"
			}
		}
		sort.Strings(profile.RegisteredAgents)
		snapshot.Profiles = append(snapshot.Profiles, *profile)
	}
	sort.Slice(snapshot.Profiles, func(i, j int) bool {
		a, b := snapshot.Profiles[i].Key, snapshot.Profiles[j].Key
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
	return snapshot, nil
}
