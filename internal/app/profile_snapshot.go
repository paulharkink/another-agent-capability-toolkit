package app

import (
	"context"
	"fmt"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/agents"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/forms"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/mcp"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
	"slices"
	"strings"
	"time"
)

func (s *Service) ProfileSnapshot(ctx context.Context, capabilityID string) (snapshot viewmodel.CapabilityProfileSnapshot, err error) {
	snapshot.Profiles = []viewmodel.CapabilityProfile{}
	snapshot.Errors = []string{}
	if err = ctx.Err(); err != nil {
		return snapshot, err
	}
	p, err := s.packageByID(capabilityID)
	if err != nil {
		return snapshot, err
	}
	profiles, err := s.profilesForCapability(capabilityID)
	if err != nil {
		return snapshot, err
	}
	records, err := s.Store.Profiles()
	if err != nil {
		return snapshot, err
	}
	rows, err := s.Store.Installations()
	if err != nil {
		return snapshot, err
	}
	var instances []mcp.Instance
	var runtimeErr error
	if p.HasMCP() {
		instances, runtimeErr = s.Options.Runtime.List(ctx)
		s.observationMu.Lock()
		if runtimeErr != nil {
			instances = append([]mcp.Instance(nil), s.lastInstances...)
			snapshot.ObservedAt = s.lastObservedAt
			snapshot.Errors = append(snapshot.Errors, runtimeErr.Error())
		} else {
			s.lastInstances = append([]mcp.Instance(nil), instances...)
			s.lastObservedAt = time.Now()
			snapshot.ObservedAt = s.lastObservedAt
		}
		s.observationMu.Unlock()
	} else {
		snapshot.ObservedAt = time.Now()
	}
	registry := s.adapterRegistry()
	for _, profile := range profiles {
		row := viewmodel.CapabilityProfile{Ref: profile.Ref, Origin: "local", ConfigStatus: "configured", Components: []viewmodel.ComponentStatus{}, MCPs: []viewmodel.RuntimeStatus{}}
		if profile.Path != "" {
			row.Origin = "pack"
		}
		key, e := s.Store.ResolveProfileKey(s.Source.ID, p.ID, profile.Ref.Name)
		row.Key = key
		row.CompletionScopeRelevant = completionScopeRelevant(key, records, rows)
		if e != nil {
			row.ConfigStatus = "invalid"
			row.ConfigError = e.Error()
			row.CompletionScopeError = e.Error()
			snapshot.Profiles = append(snapshot.Profiles, row)
			continue
		}
		if profile.Error != "" {
			row.ConfigStatus = "invalid"
			row.ConfigError = profile.Error
			row.CompletionScopeError = profile.Error
			snapshot.Profiles = append(snapshot.Profiles, row)
			continue
		}
		values, _, e := s.profileValues(p, profile, key, ProfileRequest{})
		if e != nil {
			row.ConfigStatus = "invalid"
			row.ConfigError = e.Error()
		} else if e = forms.Validate(p.Inputs, values); e != nil {
			row.ConfigStatus = "needs-input"
			row.ConfigError = e.Error()
		}
		if e != nil {
			row.CompletionScopeError = e.Error()
		}
		managed := []state.Installation{}
		destinations := []string{}
		hasDestinationSelection := false
		for _, r := range records {
			if r.Key == key && r.Selection != nil {
				hasDestinationSelection = true
				destinations = append(destinations, r.Selection.DestinationIDs...)
			}
		}
		for _, r := range rows {
			if sameCapabilityKey(r.Key, key) && r.Component != "runtime" {
				managed = append(managed, r)
				if !slices.Contains(destinations, r.AgentID) {
					destinations = append(destinations, r.AgentID)
				}
			}
		}
		if e == nil {
			s.populateCompletionScope(ctx, p, profile.Ref, key, values, records, managed, &row)
		}
		if len(destinations) == 0 && !hasDestinationSelection {
			for _, a := range registry.Adapters() {
				d, e := a.Detect(ctx, s.agentScope(a.ID()))
				if e == nil && d.Installed && (!p.HasMCP() || a.Features().MCPs) {
					destinations = append(destinations, a.ID())
				}
			}
		}
		for _, id := range destinations {
			kind := id
			scope := s.agentScope(id)
			for _, r := range managed {
				if r.AgentID == id {
					if r.AgentKind != "" {
						kind = r.AgentKind
					}
					scope = agents.Scope{ID: id, Home: r.AgentHome, ExplicitHome: true}
					if r.Component == "mcp" {
						scope.ConfigPathOverride = r.Destination
						break
					}
				}
			}
			a, e := registry.Adapter(kind)
			var observation agents.Observation
			if e == nil {
				observation, e = a.Observe(ctx, scope, agents.ObservationRequest{IncludeInventory: true, Key: key, Managed: managed})
			}
			if e != nil {
				snapshot.Errors = append(snapshot.Errors, id+": "+e.Error())
			}
			for _, message := range observation.Errors {
				snapshot.Errors = append(snapshot.Errors, id+": "+message)
			}
			add := func(kind, name, native string) {
				status := viewmodel.ComponentStatus{AgentID: id, Kind: kind, Name: name, Status: "absent"}
				if e != nil {
					status.Status = "unavailable"
					status.Error = e.Error()
				} else {
					for _, c := range observation.Components {
						if c.Kind == kind && (c.Name == native || c.RegistrationName == native) {
							status.Status = c.Status
							status.Error = c.Error
							status.Managed = c.Managed
							break
						}
					}
				}
				row.Components = append(row.Components, status)
			}
			for _, skill := range p.SkillDefinitions() {
				add("skill", skill.Name, skill.Name)
			}
			for _, definition := range packageMCPProfiles(p, values) {
				child := mcpProfileKey(key, p, definition)
				name, nerr := declaredRegistrationName(definition, child, values)
				if nerr != nil {
					snapshot.Errors = append(snapshot.Errors, nerr.Error())
				}
				add("mcp", definition.Name, name)
			}
			for _, plugin := range p.Plugins {
				if a != nil && slices.Contains(a.Features().PluginFormats, plugin.Format) {
					native := plugin.Name
					for _, r := range managed {
						if r.Component == "plugin" && strings.HasPrefix(r.ReleaseID, plugin.Name+"@") {
							native = r.ReleaseID
						}
					}
					add("plugin", plugin.Name, native)
				}
			}
		}
		for _, definition := range p.MCPDefinitions() {
			child := mcpProfileKey(key, p, definition)
			runtime := viewmodel.RuntimeStatus{MCPID: definition.Name, Status: "stopped", Ownership: "local", ObservedAt: snapshot.ObservedAt, Stale: runtimeErr != nil}
			if definition.EnabledInput != "" && values[definition.EnabledInput] != true {
				runtime.Status = "disabled"
			}
			registeredURL := ""
			for _, r := range managed {
				if r.Component == "mcp" && r.Key == child {
					registeredURL = r.URL
					if r.ExternalRegistration {
						runtime.Ownership = "external"
					}
				}
			}
			matches := []mcp.Instance{}
			for _, instance := range instances {
				if instance.Key == child || (registeredURL != "" && instance.URL == registeredURL) {
					matches = append(matches, instance)
				}
			}
			if len(matches) > 1 {
				runtime.Status = "conflict"
				runtime.Ownership = "unknown"
				runtime.Error = fmt.Sprintf("%d runtimes claim this profile MCP", len(matches))
			} else if len(matches) == 1 {
				runtime.Status = matches[0].Status
				runtime.Ownership = matches[0].Ownership
				runtime.URL = matches[0].URL
			} else if runtimeErr != nil {
				runtime.Status = "unavailable"
				runtime.Error = runtimeErr.Error()
			} else if registeredURL != "" && runtime.Ownership == "external" {
				runtime.Status = "unobserved"
				runtime.URL = registeredURL
			}
			row.MCPs = append(row.MCPs, runtime)
		}
		snapshot.Profiles = append(snapshot.Profiles, row)
	}
	return snapshot, nil
}

