package agents

import (
	"context"
	"fmt"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/install"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"os"
	"path/filepath"
)

type skillAdapter struct{ *registeredAdapter }

func (a *skillAdapter) Features() FeatureSet { return FeatureSet{Skills: true} }

func (a *registeredAdapter) environment(scope Scope) (Environment, error) {
	probe := a.scopedProbe(scope)
	id := scope.ID
	if id == "" {
		id = a.ID()
		if id == "generic" {
			id = "all"
		}
	}
	getenv := probe.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	e, err := resolveEnvironment(id, a.kind, probe.Home, probe.GOOS, getenv)
	if err != nil {
		return e, err
	}
	e, err = applyNativeConfigOverrides(e, getenv)
	if err != nil {
		return e, err
	}
	if scope.ConfigPathOverride != "" {
		e.ConfigPath = scope.ConfigPathOverride
	}
	return e, nil
}
func (a *skillAdapter) InstallSkill(ctx context.Context, scope Scope, request SkillRequest) (state.Installation, error) {
	if a.deps.Store == nil {
		return state.Installation{}, fmt.Errorf("state store required")
	}
	e, err := a.environment(scope)
	if err != nil {
		return state.Installation{}, err
	}
	d, err := a.Detect(ctx, scope)
	if err != nil {
		return state.Installation{}, err
	}
	if !d.Installed {
		return state.Installation{}, fmt.Errorf("agent %s is not installed: %s", a.Name(), d.Reason)
	}
	return install.NewSkills(a.deps.Store).InstallOne(ctx, request.Package, request.Skill, install.SkillDestination{ID: e.ID, Home: e.Home, Kind: e.Kind, SkillsDir: e.SkillsDir}, request.Key, request.StagedDir)
}
func (a *skillAdapter) RemoveSkill(ctx context.Context, scope Scope, row state.Installation) error {
	if a.deps.Store == nil {
		return fmt.Errorf("state store required")
	}
	e, err := a.environment(scope)
	if err != nil {
		return err
	}
	if row.AgentID != e.ID || filepath.Dir(row.Destination) != e.SkillsDir {
		return fmt.Errorf("skill %s does not belong to agent scope %s", row.Destination, e.ID)
	}
	return install.NewSkills(a.deps.Store).RemoveOne(ctx, row)
}
func (a *skillAdapter) Observe(ctx context.Context, scope Scope, request ObservationRequest) (Observation, error) {
	result, err := a.registeredAdapter.Observe(ctx, scope, request)
	if err != nil {
		return result, err
	}
	e, err := a.environment(scope)
	if err != nil {
		return result, err
	}
	seen := map[string]bool{}
	for _, row := range request.Managed {
		if row.Component != "skill" || row.AgentID != e.ID {
			continue
		}
		seen[row.Destination] = true
		component := ComponentObservation{Kind: "skill", Name: filepath.Base(row.Destination), Path: row.Destination, Status: "installed", Managed: true}
		if _, err := os.Stat(filepath.Join(row.Destination, "SKILL.md")); os.IsNotExist(err) {
			component.Status = "absent"
		} else if err != nil {
			component.Status = "unavailable"
			component.Error = err.Error()
		} else if err := install.VerifyInstalled(ctx, row); err != nil {
			component.Status = "modified"
			component.Error = err.Error()
		}
		result.Components = append(result.Components, component)
	}
	if request.IncludeInventory {
		entries, err := os.ReadDir(e.SkillsDir)
		if err != nil && !os.IsNotExist(err) {
			result.Errors = append(result.Errors, err.Error())
		}
		for _, entry := range entries {
			path := filepath.Join(e.SkillsDir, entry.Name())
			if seen[path] {
				continue
			}
			if info, err := os.Stat(filepath.Join(path, "SKILL.md")); err == nil && info.Mode().IsRegular() {
				result.Components = append(result.Components, ComponentObservation{Kind: "skill", Name: entry.Name(), Path: path, Status: "installed"})
			}
		}
	}
	return result, nil
}
