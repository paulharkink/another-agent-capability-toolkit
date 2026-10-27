package app

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/agents"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/forms"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/mcp"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

func (s *Service) InstanceSnapshot(ctx context.Context, capabilityID string) (snapshot viewmodel.CapabilityInstanceSnapshot, err error) {
	snapshot.Instances = []viewmodel.CapabilityInstance{}
	snapshot.Profiles = []viewmodel.CapabilityInstance{}
	snapshot.Errors = []string{}
	if err = ctx.Err(); err != nil {
		return snapshot, err
	}
	p, err := s.packageByID(capabilityID)
	if err != nil {
		return snapshot, err
	}
	configuredInstances, err := s.instancesForCapability(capabilityID)
	if err != nil {
		return snapshot, err
	}
	var runtimes []mcp.Instance
	var runtimeErr error
	if p.HasMCP() {
		runtimes, runtimeErr = s.Options.Runtime.List(ctx)
		s.observationMu.Lock()
		if runtimeErr != nil {
			runtimes = nil
			snapshot.ObservedAt = s.lastObservedAt
			snapshot.Errors = append(snapshot.Errors, runtimeErr.Error())
		} else {
			s.lastInstances = append([]mcp.Instance(nil), runtimes...)
			s.lastObservedAt = time.Now()
			snapshot.ObservedAt = s.lastObservedAt
		}
		s.observationMu.Unlock()
	} else {
		snapshot.ObservedAt = time.Now()
	}
	foreignParents := observedForeignRuntimeParents(s.Source.ID, p.ID, runtimes, runtimeErr == nil)
	if err = s.reconcileRuntimeLedger(ctx, p, configuredInstances, runtimes, runtimeErr == nil); err != nil {
		snapshot.Errors = append(snapshot.Errors, "runtime ledger reconciliation: "+err.Error())
	}
	records, err := s.Store.Instances()
	if err != nil {
		return snapshot, err
	}
	rows, err := s.Store.Installations()
	if err != nil {
		return snapshot, err
	}
	// Runtime.List includes ledger-backed missing rows. Reconciliation may have
	// just forgotten a successfully stopped row after fresh Docker observation;
	// omit that now-unlinked synthetic row from this same snapshot as well.
	remainingRuntimeRows := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		if row.Component == "runtime" {
			remainingRuntimeRows[row.Key.ID()+"\x00"+row.SourcePath] = struct{}{}
		}
	}
	filteredRuntimes := runtimes[:0]
	for _, runtime := range runtimes {
		_, hasLedgerRow := remainingRuntimeRows[runtime.Key.ID()+"\x00"+runtime.ID]
		if runtime.Status == "missing" && runtime.LastAction == "stop" && !hasLedgerRow {
			continue
		}
		filteredRuntimes = append(filteredRuntimes, runtime)
	}
	runtimes = filteredRuntimes
	registry := s.adapterRegistry()
	for _, instance := range configuredInstances {
		row := viewmodel.CapabilityInstance{Ref: instance.Ref, Origin: "local", ConfigStatus: "configured", Components: []viewmodel.ComponentStatus{}, MCPs: []viewmodel.RuntimeStatus{}}
		if instance.Path != "" {
			row.Origin = "pack"
		}
		key, e := s.Store.ResolveInstanceKey(s.Source.ID, p.ID, instance.Ref.Name)
		row.Key = key
		if e == nil {
			if _, observedForeign := foreignParents[key]; observedForeign {
				// The running runtime is the authoritative cross-installation view.
				// Do not present this Windows installation's preset/defaults/answers
				// as the inputs used by the foreign instance.
				continue
			}
		}
		row.CompletionScopeRelevant = completionScopeRelevant(key, records, rows)
		if e != nil {
			row.ConfigStatus = "invalid"
			row.ConfigError = e.Error()
			row.CompletionScopeError = e.Error()
			snapshot.Instances = append(snapshot.Instances, row)
			snapshot.Profiles = append(snapshot.Profiles, row)
			continue
		}
		if instance.Error != "" {
			row.ConfigStatus = "invalid"
			row.ConfigError = instance.Error
			row.CompletionScopeError = instance.Error
			snapshot.Instances = append(snapshot.Instances, row)
			snapshot.Profiles = append(snapshot.Profiles, row)
			continue
		}
		_, _, values, _, e := s.instanceValuesForPreview(p, instance, key, InstanceRequest{})
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
			s.populateCompletionScope(ctx, p, instance.Ref, key, values, records, managed, &row)
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
				child := mcpDefinitionKey(key, p, definition)
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
			child := mcpDefinitionKey(key, p, definition)
			runtime := viewmodel.RuntimeStatus{MCPID: definition.Name, Key: child, Status: "stopped", Ownership: "local", ObservedAt: snapshot.ObservedAt, Stale: runtimeErr != nil, RuntimeRecords: []viewmodel.RuntimeRecord{}}
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
			for _, runtimeInstance := range runtimes {
				// Runtime identity is keyed to this MCP definition. Endpoint URLs
				// are not unique: two MCP registrations may intentionally share a
				// URL, so URL matching could expose one runtime under its sibling.
				if runtimeInstance.Key == child {
					matches = append(matches, runtimeInstance)
				}
			}
			for _, match := range matches {
				runtime.RuntimeRecords = append(runtime.RuntimeRecords, viewmodel.RuntimeRecord{
					ID: match.ID, Name: match.Name, Status: match.Status, Ownership: match.Ownership,
					URL: match.URL, LastAction: match.LastAction, LastActionAt: match.LastActionAt,
				})
			}
			if len(matches) > 1 {
				// Keep every runtime independently addressable under its declared
				// MCP child. A duplicate is not a parent-instance conflict; the
				// child summary remains useful while the UI exposes exact records.
				runtime.Status = summarizeRuntimeRecords(matches)
				runtime.Ownership = matches[0].Ownership
				for _, match := range matches[1:] {
					if match.Ownership != runtime.Ownership {
						runtime.Ownership = "multiple"
						break
					}
				}
				if runtime.Status == "multiple" {
					runtime.Error = fmt.Sprintf("%d runtime records are linked to this MCP; review each record individually", len(matches))
				}
			} else if len(matches) == 1 {
				runtime.Status = matches[0].Status
				runtime.Ownership = matches[0].Ownership
				runtime.URL = matches[0].URL
			} else if runtimeErr != nil {
				runtime.Status = "unavailable"
				runtime.Ownership = "unknown"
				runtime.Error = runtimeErr.Error()
			} else if registeredURL != "" && runtime.Ownership == "external" {
				runtime.Status = "unobserved"
				runtime.URL = registeredURL
			}
			if definition.HealthPath != "" && runtime.Status == "running" && runtime.URL != "" {
				authenticated, healthErr := mcp.CheckAuthentication(ctx, runtime.URL, definition.HealthPath)
				if healthErr != nil || !authenticated {
					runtime.AuthenticationStatus = viewmodel.AuthenticationFailed
				} else {
					runtime.AuthenticationStatus = viewmodel.AuthenticationComplete
				}
			}
			row.MCPs = append(row.MCPs, runtime)
		}
		declaredChildren := map[state.Key]bool{}
		for _, definition := range p.MCPDefinitions() {
			declaredChildren[mcpDefinitionKey(key, p, definition)] = true
		}
		orphanRuntimes := map[state.Key]*viewmodel.RuntimeStatus{}
		for _, record := range rows {
			if record.Component != "runtime" || !sameCapabilityKey(record.Key, key) || declaredChildren[record.Key] {
				continue
			}
			runtime := orphanRuntimes[record.Key]
			if runtime == nil {
				name := record.Key.MCP
				if name == "" {
					name = record.Key.MCPDefinition
				}
				runtime = &viewmodel.RuntimeStatus{
					MCPID: name, Key: record.Key, Status: "missing", Ownership: "unknown",
					ObservedAt: snapshot.ObservedAt, Stale: runtimeErr != nil,
					Error:          "This runtime refers to an MCP definition no longer present in the Capability Pack; remove the exact runtime record to recover.",
					RuntimeRecords: []viewmodel.RuntimeRecord{},
				}
				if runtimeErr != nil {
					runtime.Status = "unavailable"
					runtime.Error = "Runtime ownership could not be refreshed; retry after Docker is available."
				}
				orphanRuntimes[record.Key] = runtime
			}
			observed := false
			for _, instance := range runtimes {
				if instance.ID != record.SourcePath {
					continue
				}
				observed = true
				runtime.Status = instance.Status
				runtime.Ownership = instance.Ownership
				runtime.RuntimeRecords = append(runtime.RuntimeRecords, viewmodel.RuntimeRecord{
					ID: instance.ID, Name: instance.Name, Status: instance.Status, Ownership: instance.Ownership,
					URL: instance.URL, LastAction: instance.LastAction, LastActionAt: instance.LastActionAt,
				})
				break
			}
			if !observed && runtimeErr == nil {
				runtime.RuntimeRecords = append(runtime.RuntimeRecords, viewmodel.RuntimeRecord{ID: record.SourcePath, Status: "missing", Ownership: "unknown"})
			}
		}
		orphanKeys := make([]state.Key, 0, len(orphanRuntimes))
		for child := range orphanRuntimes {
			orphanKeys = append(orphanKeys, child)
		}
		sort.Slice(orphanKeys, func(i, j int) bool {
			left, right := orphanKeys[i], orphanKeys[j]
			return strings.Join([]string{left.Source, left.Package, left.Environment, left.Target, left.MCP, left.MCPDefinition}, "\x00") < strings.Join([]string{right.Source, right.Package, right.Environment, right.Target, right.MCP, right.MCPDefinition}, "\x00")
		})
		for _, child := range orphanKeys {
			row.MCPs = append(row.MCPs, *orphanRuntimes[child])
		}
		snapshot.Instances = append(snapshot.Instances, row)
		snapshot.Profiles = append(snapshot.Profiles, row)
	}
	foreignKeys := make([]state.Key, 0, len(foreignParents))
	for key := range foreignParents {
		foreignKeys = append(foreignKeys, key)
	}
	sort.Slice(foreignKeys, func(i, j int) bool {
		left, right := foreignKeys[i], foreignKeys[j]
		return strings.Join([]string{left.Source, left.Package, left.Environment, left.Target}, "\x00") < strings.Join([]string{right.Source, right.Package, right.Environment, right.Target}, "\x00")
	})
	for _, key := range foreignKeys {
		row := observedForeignCapabilityInstance(p.ID, key, foreignParents[key], p, snapshot.ObservedAt)
		row.Components, err = s.observedForeignAgentComponents(ctx, p, key, foreignParents[key], rows, registry)
		if err != nil {
			snapshot.Errors = append(snapshot.Errors, err.Error())
		}
		snapshot.Instances = append(snapshot.Instances, row)
		snapshot.Profiles = append(snapshot.Profiles, row)
	}
	return snapshot, nil
}

