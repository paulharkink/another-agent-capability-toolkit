package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/agents"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/forms"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/install"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/mcp"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

type operationProgressContextKey struct{}
type operationProgressScopeContextKey struct{}

type operationProgressScope struct {
	mu       sync.Mutex
	step     string
	observer func(viewmodel.OperationProgress)
}

func (p *operationProgressScope) setStep(step string) {
	if p == nil || step == "" || p.observer == nil {
		return
	}
	p.mu.Lock()
	p.step = step
	p.mu.Unlock()
	p.observer(viewmodel.OperationProgress{Step: step})
}

func (p *operationProgressScope) output(output []byte) {
	if p == nil || len(output) == 0 || p.observer == nil {
		return
	}
	p.mu.Lock()
	step := p.step
	p.mu.Unlock()
	p.observer(viewmodel.OperationProgress{Step: step, Output: string(output)})
}

func reportOperationStep(ctx context.Context, step string) {
	if progress, _ := ctx.Value(operationProgressContextKey{}).(*operationProgressScope); progress != nil {
		progress.setStep(step)
	}
}

func (s *Service) withOperationProgress(ctx context.Context, observer func(viewmodel.OperationProgress)) (*Service, context.Context) {
	progress := &operationProgressScope{observer: observer}
	options := s.Options
	previousOnStderr := options.OnStderr
	options.OnStderr = func(output []byte) {
		if previousOnStderr != nil {
			previousOnStderr(output)
		}
		progress.output(output)
	}
	if runtime, ok := options.Runtime.(*mcp.Runtime); ok {
		runtimeCopy := *runtime
		previousRuntimeOnStderr := runtimeCopy.OnStderr
		runtimeCopy.OnStderr = func(output []byte) {
			if previousRuntimeOnStderr != nil {
				previousRuntimeOnStderr(output)
			}
			progress.output(output)
		}
		options.Runtime = &runtimeCopy
	}
	scoped := &Service{Source: s.Source, Store: s.Store, Options: options}
	ctx = context.WithValue(ctx, operationProgressContextKey{}, progress)
	ctx = context.WithValue(ctx, operationProgressScopeContextKey{}, true)
	return scoped, ctx
}