// populateCompletionScope resolves intended work independently from the
// observed component facts. PreviewProfile is read-only; its Selected flag can
// reflect already-achieved state for persisted selections, so saved intent is
// read from the profile ledger whenever it exists.
func (s *Service) populateCompletionScope(ctx context.Context, p catalog.Package, ref config.ProfileRef, key state.Key, values map[string]any, records []state.ProfileRecord, managed []state.Installation, row *viewmodel.CapabilityProfile) {
	preview, err := s.PreviewProfile(ctx, ProfileRequest{Ref: ref})
	if err != nil {
		row.CompletionScopeError = err.Error()
		return
	}
	items, _, err := componentSelection(p, values, preview.SelectedItemIDs, false)
	if err != nil {
		row.CompletionScopeError = err.Error()
		return
	}
	for _, item := range items {
		for _, name := range item.Skills {
			row.SelectedComponents = append(row.SelectedComponents, viewmodel.SelectedComponent{Kind: "skill", Name: name})
		}
		for _, name := range item.MCPs {
			row.SelectedComponents = append(row.SelectedComponents, viewmodel.SelectedComponent{Kind: "mcp", Name: name})
		}
		for _, name := range item.Plugins {
			row.SelectedComponents = append(row.SelectedComponents, viewmodel.SelectedComponent{Kind: "plugin", Name: name})
		}
	}

	var selectedDestinationIDs []string
	selectionRecorded := false
	for _, record := range records {
		if record.Key == key && record.Selection != nil {
			selectionRecorded = true
			selectedDestinationIDs = append(selectedDestinationIDs, record.Selection.DestinationIDs...)
			break
		}
	}
	if selectionRecorded {
		row.CompletionScopeRelevant = true
	} else if len(managed) > 0 {
		// Older state recorded successful component rows before it persisted
		// selection intent. Keep those historical scopes useful and anchored to
		// the destinations where effects were actually recorded.
		for _, installation := range managed {
			selectedDestinationIDs = append(selectedDestinationIDs, installation.AgentID)
		}
		row.CompletionScopeRelevant = true
	} else {
		for _, destination := range preview.Destinations {
			if destination.Selected {
				selectedDestinationIDs = append(selectedDestinationIDs, destination.ID)
			}
		}
	}
	row.SelectedDestinations = uniqueStrings(selectedDestinationIDs)
	row.CompletionScopeKnown = true
	if len(row.SelectedComponents) == 0 {
		return
	}

	componentsByKind := map[string][]string{}
	for _, component := range row.SelectedComponents {
		componentsByKind[component.Kind] = append(componentsByKind[component.Kind], component.Name)
	}
	byID := map[string]viewmodel.SetupDestination{}
	for _, destination := range preview.Destinations {
		byID[destination.ID] = destination
	}
	registry := s.adapterRegistry()
	for _, selectedID := range row.SelectedDestinations {
		lookupID := selectedID
		if adapter, adapterErr := registry.Adapter(selectedID); adapterErr == nil {
			lookupID = adapter.ID()
		}
		destination, found := byID[lookupID]
		if !found || !destination.Detected {
			continue
		}
		eligible := true
		if len(componentsByKind["skill"]) > 0 && !destination.Features.Skills {
			eligible = false
		}
		if len(componentsByKind["mcp"]) > 0 {
			if !destination.MCPRegistrationAvailable {
				eligible = false
			}
		}
		if len(componentsByKind["plugin"]) > 0 {
			for _, name := range componentsByKind["plugin"] {
				format := ""
				for _, plugin := range p.Plugins {
					if plugin.Name == name {
						format = plugin.Format
						break
					}
				}
				if format == "" || !slices.Contains(destination.Features.PluginFormats, format) {
					eligible = false
				}
			}
		}
		if eligible {
			row.EligibleDestinations = append(row.EligibleDestinations, selectedID)
		}
	}
	row.EligibleDestinations = uniqueStrings(row.EligibleDestinations)
}

func completionScopeRelevant(key state.Key, records []state.ProfileRecord, installations []state.Installation) bool {
	for _, record := range records {
		if record.Key == key && record.Selection != nil {
			return true
		}
	}
	for _, installation := range installations {
		if sameCapabilityKey(installation.Key, key) && installation.Component != "runtime" {
			return true
		}
	}
	return false
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}
