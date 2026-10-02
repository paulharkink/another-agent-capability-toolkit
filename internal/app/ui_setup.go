package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
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
	defaultValues := map[string]any{}
	for _, def := range p.Inputs {
		if def.Default != nil {
			defaultValues[def.Name] = def.Default
		}
	}
	paths := []string{
		filepath.Join(p.Dir, "package.toml"),
		filepath.Join(s.Store.Root(), "answers", key.ID()+".json"),
		s.Source.ManifestPath,
		target.Path,
	}
	if paths[2] == "" {
		paths[2] = filepath.Join(s.Source.Root, "aact.toml")
	}
	layers := []map[string]any{defaultValues, saved, s.Source.PackageDefaults[p.ID], target.Raw}
	for i := range layers {
		layers[i], err = config.ResolveInputPaths(p.Inputs, layers[i], paths[i])
		if err != nil {
			return viewmodel.SetupPreview{}, invalid(err)
		}
	}
	// Resolve each layer without declaration defaults. This preserves the same
	// precedence and config_key conflict checks as Install while identifying origin.
	defs := append([]catalog.Input(nil), p.Inputs...)
	for i := range defs {
		defs[i].Default = nil
	}
	preview := viewmodel.SetupPreview{Key: key, PackageName: p.Name, SourceRoot: s.Source.Root, TargetPath: target.Path}
	values := map[string]any{}
	origins := []string{"package", "saved", "source", "target"}
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
	for _, def := range p.Inputs {
		value, present := values[def.Name]
		origin := provenance[def.Name]
		if !present {
			origin = "unset"
		}
		preview.Inputs = append(preview.Inputs, viewmodel.SetupInput{Definition: def, Value: value, HasValue: present, Provenance: origin, ProvenancePath: provenancePaths[def.Name], Editable: true})
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
		preview.Destinations = append(preview.Destinations, viewmodel.SetupDestination{ID: id, Path: path, Selected: id == "all" || defaultAgents[id]})
	}
	return preview, nil
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
	return viewmodel.OperationResult{Changes: result.Changes, Errors: result.Errors, Message: result.Message}, err
}
