package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/agents"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/expressions"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/forms"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/mcp"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/render"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type profileOperationLockKey struct{}

type AdapterProvider interface {
	Adapter(string) (agents.Adapter, error)
	Adapters() []agents.Adapter
}
type ProfileRequest struct {
	Ref                                  config.ProfileRef
	Inputs                               map[string]any
	ActiveInputGroups                    map[string]string
	ResetInputs, ItemIDs, DestinationIDs []string
	Interactive, SkillsOnly              bool
	ExternalURLs                         map[string]string
}

func isDynamicChoice(input catalog.Input) bool {
	switch input.Type {
	case "choice", "multichoice", "multiple-choice":
		return len(input.Options) == 0
	default:
		return false
	}
}

func selectedPrepareMCPs(p catalog.Package, values map[string]any, items []catalog.InstallationItem) []catalog.MCP {
	selected := map[string]bool{}
	for _, item := range items {
		for _, name := range item.MCPs {
			selected[name] = true
		}
	}
	result := []catalog.MCP{}
	for _, definition := range packageMCPProfiles(p, values) {
		if selected[definition.Name] && definition.Actions["prepare"].Argv != nil {
			result = append(result, definition)
		}
	}
	return result
}

func editProfileInputs(ctx context.Context, editor Editor, defs []catalog.Input, values, fixed map[string]any) error {
	visible, editable := editableWithFixedContext(defs, values, fixed)
	if len(visible) == 0 {
		return nil
	}
	edited, err := editor(ctx, visible, editable)
	if err != nil {
		return err
	}
	merged := withFixed(mergeEditedValues(values, edited), fixed)
	for name := range values {
		delete(values, name)
	}
	for name, value := range merged {
		values[name] = value
	}
	return nil
}

func (s *Service) adapterRegistry() AdapterProvider {
	if s.Options.Adapters != nil {
		return s.Options.Adapters
	}
	return agents.NewRegistry(agents.Dependencies{Store: s.Store, Runner: s.Options.Runner, Probe: s.discoveryProbe()})
}
func (s *Service) agentScope(id string) agents.Scope {
	if scope, ok := s.Options.AgentScopes[id]; ok {
		return scope
	}
	return agents.Scope{ID: id}
}
func (s *Service) loadProfile(ref config.ProfileRef) (catalog.Package, config.Profile, state.Key, error) {
	p, err := s.packageByID(ref.CapabilityID)
	if err != nil {
		return p, config.Profile{}, state.Key{}, err
	}
	if ref.PackID != "" && ref.PackID != s.Source.ID {
		return p, config.Profile{}, state.Key{}, invalid(fmt.Errorf("Capability Pack %q is not selected", ref.PackID))
	}
	ref.PackID = s.Source.ID
	if ref.Name == "" {
		profiles, err := s.profilesForCapability(p.ID)
		if err != nil {
			return p, config.Profile{}, state.Key{}, err
		}
		if len(profiles) != 1 {
			names := []string{}
			for _, pr := range profiles {
				names = append(names, pr.Ref.Name)
			}
			return p, config.Profile{}, state.Key{}, invalid(fmt.Errorf("select a profile for %s (available: %s); create another profile if none exist", p.ID, strings.Join(names, ", ")))
		}
		ref.Name = profiles[0].Ref.Name
	}
	key, err := s.Store.ResolveProfileKey(s.Source.ID, p.ID, ref.Name)
	if err != nil {
		return p, config.Profile{}, key, err
	}
	profile, err := config.LoadProfile(s.Source.CapabilityPack(), p.ID, ref.Name)
	if err == nil {
		return p, profile, key, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return p, profile, key, invalid(err)
	}
	records, e := s.Store.Profiles()
	if e != nil {
		return p, profile, key, e
	}
	for _, record := range records {
		if record.Key == key && record.Local {
			return p, config.Profile{Ref: ref, Raw: map[string]any{}}, key, nil
		}
	}
	return p, profile, key, invalid(err)
}
func (s *Service) profilesForCapability(id string) ([]config.Profile, error) {
	profiles, err := config.DiscoverProfiles(s.Source.CapabilityPack(), id)
	if err != nil {
		return nil, err
	}
	records, err := s.Store.Profiles()
	if err != nil {
		return nil, err
	}
	for _, r := range records {
		if r.Key.Source != s.Source.ID || r.Key.Package != id || !r.Local || r.Key.MCP != "" || r.Key.Profile != "" {
			continue
		}
		found := false
		for _, p := range profiles {
			if p.Ref.Name == r.Key.Target {
				found = true
			}
		}
		if !found {
			profiles = append(profiles, config.Profile{Ref: config.ProfileRef{PackID: s.Source.ID, CapabilityID: id, Name: r.Key.Target}, Raw: map[string]any{}})
		}
	}
	slices.SortFunc(profiles, func(a, b config.Profile) int { return strings.Compare(a.Ref.Name, b.Ref.Name) })
	return profiles, nil
}
func (s *Service) CreateProfile(ctx context.Context, ref config.ProfileRef) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if ref.PackID != "" && ref.PackID != s.Source.ID {
		return invalid(errors.New("select this Capability Pack before creating a profile"))
	}
	if _, err := s.packageByID(ref.CapabilityID); err != nil {
		return err
	}
	if !config.ValidProfileName(ref.Name) {
		return invalid(fmt.Errorf("invalid profile name %q", ref.Name))
	}
	existing, err := s.profilesForCapability(ref.CapabilityID)
	if err != nil {
		return err
	}
	for _, p := range existing {
		if p.Ref.Name == ref.Name {
			return invalid(fmt.Errorf("profile %q already exists", ref.Name))
		}
	}
	key, err := s.Store.ResolveProfileKey(s.Source.ID, ref.CapabilityID, ref.Name)
	if err != nil {
		return err
	}
	return s.Store.WithLock(ctx, func() error { return s.Store.RecordProfile(state.ProfileRecord{Key: key, Name: ref.Name, Local: true}) })
}
func (s *Service) profileValues(p catalog.Package, pr config.Profile, key state.Key, q ProfileRequest) (catalog.Package, config.Profile, map[string]any, map[string]any, error) {
	known := map[string]bool{}
	for _, d := range p.Inputs {
		known[d.Name] = true
	}
	for name := range q.Inputs {
		if !known[name] {
			return p, pr, nil, nil, invalid(fmt.Errorf("unknown input %q", name))
		}
	}
	saved, err := s.Store.Answers(key)
	if err != nil {
		return p, pr, nil, nil, err
	}
	for _, name := range q.ResetInputs {
		if !known[name] {
			return p, pr, nil, nil, invalid(fmt.Errorf("unknown reset input %q", name))
		}
		delete(saved, name)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return p, pr, nil, nil, err
	}
	defaults := map[string]any{}
	for _, def := range p.Inputs {
		if def.Default != nil {
			defaults[def.Name] = def.Default
		}
	}
	packFile := s.Source.ManifestPath
	if packFile == "" {
		packFile = filepath.Join(s.Source.Root, "aact.toml")
	}
	layers := []map[string]any{defaults, s.Source.PackageDefaults[p.ID], pr.Raw, saved, q.Inputs}
	files := []string{filepath.Join(p.Dir, "package.toml"), packFile, pr.Path, filepath.Join(s.Store.Root(), "answers", key.ID()+".json"), filepath.Join(cwd, ".aact-inputs")}
	for n := range layers {
		if n < 3 {
			layers[n] = withoutInputExpressions(p.Inputs, layers[n])
		}
		layers[n], err = config.ResolveInputPaths(p.Inputs, layers[n], files[n])
		if err != nil {
			return p, pr, nil, nil, invalid(err)
		}
	}
	saved = layers[3]
	submitted := layers[4]
	var inherited map[string]any
	seedDefs := append([]catalog.Input(nil), p.Inputs...)
	for i := range seedDefs {
		if expressions.ReferencesRoot(fmt.Sprint(seedDefs[i].Default), "inputs") {
			seedDefs[i].Default = nil
		}
	}
	seedInherited, err := forms.ResolvePartial(seedDefs, layers[0], layers[1], layers[2])
	if err != nil {
		return p, pr, nil, nil, invalid(err)
	}
	fixedBefore := map[string]any{}
	for name, policy := range pr.InputPolicy {
		if policy == "fixed" {
			fixedBefore[name] = nil
		}
	}
	unlocked := map[string]any{}
	for _, layer := range []map[string]any{layers[3], layers[4]} {
		for key, value := range layer {
			if _, locked := fixedBefore[key]; !locked {
				unlocked[key] = value
			}
		}
	}
	seed, err := forms.ResolvePartial(seedDefs, seedInherited, unlocked)
	if err != nil {
		return p, pr, nil, nil, invalid(err)
	}
	inputDefs, err := catalog.ResolveInputDefinitions(p, seed)
	if err != nil {
		return p, pr, nil, nil, invalid(err)
	}
	p.Inputs = inputDefs
	defaults = map[string]any{}
	for _, def := range p.Inputs {
		if def.Default != nil {
			defaults[def.Name] = def.Default
		}
	}
	layers[0], err = config.ResolveInputPaths(p.Inputs, defaults, files[0])
	if err != nil {
		return p, pr, nil, nil, invalid(err)
	}
	pr, err = config.ResolveExpressions(pr, seed, p.Inputs)
	if err != nil {
		return p, pr, nil, nil, invalid(err)
	}
	layers[2], err = config.ResolveInputPaths(p.Inputs, pr.Raw, files[2])
	if err != nil {
		return p, pr, nil, nil, invalid(err)
	}
	inherited, err = forms.ResolvePartial(p.Inputs, layers[0], layers[1], layers[2])
	if err != nil {
		return p, pr, nil, nil, invalid(err)
	}
	fixed, err := fixedTargetInputs(p.Inputs, config.Target{Path: pr.Path, InputPolicy: pr.InputPolicy}, pr.Raw)
	if err != nil {
		return p, pr, nil, nil, invalid(err)
	}
	input := map[string]any{}
	for k, v := range saved {
		if _, locked := fixed[k]; !locked {
			input[k] = v
		}
	}
	for k, v := range submitted {
		if _, locked := fixed[k]; !locked {
			input[k] = v
		}
	}
	values, err := forms.ResolvePartial(p.Inputs, inherited, input, fixed)
	if err != nil {
		return p, pr, values, inherited, invalid(err)
	}
	pr, err = config.ResolveExpressions(pr, values, p.Inputs)
	if err != nil {
		return p, pr, nil, inherited, invalid(err)
	}
	p, err = catalog.ResolveExpressions(p, values)
	if err != nil {
		return p, pr, nil, inherited, invalid(err)
	}
	return p, pr, values, inherited, nil
}