// observedForeignAgentComponents projects this installation's own agent
// observations onto a Docker-discovered foreign parent. It reads only local
// installation rows and the active Pack definitions; it never loads the
// foreign instance's inputs, answers, or generated files.
func (s *Service) observedForeignAgentComponents(ctx context.Context, p catalog.Package, parent state.Key, runtimes []mcp.Instance, rows []state.Installation, registry AdapterProvider) ([]viewmodel.ComponentStatus, error) {
	managedByAgent := map[string][]state.Installation{}
	for _, row := range rows {
		if !sameCapabilityKey(row.Key, parent) || row.Component == "runtime" || row.AgentID == "" {
			continue
		}
		switch row.Component {
		case "skill", "mcp", "plugin":
			managedByAgent[row.AgentID] = append(managedByAgent[row.AgentID], row)
		}
	}
	agentIDs := make([]string, 0, len(managedByAgent))
	for id := range managedByAgent {
		agentIDs = append(agentIDs, id)
	}
	sort.Strings(agentIDs)
	components := []viewmodel.ComponentStatus{}
	errorsFound := []string{}
	definitions := p.MCPDefinitions()
	observations := map[string]agents.Observation{}
	observeErrors := map[string]error{}
	componentSeen := map[string]bool{}
	for _, id := range agentIDs {
		managed := managedByAgent[id]
		kind := id
		scope := s.agentScope(id)
		for _, row := range managed {
			if row.AgentKind != "" {
				kind = row.AgentKind
			}
			if row.AgentHome != "" {
				scope.Home = row.AgentHome
				scope.ExplicitHome = true
			}
			if row.Component == "mcp" && scope.ConfigPathOverride == "" {
				scope.ConfigPathOverride = row.Destination
			}
		}
		adapter, adapterErr := registry.Adapter(kind)
		var observation agents.Observation
		if adapterErr == nil {
			observation, adapterErr = adapter.Observe(ctx, scope, agents.ObservationRequest{Key: parent, Managed: managed, IncludeInventory: true})
			observations[id] = observation
			observeErrors[id] = adapterErr
		}
		if adapterErr != nil {
			errorsFound = append(errorsFound, id+": "+adapterErr.Error())
		}
		for _, row := range managed {
			name := foreignAgentComponentName(p, parent, row)
			componentSeen[foreignObservedComponentID(id, row.Component, name)] = true
			status := viewmodel.ComponentStatus{AgentID: id, Kind: row.Component, Name: name, Status: "absent"}
			if adapterErr != nil {
				status.Status = "unavailable"
				status.Error = adapterErr.Error()
			} else {
				for _, observed := range observation.Components {
					if observedForeignAgentComponentMatches(row, observed) {
						status.Status = observed.Status
						status.Error = observed.Error
						status.Managed = observed.Managed
						break
					}
				}
			}
			components = append(components, status)
		}
		for _, message := range observation.Errors {
			errorsFound = append(errorsFound, id+": "+message)
		}
	}
	// A removed local installation row cannot prove that a native agent binding
	// is absent. Inspect each adapter's current inventory against the exact
	// running Docker child identity so the projected parent can report known
	// absence after Uninstall, while preserving unavailable observations as
	// Unknown in the L2 summary.
	for _, adapter := range registry.Adapters() {
		if !adapter.Features().MCPs {
			continue
		}
		id := adapter.ID()
		if _, ok := observations[id]; !ok {
			scope := s.agentScope(id)
			detection, detectErr := adapter.Detect(ctx, scope)
			configExists := false
			configKnown := len(detection.ConfigFiles) > 0
			for _, file := range detection.ConfigFiles {
				configExists = configExists || file.Exists
			}
			if detectErr == nil && !detection.Installed && configKnown && !configExists && len(managedByAgent[id]) == 0 {
				// Do not invoke optional agent commands for adapters that have no
				// detected installation, native MCP config, or exact-key binding.
				// Their configured MCP destination is known absent; they cannot
				// make an otherwise confirmed empty inventory Unknown.
				for _, definition := range definitions {
					childKey := mcpDefinitionKey(parent, p, definition)
					if !hasObservedRuntime(runtimes, childKey) {
						continue
					}
					componentID := foreignObservedComponentID(id, "mcp", definition.Name)
					if componentSeen[componentID] {
						continue
					}
					components = append(components, viewmodel.ComponentStatus{
						AgentID: id, Kind: "mcp", Name: definition.Name, Status: "absent",
					})
					componentSeen[componentID] = true
				}
				continue
			}
			observation, observeErr := adapter.Observe(ctx, s.agentScope(id), agents.ObservationRequest{
				Key: parent, IncludeInventory: true,
			})
			observations[id] = observation
			observeErrors[id] = observeErr
			if observeErr != nil {
				errorsFound = append(errorsFound, id+": "+observeErr.Error())
			}
			for _, message := range observation.Errors {
				errorsFound = append(errorsFound, id+": "+message)
			}
		}
		observation := observations[id]
		observeErr := observeErrors[id]
		for _, definition := range definitions {
			childKey := mcpDefinitionKey(parent, p, definition)
			var runtime *mcp.Instance
			for i := range runtimes {
				if runtimes[i].Key == childKey {
					runtime = &runtimes[i]
					break
				}
			}
			if runtime == nil {
				continue
			}
			componentID := foreignObservedComponentID(id, "mcp", definition.Name)
			if componentSeen[componentID] {
				continue
			}
			status := viewmodel.ComponentStatus{AgentID: id, Kind: "mcp", Name: definition.Name, Status: "absent"}
			if runtime.RegistrationName == "" || runtime.URL == "" {
				status.Status = "unavailable"
				status.Error = "Observed runtime is missing its agent registration name or endpoint"
				components = append(components, status)
				componentSeen[componentID] = true
				continue
			}
			matchedName := false
			for _, candidate := range observation.Components {
				if candidate.Kind != "mcp" || candidate.RegistrationName != runtime.RegistrationName && candidate.Name != runtime.RegistrationName {
					continue
				}
				matchedName = true
				status.Status = candidate.Status
				status.Error = candidate.Error
				status.Managed = candidate.Managed
				if candidate.URL != runtime.URL {
					status.Status = "modified"
					status.Error = "Agent registration endpoint differs from the observed runtime"
				}
				break
			}
			if !matchedName && (observeErr != nil || len(observation.Errors) > 0 || observation.Detection.State == "unverified" || observation.Detection.State == "") {
				status.Status = "unavailable"
				if observeErr != nil {
					status.Error = observeErr.Error()
				} else if len(observation.Errors) > 0 {
					status.Error = strings.Join(observation.Errors, "; ")
				} else {
					status.Error = observation.Detection.Reason
				}
			}
			components = append(components, status)
			componentSeen[componentID] = true
		}
	}
	if len(errorsFound) > 0 {
		return components, fmt.Errorf("observed agent components: %s", strings.Join(errorsFound, "; "))
	}
	return components, nil
}