// UISetupPreview resolves a form without validating missing required answers or
// changing installed state. Values retain the source of their winning layer.
func (s *Service) UISetupPreview(ctx context.Context, q viewmodel.SetupRequest) (viewmodel.SetupPreview, error) {
	if err := ctx.Err(); err != nil {
		return viewmodel.SetupPreview{}, err
	}
	if q.SourceID != "" && q.SourceID != s.Source.ID {
		source, err := s.forSource(q.SourceID)
		if err != nil {
			return viewmodel.SetupPreview{}, invalid(err)
		}
		q.SourceID = source.Source.ID
		return source.UISetupPreview(ctx, q)
	}
	p, err := s.packageByID(q.PackageID)
	if err != nil {
		return viewmodel.SetupPreview{}, err
	}
	if q.Environment == "" && q.Target == "default" {
		q.Target = ""
	}
	if (q.Environment == "") != (q.Target == "") {
		return viewmodel.SetupPreview{}, invalid(errors.New("environment and target must be specified together"))
	}
	key := s.key(p.ID, q.Environment, q.Target)
	target := config.Target{Environment: q.Environment, Name: key.Target, Raw: map[string]any{}}
	if q.Environment != "" {
		target, err = config.LoadTarget(s.Source, p.ID, q.Environment, q.Target)
		if err != nil {
			return viewmodel.SetupPreview{}, invalid(err)
		}
	}
	saved, err := s.Store.Answers(key)
	if err != nil {
		return viewmodel.SetupPreview{}, err
	}
	attempted, err := s.Store.HasAnswers(key)
	if err != nil {
		return viewmodel.SetupPreview{}, err
	}
	profiles, err := s.Store.Profiles()
	if err != nil {
		return viewmodel.SetupPreview{}, err
	}
	for _, profile := range profiles {
		if profile.Key == key {
			attempted = true
			break
		}
	}
	installations, err := s.Store.Installations()
	if err != nil {
		return viewmodel.SetupPreview{}, err
	}
	installed := map[string]map[string]bool{}
	for _, row := range installations {
		if !sameCapabilityKey(row.Key, key) {
			continue
		}
		attempted = true
		if installed[row.AgentID] == nil {
			installed[row.AgentID] = map[string]bool{}
		}
		installed[row.AgentID][row.Component] = true
	}
	defaultValues := map[string]any{}
	for _, def := range p.Inputs {
		if def.Default != nil {
			defaultValues[def.Name] = def.Default
		}
	}
	paths := []string{
		filepath.Join(p.Dir, "package.toml"),
		s.Source.ManifestPath,
		target.Path,
		filepath.Join(s.Store.Root(), "answers", key.ID()+".json"),
	}
	if paths[1] == "" {
		paths[1] = filepath.Join(s.Source.Root, "aact.toml")
	}
	layers := []map[string]any{defaultValues, s.Source.PackageDefaults[p.ID], target.Raw, saved}
	for i := range layers {
		layers[i], err = config.ResolveInputPaths(p.Inputs, layers[i], paths[i])
		if err != nil {
			return viewmodel.SetupPreview{}, invalid(err)
		}
	}
	fixed, err := fixedTargetInputs(p.Inputs, target, layers[2])
	if err != nil {
		return viewmodel.SetupPreview{}, invalid(err)
	}
	// Resolve each layer without declaration defaults. This preserves the same
	// precedence and config_key conflict checks as Install while identifying origin.
	defs := append([]catalog.Input(nil), p.Inputs...)
	for i := range defs {
		defs[i].Default = nil
	}
	credentialState, credentialNote := s.credentialObservation(p, key)
	preview := viewmodel.SetupPreview{
		Key: key, PackageName: p.Name, SourceRoot: s.Source.Root, TargetPath: target.Path, Configured: attempted, MCP: p.HasMCP(),
		MCPDefinitions:  p.MCPDefinitions(),
		CredentialState: credentialState, CredentialNote: credentialNote,
	}
	if p.UI != nil {
		preview.HasManifestUI = true
		preview.Sections = append([]catalog.Section(nil), p.UI.Sections...)
	}
	if target.Path != "" {
		b, readErr := os.ReadFile(target.Path)
		if readErr != nil {
			return viewmodel.SetupPreview{}, invalid(readErr)
		}
		preview.TargetTOML = string(b)
	}
	values := map[string]any{}
	origins := []string{"package", "source", "target", "saved"}
	provenance := map[string]string{}
	provenancePaths := map[string]string{}
	for i, layer := range layers {
		resolved, resolveErr := forms.ResolvePartial(defs, layer)
		if resolveErr != nil {
			return viewmodel.SetupPreview{}, invalid(resolveErr)
		}
		for name, value := range resolved {
			values[name] = value
			provenance[name] = origins[i]
			provenancePaths[name] = paths[i]
		}
	}
	for name, value := range fixed {
		values[name] = value
		provenance[name] = "target"
		provenancePaths[name] = target.Path
	}
	for _, def := range p.Inputs {
		if def.OptionsFrom != "" {
			def.Options, err = targetInputChoices(target.Raw, def.OptionsFrom)
			if err != nil {
				return viewmodel.SetupPreview{}, invalid(fmt.Errorf("input %s: %w", def.Name, err))
			}
		}
		value, present := values[def.Name]
		origin := provenance[def.Name]
		if !present {
			origin = "unset"
		}
		_, locked := fixed[def.Name]
		preview.Inputs = append(preview.Inputs, viewmodel.SetupInput{Definition: def, Value: value, HasValue: present, Provenance: origin, ProvenancePath: provenancePaths[def.Name], Editable: !locked})
	}
	ids, err := s.UIAgents(ctx)
	if err != nil {
		return viewmodel.SetupPreview{}, err
	}
	defaultAgents := map[string]bool{}
	if p.HasMCP() {
		settings, settingsErr := s.UISettings(ctx)
		if settingsErr != nil {
			return viewmodel.SetupPreview{}, settingsErr
		}
		for _, id := range strings.Split(settings["default_agents"], ",") {
			id = strings.TrimSpace(id)
			if id != "" && id != "all" {
				defaultAgents[id] = true
			}
		}
	}
	for _, id := range ids {
		if id == "all" && (p.Skill == nil || p.HasMCP()) {
			continue
		}
		if p.HasMCP() && isManualAgentID(id) {
			continue
		}
		env, envErr := s.uiEnvironment(id, key)
		if envErr != nil {
			return viewmodel.SetupPreview{}, envErr
		}
		var adapterErr error
		if p.HasMCP() {
			_, adapterErr = agents.For(env.Kind, s.Options.Runner)
		}
		writePath := env.ConfigPath
		if p.HasMCP() && adapterErr == nil {
			var pathErr error
			writePath, pathErr = agents.ResolveConfigWritePath(env)
			if pathErr != nil {
				return viewmodel.SetupPreview{}, pathErr
			}
		}
		detection := "shared"
		disabledReason := ""
		probe := s.discoveryProbe()
		if id != "all" {
			discovery := agents.DiscoverAgent(ctx, env.Kind, probe)
			detection = discovery.Detection
			if p.HasMCP() {
				disabledReason = mcpDestinationDisabledReason(ctx, env.Kind, env.ConfigPath, discovery, adapterErr, probe)
			}
		}
		path := env.SkillsDir
		if p.HasMCP() && adapterErr == nil {
			path = writePath
		}
		selected := id == "all" || defaultAgents[id]
		if attempted {
			selected = p.Skill == nil || installed[id]["skill"]
			if p.HasMCP() {
				selected = selected && hasAllMCPRegistrations(p, installations, key, id)
			}
		}
		note := ""
		if filepath.Clean(env.Home) != filepath.Clean(currentUserHome()) {
			note = "Uses recorded custom home override " + env.Home
		} else if override := nativeConfigOverride(env.Kind, env); override != "" {
			note = override
		}
		if p.HasMCP() && adapterErr == nil {
			nativePath, nativeErr := nativePlannedConfigPath(id, env.Kind, currentUserHome())
			if nativeErr != nil {
				return viewmodel.SetupPreview{}, nativeErr
			}
			if filepath.Clean(writePath) != filepath.Clean(nativePath) {
				if note != "" {
					note += "; "
				}
				note += fmt.Sprintf("Recorded AACT config path %s plans write to %s; process-native planned config path is %s", env.ConfigPath, writePath, nativePath)
			}
		}
		if p.HasMCP() && adapterErr == nil {
			if _, statErr := os.Stat(writePath); os.IsNotExist(statErr) && disabledReason == "" {
				if note != "" {
					note += "; "
				}
				note += "Configuration file will be created on Save"
			}
		}
		preview.Destinations = append(preview.Destinations, viewmodel.SetupDestination{
			ID: id, Kind: env.Kind, Home: env.Home, SkillsPath: env.SkillsDir,
			ConfigPath: writePath, Detection: detection, Note: note, DisabledReason: disabledReason,
			Path: path, Selected: selected,
		})
	}
	shared := map[string][]int{}
	for i, destination := range preview.Destinations {
		if destination.SkillsPath != "" {
			shared[filepath.Clean(destination.SkillsPath)] = append(shared[filepath.Clean(destination.SkillsPath)], i)
		}
	}
	for path, indexes := range shared {
		if len(indexes) < 2 {
			continue
		}
		for _, index := range indexes {
			message := "Shares skill directory " + path + " with "
			names := []string{}
			for _, other := range indexes {
				if index != other {
					names = append(names, preview.Destinations[other].ID)
				}
			}
			preview.Destinations[index].Note = strings.TrimSpace(preview.Destinations[index].Note + "; " + message + strings.Join(names, ", "))
		}
	}
	return preview, nil
}