func withoutInputExpressions(defs []catalog.Input, values map[string]any) map[string]any {
	out, _ := cloneExpressionTree(values).(map[string]any)
	for _, def := range defs {
		removeInputExpression(out, []string{def.Name})
		removeInputExpression(out, []string{"inputs", def.Name})
		if def.ConfigKey != "" {
			removeInputExpression(out, []string{def.ConfigKey})
			removeInputExpression(out, strings.Split(def.ConfigKey, "."))
		}
	}
	return out
}

func cloneExpressionTree(value any) any {
	switch node := value.(type) {
	case map[string]any:
		copy := make(map[string]any, len(node))
		for key, child := range node {
			copy[key] = cloneExpressionTree(child)
		}
		return copy
	case []any:
		copy := make([]any, len(node))
		for index, child := range node {
			copy[index] = cloneExpressionTree(child)
		}
		return copy
	default:
		return value
	}
}

func removeInputExpression(values map[string]any, path []string) {
	if len(path) == 0 {
		return
	}
	current := values
	for _, part := range path[:len(path)-1] {
		nested, ok := current[part].(map[string]any)
		if !ok {
			return
		}
		current = nested
	}
	last := path[len(path)-1]
	text, ok := current[last].(string)
	if ok && expressions.ReferencesRoot(text, "inputs") {
		delete(current, last)
	}
}

// effectiveActiveInputGroups resolves UI method metadata from declared
// exclusive groups. Fixed profile values take precedence over submitted UI
// metadata; legacy records fall back to a unique populated declared input.
func effectiveActiveInputGroups(defs []catalog.Input, values, fixed map[string]any, explicit, saved map[string]string) map[string]string {
	members := map[string]map[string]bool{}
	for _, def := range defs {
		if def.ExclusiveGroup != "" {
			if members[def.ExclusiveGroup] == nil {
				members[def.ExclusiveGroup] = map[string]bool{}
			}
			members[def.ExclusiveGroup][def.Name] = true
		}
	}
	out := map[string]string{}
	for group, groupMembers := range members {
		for _, def := range defs {
			if def.ExclusiveGroup == group {
				if _, isFixed := fixed[def.Name]; isFixed && formsValueFilled(fixed[def.Name]) {
					out[group] = def.Name
					break
				}
			}
		}
		if out[group] != "" {
			continue
		}
		if candidate, specified := explicit[group]; specified {
			if groupMembers[candidate] {
				if _, isFixed := fixed[candidate]; !isFixed || formsValueFilled(fixed[candidate]) {
					out[group] = candidate
					continue
				}
			}
			if candidate == "" {
				populated := ""
				ambiguous := false
				for _, def := range defs {
					if def.ExclusiveGroup == group && formsValueFilled(values[def.Name]) {
						if populated != "" {
							ambiguous = true
							break
						}
						populated = def.Name
					}
				}
				if !ambiguous && populated != "" {
					out[group] = populated
				}
				continue
			}
		}
		populated := ""
		ambiguous := false
		for _, def := range defs {
			if def.ExclusiveGroup == group && formsValueFilled(values[def.Name]) {
				if populated != "" {
					ambiguous = true
					break
				}
				populated = def.Name
			}
		}
		if !ambiguous && populated != "" {
			out[group] = populated
			continue
		}
		if candidate := saved[group]; groupMembers[candidate] {
			if _, isFixed := fixed[candidate]; !isFixed || formsValueFilled(fixed[candidate]) {
				out[group] = candidate
			}
		}
	}
	return out
}