func foreignObservedComponentID(agentID, kind, name string) string {
	return agentID + "\x00" + kind + "\x00" + name
}

func hasObservedRuntime(runtimes []mcp.Instance, key state.Key) bool {
	for _, runtime := range runtimes {
		if runtime.Key == key {
			return true
		}
	}
	return false
}

func observedForeignAgentComponentMatches(row state.Installation, observed agents.ComponentObservation) bool {
	if observed.Kind != row.Component {
		return false
	}
	switch row.Component {
	case "skill":
		return observed.Path == row.Destination || observed.Name == filepath.Base(row.Destination)
	case "mcp":
		return observed.RegistrationName == row.RegistrationName || observed.Name == row.RegistrationName
	case "plugin":
		return observed.Name == foreignPluginName(row)
	default:
		return false
	}
}

func foreignAgentComponentName(p catalog.Package, parent state.Key, row state.Installation) string {
	switch row.Component {
	case "skill":
		return filepath.Base(row.Destination)
	case "mcp":
		for _, definition := range p.MCPDefinitions() {
			if mcpDefinitionKey(parent, p, definition) == row.Key {
				return definition.Name
			}
		}
		if row.Key.MCPDefinition != "" {
			return row.Key.MCPDefinition
		}
		if row.Key.MCP != "" {
			return row.Key.MCP
		}
		return row.RegistrationName
	case "plugin":
		return foreignPluginName(row)
	default:
		return row.Component
	}
}