func hasAllMCPRegistrations(p catalog.Package, rows []state.Installation, key state.Key, agentID string) bool {
	for _, definition := range p.MCPDefinitions() {
		mcpID := ""
		if len(p.MCPs) > 0 {
			mcpID = definition.Name
		}
		found := false
		for _, row := range rows {
			if sameCapabilityKey(row.Key, key) &&
				row.Key.MCP == mcpID && row.AgentID == agentID && row.Component == "mcp" {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return len(p.MCPDefinitions()) > 0
}

func isManualAgentID(id string) bool {
	kind, _, _ := strings.Cut(id, ":")
	return agents.IsManual(kind)
}

func currentUserHome() string {
	home, _ := os.UserHomeDir()
	return home
}

func uiDiscoveryProbe() agents.DiscoveryProbe {
	probe, err := agents.DefaultDiscoveryProbe()
	if err != nil {
		return agents.DiscoveryProbe{}
	}
	return probe
}

func (s *Service) discoveryProbe() agents.DiscoveryProbe {
	if s.Options.DiscoveryProbe != nil {
		return *s.Options.DiscoveryProbe
	}
	return uiDiscoveryProbe()
}

func mcpDestinationDisabledReason(ctx context.Context, kind, configPath string, discovery agents.AgentDiscovery, adapterErr error, probe agents.DiscoveryProbe) string {
	if adapterErr != nil {
		return adapterErr.Error()
	}
	lookPath := probe.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	switch kind {
	case "codex":
		if _, err := lookPath("codex"); err != nil {
			if discovery.Detection == "installed" {
				return "Codex desktop detected; this adapter requires the codex CLI, which is unavailable"
			}
			return "Install the codex CLI to manage MCP registrations"
		}
	case "copilot-cli", "copilot":
		if _, err := lookPath("copilot"); err != nil {
			return "Install the Copilot CLI to manage MCP registrations"
		}
	case "opencode", "claude":
		if discovery.Detection != "installed" {
			return fmt.Sprintf("%s was not detected; install it before adding MCP registrations", discovery.Name)
		}
	case "copilot-intellij":
		if _, err := os.Stat(configPath); err != nil {
			return "Copilot IntelliJ config is missing; open Copilot Chat and select Add MCP Tools first"
		}
	default:
		if discovery.Detection != "installed" {
			return fmt.Sprintf("%s was not detected", discovery.Name)
		}
	}
	if err := ctx.Err(); err != nil {
		return "Discovery was interrupted; try loading setup again"
	}
	return ""
}

func nativeConfigOverride(kind string, env agents.Environment) string {
	var variable string
	switch kind {
	case "codex":
		variable = "CODEX_HOME"
	case "opencode":
		variable = "XDG_CONFIG_HOME"
	case "claude":
		variable = "CLAUDE_CONFIG_DIR"
	}
	if variable == "" {
		return ""
	}
	if value := os.Getenv(variable); value != "" {
		return "Uses process-native " + variable + " for agent config paths: " + value
	}
	return ""
}

func nativePlannedConfigPath(id, kind, home string) (string, error) {
	env, err := agents.ResolveEnvironment(id, kind, home)
	if err != nil {
		return "", err
	}
	if kind == "codex" || kind == "opencode" {
		env, err = agents.ApplyNativeConfigOverrides(env)
		if err != nil {
			return "", err
		}
	}
	return agents.ResolveConfigWritePath(env)
}

// targetInputChoices turns wildcard table keys in a target TOML into stable
// checkbox values. For example, dbms.*.tenants.* yields dbms/tenant IDs.
func targetInputChoices(raw map[string]any, path string) ([]catalog.Choice, error) {
	parts := strings.Split(path, ".")
	choices := []catalog.Choice{}
	var walk func(any, int, []string) error
	walk = func(node any, index int, keys []string) error {
		if index == len(parts) {
			if len(keys) == 0 {
				return fmt.Errorf("options_from %q must contain a wildcard", path)
			}
			value := strings.Join(keys, "/")
			label := value
			if table, ok := node.(map[string]any); ok {
				if named, exists := table["label"]; exists {
					name, ok := named.(string)
					if !ok || strings.TrimSpace(name) == "" {
						return fmt.Errorf("options_from %q: label for %s must be a non-empty string", path, value)
					}
					label = name + " — " + value
				}
			}
			choices = append(choices, catalog.Choice{Value: value, Label: label})
			return nil
		}
		table, ok := node.(map[string]any)
		if !ok {
			return fmt.Errorf("options_from %q expects a table at %s", path, strings.Join(parts[:index], "."))
		}
		if part := parts[index]; part != "*" {
			child, exists := table[part]
			if !exists {
				return nil
			}
			return walk(child, index+1, keys)
		}
		names := make([]string, 0, len(table))
		for name := range table {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if err := walk(table[name], index+1, append(append([]string(nil), keys...), name)); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(raw, 0, nil); err != nil {
		return nil, err
	}
	return choices, nil
}

// UIInstall applies the complete form through the existing noninteractive
// service operation. A caller-supplied form never opens the legacy editor.
func (s *Service) UIInstall(ctx context.Context, q viewmodel.SetupInstallRequest) (viewmodel.OperationResult, error) {
	if _, scoped := ctx.Value(operationProgressScopeContextKey{}).(bool); !scoped {
		if observer := viewmodel.OperationProgressObserver(ctx); observer != nil {
			scopedService, scopedCtx := s.withOperationProgress(ctx, observer)
			return scopedService.UIInstall(scopedCtx, q)
		}
	}
	if err := ctx.Err(); err != nil {
		return viewmodel.OperationResult{}, err
	}
	if q.SourceID != "" && q.SourceID != s.Source.ID {
		source, err := s.forSource(q.SourceID)
		if err != nil {
			return viewmodel.OperationResult{}, invalid(err)
		}
		q.SourceID = source.Source.ID
		return source.UIInstall(ctx, q)
	}
	p, err := s.packageByID(q.PackageID)
	if err != nil {
		return viewmodel.OperationResult{}, err
	}
	key := s.key(p.ID, q.Environment, q.Target)
	envs := make([]agents.Environment, 0, len(q.DestinationIDs))
	desired := make(map[string]bool, len(q.DestinationIDs))
	for _, id := range q.DestinationIDs {
		if id == "" || desired[id] {
			return viewmodel.OperationResult{}, invalid(fmt.Errorf("duplicate or empty destination %q", id))
		}
		desired[id] = true
		kind, _, _ := strings.Cut(id, ":")
		if p.HasMCP() && id == "all" {
			return viewmodel.OperationResult{}, invalid(errors.New("global All is a skill-only destination and is not a named MCP agent"))
		}
		if p.HasMCP() && agents.IsManual(kind) {
			return viewmodel.OperationResult{}, invalid(fmt.Errorf("destination %q is not a named MCP agent", id))
		}
		env, envErr := s.uiEnvironment(id, key)
		if envErr != nil {
			return viewmodel.OperationResult{}, envErr
		}
		envs = append(envs, env)
	}
	if err := validateGlobalSkillDestination(p, envs); err != nil {
		return viewmodel.OperationResult{}, invalid(err)
	}
	rows, err := s.Store.Installations()
	if err != nil {
		return viewmodel.OperationResult{}, err
	}
	remove := map[string]agents.Environment{}
	for _, row := range rows {
		if row.Key.Source != key.Source || row.Key.Package != key.Package || row.Key.Environment != key.Environment || row.Key.Target != key.Target ||
			(row.Component != "skill" && row.Component != "mcp") || row.AgentID == "" || desired[row.AgentID] {
			continue
		}
		kind := row.AgentKind
		if kind == "" {
			kind, _, _ = strings.Cut(row.AgentID, ":")
		}
		env := remove[row.AgentID]
		env.ID, env.Kind, env.Home = row.AgentID, kind, row.AgentHome
		if row.Component == "mcp" {
			env.ConfigPath = row.Destination
		}
		if row.Component == "skill" {
			env.SkillsDir = filepath.Dir(row.Destination)
		}
		remove[row.AgentID] = env
	}
	removed := viewmodel.OperationResult{}
	var removeErr error
	if len(remove) > 0 {
		removed, removeErr = s.removeCapabilityBindings(ctx, p, key, remove)
	}
	if len(q.DestinationIDs) == 0 {
		saveErr := s.savePartialSetupAnswers(ctx, p, key, q.Inputs)
		removed.Saved = saveErr == nil
		removed.SavedApplicable = true
		return removed, errors.Join(removeErr, saveErr)
	}
	if removeErr != nil {
		return removed, removeErr
	}
	inputs := make(map[string]any, len(q.Inputs))
	for name, value := range q.Inputs {
		inputs[name] = value
	}
	result, err := s.Install(ctx, InstallRequest{
		Package: q.PackageID, Environment: q.Environment, Target: q.Target, Agents: envs,
		Inputs: inputs, Interactive: false, ExternalURL: q.ExternalURL, ExternalURLs: q.ExternalURLs,
	})
	result.Changes = append(removed.Changes, result.Changes...)
	result.Errors = append(removed.Errors, result.Errors...)
	return viewmodel.OperationResult{
		Changes: result.Changes, Errors: result.Errors, Saved: result.Saved, SavedApplicable: true, Message: result.Message,
		Step: result.Step, Target: result.Target,
	}, err
}

// savePartialSetupAnswers persists declared nonsecret edits without applying
// required-field validation, generation, authentication, or runtime actions.
func (s *Service) savePartialSetupAnswers(ctx context.Context, p catalog.Package, key state.Key, submitted map[string]any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	target := config.Target{Environment: key.Environment, Name: key.Target, Raw: map[string]any{}}
	if key.Environment != "" {
		loaded, err := config.LoadTarget(s.Source, p.ID, key.Environment, key.Target)
		if err != nil {
			return invalid(err)
		}
		target = loaded
	}
	fixed, err := fixedTargetInputs(p.Inputs, target, target.Raw)
	if err != nil {
		return invalid(err)
	}
	values, err := s.Store.Answers(key)
	if err != nil {
		return err
	}
	declared := map[string]bool{}
	for _, definition := range p.Inputs {
		declared[definition.Name] = true
	}
	for name, value := range submitted {
		if !declared[name] {
			continue
		}
		if _, locked := fixed[name]; locked {
			return invalid(fmt.Errorf("input %q is fixed by target %s", name, target.Path))
		}
		values[name] = value
	}
	for name, value := range fixed {
		values[name] = value
	}
	answerPath := filepath.Join(s.Store.Root(), "answers", key.ID()+".json")
	values, err = config.ResolveInputPaths(p.Inputs, values, answerPath)
	if err != nil {
		return invalid(err)
	}
	safe := map[string]any{}
	for _, definition := range p.Inputs {
		if definition.Type != "secret" {
			if value, ok := values[definition.Name]; ok {
				safe[definition.Name] = value
			}
		}
	}
	return s.Store.WithLock(ctx, func() error { return s.Store.SaveAnswers(key, safe) })
}

// removeCapabilityBindings removes only recorded skill and MCP effects for
// unchecked destinations. Runtime lifecycle remains shared by the capability.
func (s *Service) removeCapabilityBindings(ctx context.Context, p catalog.Package, key state.Key, destinations map[string]agents.Environment) (out viewmodel.OperationResult, err error) {
	out.Changes = []state.Installation{}
	out.Errors = []string{}
	err = s.Store.WithLock(ctx, func() error {
		rows, e := s.Store.Installations()
		if e != nil {
			return e
		}
		skills := install.NewSkills(s.Store)
		for id, environment := range destinations {
			env := s.ownedEnvironment(environment, rows)
			failed := false
			for _, row := range rows {
				if row.Key.Source != key.Source || row.Key.Package != key.Package || row.Key.Environment != key.Environment || row.Key.Target != key.Target || row.AgentID != id || row.Component != "mcp" {
					continue
				}
				adapter, adapterErr := agents.For(env.Kind, s.Options.Runner)
				files, snapshotErr := snapshotRegistration(env)
				if adapterErr == nil {
					adapterErr = snapshotErr
				}
				if adapterErr == nil {
					adapterErr = adapter.Unregister(ctx, env, row.RegistrationName)
				}
				if adapterErr == nil {
					adapterErr = s.removeRegistration(row)
				}
				if adapterErr != nil && len(files) > 0 {
					adapterErr = errors.Join(adapterErr, restoreRegistration(files))
				}
				if adapterErr != nil {
					out.Errors = append(out.Errors, id+": "+adapterErr.Error())
					failed = true
				} else {
					out.Changes = append(out.Changes, row)
				}
			}
			if failed {
				continue
			}
			hasSkill := false
			skillRows := []state.Installation{}
			for _, row := range rows {
				if row.Key.Source == key.Source && row.Key.Package == key.Package && row.Key.Environment == key.Environment && row.Key.Target == key.Target && row.AgentID == id && row.Component == "skill" {
					hasSkill = true
					env.SkillsDir = filepath.Dir(row.Destination)
					skillRows = append(skillRows, row)
				}
			}
			if hasSkill {
				if e := skills.Uninstall(ctx, key, env); e != nil {
					out.Errors = append(out.Errors, id+": "+e.Error())
				} else {
					out.Changes = append(out.Changes, skillRows...)
				}
			}
		}
		if len(out.Errors) > 0 {
			return errors.New(strings.Join(out.Errors, "; "))
		}
		return nil
	})
	return out, err
}