// validateProfileValuesForActiveGroups keeps type and ordinary input checks
// for every value, while applying exclusive-group validation only to the
// selected member. Inactive methods may retain older populated values so a
// method switch does not erase saved answers or let stale defaults block it.
func validateProfileValuesForActiveGroups(defs []catalog.Input, values map[string]any, active map[string]string) error {
	selected := append([]catalog.Input(nil), defs...)
	groupRequired := map[string]bool{}
	for _, def := range defs {
		if def.ExclusiveGroup != "" && def.Required {
			groupRequired[def.ExclusiveGroup] = true
		}
	}
	for i := range selected {
		group := selected[i].ExclusiveGroup
		if group == "" || active[group] == "" {
			continue
		}
		if selected[i].Name != active[group] {
			selected[i].ExclusiveGroup = ""
			selected[i].Required = false
			continue
		}
		selected[i].Required = selected[i].Required || groupRequired[group]
	}
	return forms.Validate(selected, values)
}

func formsValueFilled(value any) bool {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v) != ""
	case []string:
		return len(v) > 0
	case bool:
		return v
	case nil:
		return false
	default:
		return true
	}
}

func validateActiveInputGroups(defs []catalog.Input, groups map[string]string) error {
	declared := map[string]map[string]bool{}
	for _, def := range defs {
		if def.ExclusiveGroup == "" {
			continue
		}
		if declared[def.ExclusiveGroup] == nil {
			declared[def.ExclusiveGroup] = map[string]bool{}
		}
		declared[def.ExclusiveGroup][def.Name] = true
	}
	for group, input := range groups {
		if input == "" {
			continue
		}
		if !declared[group][input] {
			return fmt.Errorf("active input %q is not a member of declared group %q", input, group)
		}
	}
	return nil
}
func invalidIf(err error) error {
	if err != nil {
		return invalid(err)
	}
	return nil
}
func (s *Service) PreviewProfile(ctx context.Context, q ProfileRequest) (viewmodel.SetupPreview, error) {
	if err := ctx.Err(); err != nil {
		return viewmodel.SetupPreview{}, err
	}
	p, pr, key, err := s.loadProfile(q.Ref)
	if err != nil {
		return viewmodel.SetupPreview{}, err
	}
	p, pr, values, inherited, err := s.profileValues(p, pr, key, q)
	credentialState, credentialNote := s.credentialObservation(p, key)
	preview := viewmodel.SetupPreview{Key: key, PackageName: p.Name, PackRoot: s.Source.Root, ProfilePath: pr.Path, ProfileOrigin: "local", MCP: p.HasMCP(), MCPDefinitions: p.MCPDefinitions(), PluginDefinitions: p.PluginDefinitions(), CredentialState: credentialState, CredentialNote: credentialNote}
	if err != nil {
		return preview, err
	}
	storedGroups, err := s.Store.ActiveInputGroups(key)
	if err != nil {
		return preview, err
	}
	fixed, err := fixedTargetInputs(p.Inputs, config.Target{Path: pr.Path, InputPolicy: pr.InputPolicy}, pr.Raw)
	if err != nil {
		return preview, invalid(err)
	}
	if err = validateActiveInputGroups(p.Inputs, q.ActiveInputGroups); err != nil {
		return preview, invalid(err)
	}
	preview.ActiveInputGroups = effectiveActiveInputGroups(p.Inputs, values, fixed, q.ActiveInputGroups, storedGroups)
	if p.UI != nil {
		preview.Sections = p.UI.Sections
		preview.HasManifestUI = len(p.UI.Sections) > 0
	}
	if pr.Path != "" {
		preview.ProfileOrigin = "pack"
		b, e := os.ReadFile(pr.Path)
		if e != nil {
			return preview, e
		}
		preview.ProfileTOML = string(b)
	}
	installations, err := s.Store.Installations()
	if err != nil {
		return preview, err
	}
	currentRegistration := activeRegistrationName(p, installations, key)
	registrationInput := ""
	if definitions := p.MCPDefinitions(); len(definitions) == 1 {
		registrationInput = definitions[0].RegistrationNameInput
	}
	if currentRegistration != "" && registrationInput != "" {
		if _, hasValue := values[registrationInput]; !hasValue {
			values[registrationInput] = currentRegistration
		}
	}
	if e := forms.Validate(p.Inputs, values); e == nil {
		preview.Configured = true
	} else {
		preview.ValidationIssues = append(preview.ValidationIssues, e.Error())
	}
	saved, _ := s.Store.Answers(key)
	for _, def := range p.Inputs {
		if def.OptionsFrom != "" {
			def.Options, err = targetInputChoices(pr.Raw, def.OptionsFrom)
			if err != nil {
				return preview, invalid(err)
			}
		}
		v, ok := values[def.Name]
		iv, iok := inherited[def.Name]
		origin := "Capability definition"
		if _, yes := s.Source.PackageDefaults[p.ID][def.Name]; yes {
			origin = "Capability Pack default"
		}
		profileInputs, _ := pr.Raw["inputs"].(map[string]any)
		if _, yes := profileInputs[def.Name]; yes {
			origin = "Profile default"
		}
		editable := pr.InputPolicy[def.Name] != "fixed"
		provenance := origin
		if _, yes := saved[def.Name]; yes && editable {
			provenance = "Saved override"
		}
		if !editable {
			provenance = "Profile fixed"
		} else if def.Name == registrationInput && currentRegistration != "" {
			if _, configured := inherited[def.Name]; !configured {
				if _, savedValue := saved[def.Name]; !savedValue {
					provenance = "registration"
				}
			}
		}
		if def.Name == registrationInput && currentRegistration != "" {
			current := "Currently registered as: " + currentRegistration
			if def.Hint == "" {
				def.Hint = current
			} else {
				def.Hint += " " + current
			}
		}
		preview.Inputs = append(preview.Inputs, viewmodel.SetupInput{Definition: def, Value: v, HasValue: ok, Editable: editable, Provenance: provenance, ProvenancePath: pr.Path, InheritedValue: iv, HasInheritedValue: iok, InheritedOrigin: origin, InheritedPath: pr.Path})
	}
	records, _ := s.Store.Profiles()
	ids := q.ItemIDs
	for _, r := range records {
		if r.Key == key && r.Selection != nil && ids == nil {
			ids = r.Selection.ItemIDs
		}
	}
	selectedItems, selectedItemIDs, err := componentSelection(p, values, ids, q.SkillsOnly)
	if err != nil {
		return preview, invalid(err)
	}
	preview.SelectedItemIDs = selectedItemIDs
	selectedNeedsMCP := false
	selectedNeedsPlugin := false
	for _, item := range selectedItems {
		selectedNeedsMCP = selectedNeedsMCP || len(item.MCPs) > 0
		selectedNeedsPlugin = selectedNeedsPlugin || len(item.Plugins) > 0
	}
	preview.Items, err = catalog.InstallationItems(p, p.Sets)
	if err != nil {
		return preview, err
	}
	destinationIDs := q.DestinationIDs
	hadSelection := false
	for _, record := range records {
		if record.Key == key && record.Selection != nil {
			hadSelection = true
			if destinationIDs == nil {
				destinationIDs = record.Selection.DestinationIDs
			}
		}
	}
	if destinationIDs == nil && !hadSelection {
		settings, e := s.UISettings(ctx)
		if e != nil {
			return preview, e
		}
		for _, id := range strings.Split(settings["default_agents"], ",") {
			if id != "" {
				destinationIDs = append(destinationIDs, id)
			}
		}
		if len(destinationIDs) == 0 && !selectedNeedsMCP && !selectedNeedsPlugin {
			destinationIDs = []string{"generic"}
		}
	}
	achievedRows := installations
	for _, a := range s.adapterRegistry().Adapters() {
		features := a.Features()
		if a.ID() == "generic" && (selectedNeedsMCP || selectedNeedsPlugin) {
			continue
		}
		nativeScope := s.agentScope(a.ID())
		nativeScope.ID = a.ID()
		scope := nativeScope
		recorded, hasRecorded := profileRegistrationConfig(achievedRows, key, a.ID())
		if hasRecorded {
			scope.ConfigPathOverride = recorded.Destination
			if recorded.AgentHome != "" {
				scope.Home = recorded.AgentHome
				scope.ExplicitHome = true
			}
		}
		d, e := a.Detect(ctx, scope)
		hasDetectedConfig := false
		for _, file := range d.ConfigFiles {
			if file.Exists {
				hasDetectedConfig = true
				break
			}
		}
		row := viewmodel.SetupDestination{
			Name: a.Name(), ID: a.ID(), Kind: a.ID(), Home: d.Home, SkillsPath: d.SkillsPath,
			Detection: d.State, Note: d.Reason, Features: features, Selected: slices.Contains(destinationIDs, a.ID()),
			Detected:                 e == nil && d.Installed,
			MCPRegistrationAvailable: e == nil && d.Installed && features.MCPs && d.MCPDisabledReason == "" && (d.CanCreateConfig || hasDetectedConfig),
		}
		if e != nil {
			row.DisabledReason = e.Error()
		} else if selectedNeedsMCP && d.MCPDisabledReason != "" {
			row.DisabledReason = d.MCPDisabledReason
		} else if selectedNeedsMCP && !features.MCPs {
			row.DisabledReason = "This agent does not support MCP registration"
		} else if !d.Installed {
			row.DisabledReason = "Agent is not detected"
			if d.Reason != "" {
				row.DisabledReason += ": " + d.Reason
			}
		} else if selectedNeedsMCP && !d.CanCreateConfig && !hasDetectedConfig {
			row.DisabledReason = "Adapter cannot create the missing MCP config file"
		}
		row.ConfigPath = d.ConfigPath
		if row.ConfigPath == "" {
			for _, file := range d.ConfigFiles {
				if file.Precedence == "effective" {
					row.ConfigPath = file.Path
					break
				}
			}
		}
		if hasRecorded && recorded.Destination != "" {
			defaultPath := ""
			if defaultDetection, detectErr := a.Detect(ctx, nativeScope); detectErr == nil {
				defaultPath = defaultDetection.ConfigPath
			}
			if recorded.AgentHome != "" {
				row.Home = recorded.AgentHome
			}
			if row.Note != "" {
				row.Note += "; "
			}
			row.Note += "Using recorded profile MCP config " + recorded.Destination + " (adapter write path " + row.ConfigPath + "; native path " + defaultPath + ")"
		}
		for _, id := range destinationIDs {
			selectedAdapter, e := s.adapterRegistry().Adapter(id)
			if e == nil && selectedAdapter.ID() == a.ID() {
				row.Selected = true
				if hadSelection {
					scope := s.agentScope(id)
					scope.ID = id
					observation, e := a.Observe(ctx, scope, agents.ObservationRequest{IncludeInventory: true, Key: key, Managed: profileManagedRows(achievedRows, key, id)})
					row.Selected = e == nil && observedSelectionComplete(p, preview.SelectedItemIDs, values, key, observation)
				}
			}
		}
		preview.Destinations = append(preview.Destinations, row)
	}
	knownDestinationIDs := map[string]bool{}
	for _, adapter := range s.adapterRegistry().Adapters() {
		knownDestinationIDs[adapter.ID()] = true
	}
	for _, id := range destinationIDs {
		if knownDestinationIDs[id] {
			continue
		}
		if _, adapterErr := s.adapterRegistry().Adapter(id); adapterErr == nil {
			continue
		}
		preview.Destinations = append(preview.Destinations, viewmodel.SetupDestination{
			Name: id, ID: id, Kind: id, Detection: "unverified",
			Note:           "Historical saved destination; no adapter is available for agent ID " + id,
			DisabledReason: "No adapter can handle saved agent ID " + id + "; this historical destination is excluded",
		})
	}
	for index := range preview.Destinations {
		current := &preview.Destinations[index]
		if current.SkillsPath == "" {
			continue
		}
		for otherIndex := range preview.Destinations {
			other := preview.Destinations[otherIndex]
			if other.ID != current.ID && other.SkillsPath == current.SkillsPath {
				if current.Note != "" {
					current.Note += "; "
				}
				current.Note += "Shares skill directory with " + other.Name + ": " + current.SkillsPath
				break
			}
		}
	}
	return preview, nil
}
func (s *Service) ApplyProfile(ctx context.Context, q ProfileRequest) (out viewmodel.OperationResult, err error) {
	if observer := viewmodel.OperationProgressObserver(ctx); observer != nil && ctx.Value(operationProgressScopeContextKey{}) != true {
		scoped, progress := s.withOperationProgress(ctx, observer)
		return scoped.ApplyProfile(progress, q)
	}
	if ctx.Value(profileOperationLockKey{}) != s.Store {
		err = s.Store.WithLock(ctx, func() error {
			out, err = s.ApplyProfile(context.WithValue(ctx, profileOperationLockKey{}, s.Store), q)
			return err
		})
		if err != nil && len(out.Errors) == 0 && !ordinaryCancellation(err) {
			out.Errors = append(out.Errors, err.Error())
		}
		return out, cancellationError(out.Errors, err)
	}
	out.SavedApplicable = true
	out.Changes = []state.Installation{}
	out.Errors = []string{}
	defer func() {
		if err != nil && !ordinaryCancellation(err) {
			out.Errors = append(out.Errors, err.Error())
		}
	}()
	p, pr, key, err := s.loadProfile(q.Ref)
	if err != nil {
		return out, err
	}
	out.Target = s.Source.ID + "/" + p.ID + " — " + pr.Ref.Name
	p, pr, values, _, err := s.profileValues(p, pr, key, q)
	if err != nil {
		return out, err
	}
	records, err := s.Store.Profiles()
	if err != nil {
		return out, err
	}
	ids := q.ItemIDs
	destIDs := q.DestinationIDs
	local := pr.Path == ""
	for _, r := range records {
		if r.Key == key && r.Selection != nil {
			if ids == nil {
				ids = r.Selection.ItemIDs
			}
			if destIDs == nil {
				destIDs = r.Selection.DestinationIDs
			}
		}
	}
	items, selected, err := componentSelection(p, values, ids, q.SkillsOnly)
	if err != nil {
		return out, invalid(err)
	}
	fixed, err := fixedTargetInputs(p.Inputs, config.Target{Path: pr.Path, InputPolicy: pr.InputPolicy}, pr.Raw)
	if err != nil {
		return out, invalid(err)
	}
	previousGroups, err := s.Store.ActiveInputGroups(key)
	if err != nil {
		return out, err
	}
	activeGroups := effectiveActiveInputGroups(p.Inputs, values, fixed, q.ActiveInputGroups, previousGroups)
	if q.Interactive {
		if s.Options.Editor == nil {
			return out, invalid(errors.New("interactive editor is unavailable"))
		}
		prepare := selectedPrepareMCPs(p, values, items)
		seedInputs := append([]catalog.Input(nil), p.Inputs...)
		if len(prepare) > 0 {
			for i := range seedInputs {
				if isDynamicChoice(seedInputs[i]) {
					seedInputs[i].Required = false
					seedInputs[i].MinItems = nil
				}
			}
		}
		if err = editProfileInputs(ctx, s.Options.Editor, seedInputs, values, fixed); err != nil {
			return out, err
		}
		if len(prepare) > 0 {
			if err = validateProfileValuesForActiveGroups(seedInputs, values, activeGroups); err != nil {
				return out, invalid(err)
			}
			choices := map[string][]catalog.Choice{}
			for _, definition := range prepare {
				cp := p
				cp.MCP = &definition
				cp.MCPs = nil
				result, runErr := (&mcp.ActionRunner{Executor: s.Options.Runner, OnStderr: s.Options.OnStderr}).Run(ctx, cp, mcp.ActionRequest{Action: "prepare", Profile: pr, Inputs: values, StateDir: s.Store.AuthDir(mcpProfileKey(key, p, definition)), Interactive: false})
				if runErr != nil {
					return out, runErr
				}
				for name, options := range result.Choices {
					choices[name] = append(choices[name], options...)
				}
			}
			if len(choices) > 0 {
				choiceInputs := withChoices(p.Inputs, choices)
				if err = editProfileInputs(ctx, s.Options.Editor, choiceInputs, values, fixed); err != nil {
					return out, err
				}
				if err = validateProfileValuesForActiveGroups(choiceInputs, values, activeGroups); err != nil {
					return out, invalid(err)
				}
			}
		}
	}
	if err = validateProfileValuesForActiveGroups(p.Inputs, values, activeGroups); err != nil {
		return out, invalid(err)
	}
	items, selected, err = componentSelection(p, values, ids, q.SkillsOnly)
	if err != nil {
		return out, invalid(err)
	}
	skills, mcps, plugins := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, item := range items {
		for _, v := range item.Skills {
			skills[v] = true
		}
		for _, v := range item.MCPs {
			mcps[v] = true
		}
		for _, v := range item.Plugins {
			plugins[v] = true
		}
	}
	authenticationRequired := map[string]bool{}
	authenticationComplete := map[string]bool{}
	for _, definition := range p.MCPDefinitions() {
		if mcps[definition.Name] {
			if _, hasAuth := definition.Actions["authenticate"]; hasAuth {
				authenticationRequired[definition.Name] = true
			}
		}
	}
	if len(authenticationRequired) == 0 {
		out.AuthenticationStatus = viewmodel.AuthenticationNotRequired
	} else {
		out.AuthenticationStatus = viewmodel.AuthenticationNotConfirmed
	}
	if destIDs == nil {
		settings, e := s.UISettings(ctx)
		if e != nil {
			return out, e
		}
		for _, id := range strings.Split(settings["default_agents"], ",") {
			if id != "" {
				destIDs = append(destIDs, id)
			}
		}
	}
	if len(items) > 0 && len(destIDs) == 0 {
		return out, invalid(errors.New("select an agent destination"))
	}
	adapters := map[string]agents.Adapter{}
	for _, id := range destIDs {
		a, e := s.adapterRegistry().Adapter(id)
		if e != nil {
			return out, invalid(e)
		}
		d, e := a.Detect(ctx, s.agentScope(id))
		if e != nil {
			return out, e
		}
		if len(mcps) > 0 && d.MCPDisabledReason != "" {
			return out, invalid(errors.New(d.MCPDisabledReason))
		}
		if !d.Installed {
			return out, invalid(fmt.Errorf("agent %s is not detected: %s", id, d.Reason))
		}
		f := a.Features()
		if (len(skills) > 0 && !f.Skills) || (len(mcps) > 0 && !f.MCPs) {
			return out, invalid(fmt.Errorf("agent %s cannot install every selected capability component", id))
		}
		for _, plugin := range p.Plugins {
			if plugins[plugin.Name] && !slices.Contains(f.PluginFormats, plugin.Format) {
				return out, invalid(fmt.Errorf("agent %s does not support plugin format %s", id, plugin.Format))
			}
		}
		adapters[id] = a
	}
	if err = s.validateRegistrationNames(p, packageMCPProfiles(p, values), key, values, nil); err != nil {
		return out, invalid(err)
	}
	for _, definition := range p.MCPDefinitions() {
		if !mcps[definition.Name] {
			continue
		}
		if _, e := profileRegistrationHeaders(definition, values); e != nil {
			return out, invalid(e)
		}
		if endpoint := q.ExternalURLs[definition.Name]; endpoint != "" {
			if e := validateEndpointURL(endpoint); e != nil {
				return out, invalid(e)
			}
		}
	}
	for name := range q.ExternalURLs {
		if !mcps[name] {
			return out, invalid(fmt.Errorf("external endpoint supplied for unselected MCP %q", name))
		}
	}
	out.Step = "save"
	reportOperationStep(ctx, out.Step)
	previousAnswers, err := s.Store.Answers(key)
	if err != nil {
		return out, err
	}
	if err = validateActiveInputGroups(p.Inputs, q.ActiveInputGroups); err != nil {
		return out, invalid(err)
	}
	err = func() error {
		if e := s.saveAnswersWithActiveGroups(key, p, values, q.SkillsOnly, q.ResetInputs, activeGroups); e != nil {
			return e
		}
		return s.Store.RecordProfile(state.ProfileRecord{Key: key, Name: pr.Ref.Name, Local: local, Selection: &state.ProfileSelection{ItemIDs: selected, DestinationIDs: destIDs}})
	}()
	if err != nil {
		return out, err
	}
	out.Saved = true
	stages := map[string]string{}
	pluginStages := map[string]string{}
	out.Step = "generate"
	reportOperationStep(ctx, out.Step)
	renderer := render.Renderer{Generator: &render.Generator{Executor: s.Options.Runner, OnStderr: s.Options.OnStderr}}
	for _, skill := range p.SkillDefinitions() {
		if !skills[skill.Name] {
			continue
		}
		stage, e := renderer.StageSkill(ctx, p, skill, values, pr, s.Store.GeneratedDir(key))
		if e != nil {
			return out, e
		}
		published := filepath.Join(filepath.Dir(stage), "output-"+strings.TrimPrefix(filepath.Base(stage), ".aact-stage-"))
		if e = render.Publish(stage, published); e != nil {
			os.RemoveAll(stage)
			return out, e
		}
		stages[skill.Name] = published
	}
	for _, plugin := range p.Plugins {
		if plugins[plugin.Name] {
			stage, e := render.StagePlugin(ctx, p, plugin, s.Store.GeneratedDir(key))
			if e != nil {
				return out, e
			}
			pluginStages[plugin.Name] = stage
		}
	}
	urls := map[string]string{}
	for _, definition := range p.MCPDefinitions() {
		if !mcps[definition.Name] {
			continue
		}
		child := mcpProfileKey(key, p, definition)
		if url := q.ExternalURLs[definition.Name]; url != "" {
			if e := validateEndpointURL(url); e != nil {
				return out, invalid(e)
			}
			urls[definition.Name] = url
			continue
		}
		cp := p
		cp.MCP = &definition
		cp.MCPs = nil
		if _, yes := definition.Actions["authenticate"]; yes {
			changes := changedActiveInputGroups(p.Inputs, previousGroups, activeGroups)
			if len(changes) > 0 {
				if e := s.Store.UpdatePendingAuthInputGroups(key, child.ID(), changes); e != nil {
					return out, e
				}
			}
			pendingGroups, e := s.Store.PendingAuthInputGroups(key, child.ID())
			if e != nil {
				return out, e
			}
			if needsProfileAuthentication(s, cp, child, values, previousAnswers, pendingGroups, activeGroups) {
				out.Step = "authenticate"
				reportOperationStep(ctx, out.Step)
				result, e := (&mcp.ActionRunner{Executor: s.Options.Runner, OnStderr: s.Options.OnStderr}).Run(ctx, cp, mcp.ActionRequest{Action: "authenticate", Profile: pr, Inputs: values, StateDir: s.Store.AuthDir(child), Interactive: q.Interactive})
				if e != nil {
					return out, e
				}
				if result.AuthRequired {
					// Older providers may return the legacy auth-required marker
					// without a diagnostic. Preserve that flow while retaining the
					// pending method transition; always stop before registration can
					// imply success.
					if strings.TrimSpace(result.Diagnostic) != "" {
						return out, errors.New(authenticationRequiredMessage(cp.ID, result.Diagnostic))
					}
					out.Errors = append(out.Errors, authenticationRequiredMessage(cp.ID, ""))
					return out, nil
				} else {
					authenticationComplete[definition.Name] = true
					if e = s.Store.ClearPendingAuthInputGroups(key, child.ID(), activeGroups); e != nil {
						return out, e
					}
					remaining, pendingErr := s.Store.PendingAuthInputGroups(key, child.ID())
					if pendingErr != nil {
						return out, pendingErr
					}
					if len(authenticationComplete) == len(authenticationRequired) && len(remaining) == 0 {
						out.AuthenticationStatus = viewmodel.AuthenticationComplete
					} else if len(remaining) > 0 {
						out.AuthenticationStatus = viewmodel.AuthenticationNotConfirmed
						out.Errors = append(out.Errors, authenticationRequiredMessage(cp.ID, ""))
						return out, nil
					}
				}
			}
		}
		out.Step = "start"
		reportOperationStep(ctx, out.Step)
		instance, e := s.startConfigurationProfile(ctx, cp, pr, child, values, q.Interactive)
		if e != nil {
			var failure operationFailure
			if errors.As(e, &failure) {
				out.Step = failure.step
			}
			var authRequired authenticationRequiredFailure
			if errors.As(e, &authRequired) {
				out.AuthenticationStatus = viewmodel.AuthenticationNotConfirmed
			}
			return out, e
		}
		urls[definition.Name] = instance.URL
	}
	record := func(row state.Installation) error {
		out.Changes = append(out.Changes, row)
		if e := s.recordRegistration(row); e != nil {
			out.Step = "record"
			return e
		}
		return nil
	}
	// Remove only bindings previously owned by this logical capability/profile.
	old, err := s.Store.Installations()
	if err != nil {
		return out, err
	}
	// Marketplace ownership outlives an individual plugin. Remove installed
	// plugin rows first, then remove an otherwise-empty owned marketplace.
	slices.SortStableFunc(old, func(a, b state.Installation) int {
		if a.Component == "plugin-marketplace" && b.Component != "plugin-marketplace" {
			return 1
		}
		if b.Component == "plugin-marketplace" && a.Component != "plugin-marketplace" {
			return -1
		}
		return 0
	})
	for _, row := range old {
		if !sameCapabilityKey(row.Key, key) || row.Component == "runtime" {
			continue
		}
		want := slices.Contains(destIDs, row.AgentID)
		switch row.Component {
		case "skill":
			want = want && skills[filepath.Base(row.Destination)]
		case "mcp":
			found := false
			for _, d := range p.MCPDefinitions() {
				if mcps[d.Name] && mcpProfileKey(key, p, d) == row.Key {
					found = true
				}
			}
			want = want && found
		case "plugin":
			found := false
			for _, d := range p.Plugins {
				if plugins[d.Name] && strings.HasPrefix(row.ReleaseID, d.Name+"@") {
					found = true
				}
			}
			want = want && found
		case "plugin-marketplace":
			found := false
			for _, d := range p.Plugins {
				if plugins[d.Name] && row.ReleaseID == "aact-"+key.ID()[:12]+"-"+d.Name {
					found = true
				}
			}
			want = want && found
		default:
			continue
		}
		if want {
			continue
		}
		out.Step = "remove"
		reportOperationStep(ctx, out.Step)
		if e := s.removeProfileBinding(ctx, row); e != nil {
			return out, e
		}
	}
	for _, id := range destIDs {
		a := adapters[id]
		scope := s.agentScope(id)
		for _, skill := range p.SkillDefinitions() {
			if !skills[skill.Name] {
				continue
			}
			out.Step = "skill"
			reportOperationStep(ctx, out.Step)
			row, e := a.(agents.SkillManager).InstallSkill(ctx, scope, agents.SkillRequest{Key: key, Package: p, Skill: skill, StagedDir: stages[skill.Name]})
			if e != nil {
				return out, e
			}
			if e = record(row); e != nil {
				return out, e
			}
		}
		for _, d := range p.MCPDefinitions() {
			if !mcps[d.Name] {
				continue
			}
			out.Step = "register"
			reportOperationStep(ctx, out.Step)
			child := mcpProfileKey(key, p, d)
			name, e := declaredRegistrationName(d, child, values)
			if e != nil {
				return out, invalid(e)
			}
			headers, e := profileRegistrationHeaders(d, values)
			if e != nil {
				return out, invalid(e)
			}
			for _, r := range old {
				if r.Component == "mcp" && r.Key == child && r.AgentID == id && r.RegistrationName != name {
					if e = s.removeProfileBinding(ctx, r); e != nil {
						return out, e
					}
				}
			}
			registrationResult, e := a.(agents.MCPManager).Register(ctx, scope, agents.MCPRequest{Key: child, Registration: agents.Registration{Name: name, URL: urls[d.Name], Transport: d.Transport, TimeoutMS: registrationTimeoutMS(&d), Headers: headers}})
			for _, removed := range registrationResult.Removed {
				out.Changes = append(out.Changes, removed)
				if removeErr := s.removeRegistration(removed); removeErr != nil {
					return out, removeErr
				}
			}
			for _, effect := range registrationResult.Effects {
				if recordErr := record(effect); recordErr != nil {
					return out, recordErr
				}
			}
			if e != nil {
				return out, e
			}
			row := registrationResult.Installation
			row.ExternalRegistration = q.ExternalURLs[d.Name] != ""
			if e = record(row); e != nil {
				return out, e
			}
		}
		for _, plugin := range p.Plugins {
			if !plugins[plugin.Name] {
				continue
			}
			out.Step = "plugin"
			reportOperationStep(ctx, out.Step)
			marketplaceConfigured := false
			var existingPlugin *state.Installation
			for _, existing := range old {
				if existing.Key == key && existing.AgentID == id && existing.Component == "plugin-marketplace" && existing.ReleaseID == "aact-"+key.ID()[:12]+"-"+plugin.Name {
					marketplaceConfigured = true
				}
				if existing.Key == key && existing.AgentID == id && existing.Component == "plugin" && strings.HasPrefix(existing.ReleaseID, plugin.Name+"@") {
					copy := existing
					existingPlugin = &copy
					marketplaceConfigured = true
				}
			}
			pluginResult, e := a.(agents.PluginManager).InstallPlugin(ctx, scope, agents.PluginRequest{Key: key, Plugin: plugin, StagedDir: pluginStages[plugin.Name], MarketplaceConfigured: marketplaceConfigured, Existing: existingPlugin})
			for _, removed := range pluginResult.Removed {
				out.Changes = append(out.Changes, removed)
				if removeErr := s.removeRegistration(removed); removeErr != nil {
					return out, removeErr
				}
			}
			for _, effect := range pluginResult.Effects {
				if recordErr := record(effect); recordErr != nil {
					return out, recordErr
				}
			}
			if e != nil {
				return out, e
			}
			if e = record(pluginResult.Installation); e != nil {
				return out, e
			}
		}
	}
	out.Step = "complete"
	return out, nil
}
func (s *Service) removeProfileBinding(ctx context.Context, row state.Installation) error {
	a, err := s.registrationAdapterFor(row.AgentID, row.AgentKind)
	if err != nil {
		return err
	}
	scope := agents.Scope{ID: row.AgentID, Home: row.AgentHome, ConfigPathOverride: row.Destination, ExplicitHome: true}
	switch row.Component {
	case "skill":
		scope.ConfigPathOverride = ""
		manager, ok := a.(agents.SkillManager)
		if !ok {
			return errors.New("adapter cannot remove skill")
		}
		err = manager.RemoveSkill(ctx, scope, row)
	case "mcp":
		manager, ok := a.(agents.MCPManager)
		if !ok {
			return errors.New("adapter cannot remove MCP")
		}
		err = manager.Unregister(ctx, scope, row)
	case "plugin":
	case "plugin-marketplace":
		scope.ConfigPathOverride = ""
		manager, ok := a.(agents.PluginManager)
		if !ok {
			return errors.New("adapter cannot remove plugin")
		}
		err = manager.RemovePlugin(ctx, scope, row)
	}
	if err != nil {
		return err
	}
	return s.removeRegistration(row)
}