func foreignPluginName(row state.Installation) string {
	name, _, _ := strings.Cut(row.ReleaseID, "@")
	if name != "" {
		return name
	}
	return row.RegistrationName
}

func observedForeignRuntimeParents(sourceID, packageID string, runtimes []mcp.Instance, observed bool) map[state.Key][]mcp.Instance {
	parents := map[state.Key][]mcp.Instance{}
	if !observed {
		return parents
	}
	for _, runtime := range runtimes {
		if runtime.Status != "running" || runtime.Ownership != "other-aact" || runtime.Key.Source != sourceID || runtime.Key.Package != packageID || runtime.Key.Target == "" {
			continue
		}
		parent := runtime.Key
		parent.MCP = ""
		parent.MCPDefinition = ""
		parents[parent] = append(parents[parent], runtime)
	}
	return parents
}

func observedForeignCapabilityInstance(packageID string, parent state.Key, runtimes []mcp.Instance, p catalog.Package, observedAt time.Time) viewmodel.CapabilityInstance {
	const reason = foreignRuntimeManagementReason
	row := viewmodel.CapabilityInstance{
		Ref: config.InstanceRef{PackID: parent.Source, CapabilityID: packageID, Name: parent.Target},
		Key: parent, Origin: "observed", ConfigStatus: "read-only", ConfigError: reason,
		Ownership: "other-aact", ManagementReason: reason, Components: []viewmodel.ComponentStatus{}, MCPs: []viewmodel.RuntimeStatus{},
	}
	byChild := map[state.Key][]mcp.Instance{}
	for _, runtime := range runtimes {
		byChild[runtime.Key] = append(byChild[runtime.Key], runtime)
	}
	children := make([]state.Key, 0, len(byChild))
	for child := range byChild {
		children = append(children, child)
	}
	sort.Slice(children, func(i, j int) bool {
		return runtimeChildName(children[i]) < runtimeChildName(children[j])
	})
	definitions := p.MCPDefinitions()
	for _, child := range children {
		matches := byChild[child]
		name := runtimeChildName(child)
		if name == "" && len(definitions) == 1 {
			name = definitions[0].Name
		}
		if name == "" && len(matches) > 0 {
			name = matches[0].RegistrationName
		}
		if name == "" && len(matches) > 0 {
			name = matches[0].Name
		}
		status := summarizeRuntimeRecords(matches)
		runtime := viewmodel.RuntimeStatus{MCPID: name, Key: child, Status: status, Ownership: "other-aact", ObservedAt: observedAt, RuntimeRecords: []viewmodel.RuntimeRecord{}}
		if len(matches) > 1 {
			runtime.Error = fmt.Sprintf("%d running runtimes are linked to this MCP", len(matches))
		}
		if len(matches) > 0 {
			runtime.URL = matches[0].URL
			for _, match := range matches {
				runtime.RuntimeRecords = append(runtime.RuntimeRecords, viewmodel.RuntimeRecord{
					ID: match.ID, Name: match.Name, Status: match.Status, Ownership: match.Ownership,
					URL: match.URL, LastAction: match.LastAction, LastActionAt: match.LastActionAt,
				})
			}
		}
		row.MCPs = append(row.MCPs, runtime)
	}
	return row
}

func summarizeRuntimeRecords(records []mcp.Instance) string {
	if len(records) == 0 {
		return "stopped"
	}
	status := records[0].Status
	if status == "" {
		status = "unknown"
	}
	for _, record := range records[1:] {
		current := record.Status
		if current == "" {
			current = "unknown"
		}
		if current != status {
			return "multiple"
		}
	}
	return status
}

// ProfileSnapshot is retained for callers of the former selected-instance API.
func (s *Service) ProfileSnapshot(ctx context.Context, capabilityID string) (viewmodel.CapabilityProfileSnapshot, error) {
	return s.InstanceSnapshot(ctx, capabilityID)
}

// populateCompletionScope resolves intended work independently from the
// observed component facts. PreviewProfile is read-only; its Selected flag can
// reflect already-achieved state for persisted selections, so saved intent is
// read from the profile ledger whenever it exists.
func (s *Service) populateCompletionScope(ctx context.Context, p catalog.Package, ref config.InstanceRef, key state.Key, values map[string]any, records []state.ProfileRecord, managed []state.Installation, row *viewmodel.CapabilityProfile) {
	preview, err := s.PreviewInstance(ctx, InstanceRequest{Ref: ref})
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
