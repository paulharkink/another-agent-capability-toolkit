package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/agents"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/forms"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

// UISetupPreview resolves a form without validating missing required answers or
// changing installed state. Values retain the source of their winning layer.
func (s *Service) UISetupPreview(ctx context.Context, q viewmodel.SetupRequest) (viewmodel.SetupPreview, error) {
	if err := ctx.Err(); err != nil {
		return viewmodel.SetupPreview{}, err
	}
	if q.SourceID != "" && q.SourceID != s.Source.ID {
		return viewmodel.SetupPreview{}, invalid(fmt.Errorf("source %s is not the current checkout", q.SourceID))
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
		if row.Key != key {
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
	preview := viewmodel.SetupPreview{Key: key, PackageName: p.Name, SourceRoot: s.Source.Root, TargetPath: target.Path}
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
	if p.MCP != nil {
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
		if id == "all" && (p.Skill == nil || p.MCP != nil) {
			continue
		}
		env, envErr := s.uiEnvironment(id, key)
		if envErr != nil {
			return viewmodel.SetupPreview{}, envErr
		}
		if p.MCP != nil {
			if _, adapterErr := agents.For(env.Kind, s.Options.Runner); adapterErr != nil {
				continue
			}
		}
		path := env.SkillsDir
		if p.MCP != nil {
			path = env.ConfigPath
		}
		selected := id == "all" || defaultAgents[id]
		if attempted {
			selected = (p.Skill == nil || installed[id]["skill"]) && (p.MCP == nil || installed[id]["mcp"])
		}
		preview.Destinations = append(preview.Destinations, viewmodel.SetupDestination{ID: id, Path: path, Selected: selected})
	}
	return preview, nil
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
	if err := ctx.Err(); err != nil {
		return viewmodel.OperationResult{}, err
	}
	if q.SourceID != "" && q.SourceID != s.Source.ID {
		return viewmodel.OperationResult{}, invalid(fmt.Errorf("source %s is not the current checkout", q.SourceID))
	}
	p, err := s.packageByID(q.PackageID)
	if err != nil {
		return viewmodel.OperationResult{}, err
	}
	if len(q.DestinationIDs) == 0 {
		return viewmodel.OperationResult{}, invalid(errors.New("select at least one destination"))
	}
	key := s.key(p.ID, q.Environment, q.Target)
	envs := make([]agents.Environment, 0, len(q.DestinationIDs))
	seen := map[string]bool{}
	for _, id := range q.DestinationIDs {
		if id == "" || seen[id] {
			return viewmodel.OperationResult{}, invalid(fmt.Errorf("duplicate or empty destination %q", id))
		}
		seen[id] = true
		env, envErr := s.uiEnvironment(id, key)
		if envErr != nil {
			return viewmodel.OperationResult{}, envErr
		}
		envs = append(envs, env)
	}
	if err := validateGlobalSkillDestination(p, envs); err != nil {
		return viewmodel.OperationResult{}, invalid(err)
	}
	inputs := make(map[string]any, len(q.Inputs))
	for name, value := range q.Inputs {
		inputs[name] = value
	}
	result, err := s.Install(ctx, InstallRequest{Package: q.PackageID, Environment: q.Environment, Target: q.Target, Agents: envs, Inputs: inputs, Interactive: false})
	return viewmodel.OperationResult{Changes: result.Changes, Errors: result.Errors, Saved: result.Saved, Message: result.Message}, err
}