func validateEndpointURL(value string) error {
	u, err := url.Parse(value)
	if err != nil {
		return err
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("MCP endpoint %q must be an absolute HTTP(S) URL", value)
	}
	return nil
}

func (s *Service) RemoveProfile(ctx context.Context, q ProfileRequest) (out viewmodel.OperationResult, err error) {
	if ctx.Value(profileOperationLockKey{}) != s.Store {
		err = s.Store.WithLock(ctx, func() error {
			out, err = s.RemoveProfile(context.WithValue(ctx, profileOperationLockKey{}, s.Store), q)
			return err
		})
		return out, err
	}
	p, pr, key, err := s.loadProfile(q.Ref)
	if err != nil {
		return out, err
	}
	out.Target = s.Source.ID + "/" + p.ID + " — " + pr.Ref.Name
	out.Step = "remove"
	rows, err := s.Store.Installations()
	if err != nil {
		return out, err
	}
	for _, row := range rows {
		if !sameCapabilityKey(row.Key, key) || (q.DestinationIDs != nil && !slices.Contains(q.DestinationIDs, row.AgentID)) {
			continue
		}
		if row.Component == "runtime" {
			continue
		}
		if err = s.removeProfileBinding(ctx, row); err != nil {
			out.Errors = append(out.Errors, err.Error())
			return out, err
		}
		out.Changes = append(out.Changes, row)
	}
	if q.DestinationIDs == nil {
		for _, row := range rows {
			if row.Component == "runtime" && sameCapabilityKey(row.Key, key) && !row.ExternalRegistration {
				out.Step = "stop"
				if err = s.Options.Runtime.Stop(ctx, row.Key); err != nil {
					return out, err
				}
			}
		}
	}
	out.Step = "complete"
	return out, nil
}
func (s *Service) RunProfileMCP(ctx context.Context, action string, q ProfileRequest, mcpName string) (out Result, err error) {
	if ctx.Value(profileOperationLockKey{}) != s.Store && action != "list" && action != "status" && action != "logs" {
		err = s.Store.WithLock(ctx, func() error {
			out, err = s.RunProfileMCP(context.WithValue(ctx, profileOperationLockKey{}, s.Store), action, q, mcpName)
			return err
		})
		return out, err
	}
	if action == "list" || action == "status" {
		out.Instances, err = s.Options.Runtime.List(ctx)
		return out, err
	}
	p, pr, key, err := s.loadProfile(q.Ref)
	if err != nil {
		return out, err
	}
	definition, err := selectMCPProfile(p, mcpName)
	if err != nil {
		return out, invalid(err)
	}
	child := mcpProfileKey(key, p, definition)
	out.Target = s.Source.ID + "/" + p.ID + " — " + pr.Ref.Name + " / " + definition.Name
	out.Step = action
	switch action {
	case "stop":
		err = s.Options.Runtime.Stop(ctx, child)
	case "logs":
		var r io.ReadCloser
		r, err = s.Options.Runtime.Logs(ctx, child)
		if err == nil {
			defer r.Close()
			b, e := io.ReadAll(io.LimitReader(r, 1<<20))
			out.Logs = string(b)
			err = e
		}
	case "start", "prepare", "authenticate":
		p, pr, values, _, e := s.profileValues(p, pr, key, q)
		if e != nil {
			return out, e
		}
		definition, e = selectMCPProfile(p, mcpName)
		if e != nil {
			return out, invalid(e)
		}
		child = mcpProfileKey(key, p, definition)
		if e = forms.Validate(p.Inputs, values); e != nil {
			return out, invalid(e)
		}
		cp := p
		cp.MCP = &definition
		cp.MCPs = nil
		if action == "start" {
			instance, e := s.startConfigurationProfile(ctx, cp, pr, child, values, q.Interactive)
			if e != nil {
				return out, e
			}
			out.Instances = []mcp.Instance{instance}
		} else {
			var result mcp.ActionResult
			result, err = (&mcp.ActionRunner{Executor: s.Options.Runner, OnStderr: s.Options.OnStderr}).Run(ctx, cp, mcp.ActionRequest{Action: action, Profile: pr, Inputs: values, StateDir: s.Store.AuthDir(child), Interactive: q.Interactive})
			if err == nil && result.AuthRequired {
				err = errors.New(authenticationRequiredMessage(cp.ID, result.Diagnostic))
			}
		}
	default:
		err = invalid(fmt.Errorf("unknown MCP action %q", action))
	}
	return out, err
}

