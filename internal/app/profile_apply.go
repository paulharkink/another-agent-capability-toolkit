package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/agents"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/forms"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/mcp"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/render"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type AdapterProvider interface {
	Adapter(string) (agents.Adapter, error)
	Adapters() []agents.Adapter
}
type ProfileRequest struct {
	Ref                                  config.ProfileRef
	Inputs                               map[string]any
	ResetInputs, ItemIDs, DestinationIDs []string
	Interactive, SkillsOnly              bool
	ExternalURLs                         map[string]string
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
	if ref.Name == "" || ref.Name == "." || ref.Name == ".." || strings.ContainsAny(ref.Name, "/\\\n\x00") {
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
func (s *Service) profileValues(p catalog.Package, pr config.Profile, key state.Key, q ProfileRequest) (map[string]any, map[string]any, error) {
	known := map[string]bool{}
	for _, d := range p.Inputs {
		known[d.Name] = true
	}
	for name := range q.Inputs {
		if !known[name] {
			return nil, nil, invalid(fmt.Errorf("unknown input %q", name))
		}
	}
	saved, err := s.Store.Answers(key)
	if err != nil {
		return nil, nil, err
	}
	for _, name := range q.ResetInputs {
		if !known[name] {
			return nil, nil, invalid(fmt.Errorf("unknown reset input %q", name))
		}
		delete(saved, name)
	}
	inherited, err := forms.ResolvePartial(p.Inputs, s.Source.PackageDefaults[p.ID], pr.Raw)
	if err != nil {
		return nil, nil, invalid(err)
	}
	fixed, err := fixedTargetInputs(p.Inputs, config.Target{Path: pr.Path, InputPolicy: pr.InputPolicy}, pr.Raw)
	if err != nil {
		return nil, nil, invalid(err)
	}
	input := map[string]any{}
	for k, v := range saved {
		if _, locked := fixed[k]; !locked {
			input[k] = v
		}
	}
	for k, v := range q.Inputs {
		if _, locked := fixed[k]; !locked {
			input[k] = v
		}
	}
	values, err := forms.ResolvePartial(p.Inputs, inherited, input, fixed)
	return values, inherited, invalidIf(err)
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
	values, inherited, err := s.profileValues(p, pr, key, q)
	preview := viewmodel.SetupPreview{Key: key, PackageName: p.Name, PackRoot: s.Source.Root, ProfilePath: pr.Path, ProfileOrigin: "local", MCP: p.HasMCP(), MCPDefinitions: p.MCPDefinitions()}
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
	if err != nil {
		preview.ValidationIssues = append(preview.ValidationIssues, err.Error())
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
	_, preview.SelectedItemIDs, err = componentSelection(p, values, ids, q.SkillsOnly)
	if err != nil {
		return preview, invalid(err)
	}
	preview.Items, err = catalog.InstallationItems(p, p.Sets)
	if err != nil {
		return preview, err
	}
	for _, a := range s.adapterRegistry().Adapters() {
		features := a.Features()
		if p.HasMCP() && !features.MCPs {
			continue
		}
		d, e := a.Detect(ctx, s.agentScope(a.ID()))
		row := viewmodel.SetupDestination{ID: a.ID(), Kind: a.ID(), Detection: d.State, Note: d.Reason, Features: features, Selected: slices.Contains(q.DestinationIDs, a.ID())}
		if e != nil {
			row.DisabledReason = e.Error()
		} else if !d.Installed {
			row.DisabledReason = "Agent is not detected: " + d.Reason
		}
		for _, f := range d.ConfigFiles {
			if f.Precedence == "effective" || row.ConfigPath == "" {
				row.ConfigPath = f.Path
			}
		}
		preview.Destinations = append(preview.Destinations, row)
	}
	return preview, nil
}
func (s *Service) ApplyProfile(ctx context.Context, q ProfileRequest) (out viewmodel.OperationResult, err error) {
	if observer := viewmodel.OperationProgressObserver(ctx); observer != nil && ctx.Value(operationProgressScopeContextKey{}) != true {
		scoped, progress := s.withOperationProgress(ctx, observer)
		return scoped.ApplyProfile(progress, q)
	}
	out.SavedApplicable = true
	out.Changes = []state.Installation{}
	out.Errors = []string{}
	defer func() {
		if err != nil {
			out.Errors = append(out.Errors, err.Error())
		}
	}()
	p, pr, key, err := s.loadProfile(q.Ref)
	if err != nil {
		return out, err
	}
	out.Target = s.Source.ID + "/" + p.ID + " — " + pr.Ref.Name
	values, _, err := s.profileValues(p, pr, key, q)
	if err != nil {
		return out, err
	}
	if q.Interactive && s.Options.Editor != nil {
		values, err = s.Options.Editor(ctx, p.Inputs, values)
		if err != nil {
			return out, err
		}
		fixed, e := fixedTargetInputs(p.Inputs, config.Target{Path: pr.Path, InputPolicy: pr.InputPolicy}, pr.Raw)
		if e != nil {
			return out, e
		}
		values = withFixed(values, fixed)
	}
	if err = forms.Validate(p.Inputs, values); err != nil {
		return out, invalid(err)
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
	for name := range q.ExternalURLs {
		if !mcps[name] {
			return out, invalid(fmt.Errorf("external endpoint supplied for unselected MCP %q", name))
		}
	}
	out.Step = "save"
	reportOperationStep(ctx, out.Step)
	err = s.Store.WithLock(ctx, func() error {
		if e := s.saveAnswersWithReset(key, p, values, q.SkillsOnly, q.ResetInputs); e != nil {
			return e
		}
		return s.Store.RecordProfile(state.ProfileRecord{Key: key, Name: pr.Ref.Name, Local: local, Selection: &state.ProfileSelection{ItemIDs: selected, DestinationIDs: destIDs}})
	})
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
			out.Step = "authenticate"
			reportOperationStep(ctx, out.Step)
			_, e := (&mcp.ActionRunner{Executor: s.Options.Runner, OnStderr: s.Options.OnStderr}).Run(ctx, cp, mcp.ActionRequest{Action: "authenticate", Profile: pr, Inputs: values, StateDir: s.Store.AuthDir(child), Interactive: q.Interactive})
			if e != nil {
				return out, e
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
			row, e := a.(agents.MCPManager).Register(ctx, scope, agents.MCPRequest{Key: child, Registration: agents.Registration{Name: name, URL: urls[d.Name], Transport: d.Transport, TimeoutMS: registrationTimeoutMS(&d), Headers: headers}})
			if e != nil {
				return out, e
			}
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
			row, e := a.(agents.PluginManager).InstallPlugin(ctx, scope, agents.PluginRequest{Key: key, Plugin: plugin, StagedDir: pluginStages[plugin.Name]})
			if e != nil {
				return out, e
			}
			if e = record(row); e != nil {
				return out, e
			}
		}
	}
	out.Step = "complete"
	return out, nil
}
func (s *Service) removeProfileBinding(ctx context.Context, row state.Installation) error {
	a, err := s.adapterRegistry().Adapter(row.AgentKind)
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