func profileManagedRows(rows []state.Installation, key state.Key, id string) []state.Installation {
	result := []state.Installation{}
	for _, r := range rows {
		if r.AgentID == id && sameCapabilityKey(r.Key, key) {
			result = append(result, r)
		}
	}
	return result
}
func observedSelectionComplete(p catalog.Package, ids []string, values map[string]any, key state.Key, o agents.Observation) bool {
	items, _, err := componentSelection(p, values, ids, false)
	if err != nil || len(items) == 0 {
		return false
	}
	installed := func(kind, name string) bool {
		for _, c := range o.Components {
			if c.Kind == kind && c.Status == "installed" && (c.Name == name || c.RegistrationName == name) {
				return true
			}
		}
		return false
	}
	for _, item := range items {
		for _, name := range item.Skills {
			if !installed("skill", name) {
				return false
			}
		}
		for _, name := range item.MCPs {
			for _, d := range p.MCPDefinitions() {
				if d.Name == name {
					registration, e := declaredRegistrationName(d, mcpProfileKey(key, p, d), values)
					if e != nil || !installed("mcp", registration) {
						return false
					}
				}
			}
		}
		for _, name := range item.Plugins {
			present := false
			for _, c := range o.Components {
				present = present || (c.Kind == "plugin" && c.Status == "installed" && (c.Name == name || strings.HasPrefix(c.Name, name+"@")))
			}
			if !present {
				return false
			}
		}
	}
	return true
}

func needsProfileAuthentication(s *Service, p catalog.Package, key state.Key, values, previous map[string]any, pendingGroups, activeGroups map[string]string) bool {
	status, _ := s.credentialObservation(p, key)
	if status != "present" {
		return true
	}
	for group, method := range activeGroups {
		if method != "" && pendingGroups[group] == method {
			return true
		}
	}
	return hasSubmittedAuthentication(s, p, key, values, previous)
}

func changedActiveInputGroups(defs []catalog.Input, previous, current map[string]string) map[string]string {
	changes := map[string]string{}
	groups := map[string]bool{}
	for _, def := range defs {
		if def.ExclusiveGroup != "" {
			groups[def.ExclusiveGroup] = true
		}
	}
	for group := range groups {
		if previous[group] != current[group] {
			changes[group] = current[group]
		}
	}
	return changes
}
