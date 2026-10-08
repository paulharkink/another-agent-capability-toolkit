package install

import (
	"context"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"path/filepath"
)

// SkillDestination contains resolved filesystem locations, never agent policy.
// Kind is retained solely as legacy ownership-record metadata.
type SkillDestination struct{ ID, Home, SkillsDir, Kind string }

func (s *Skills) recordInstallation(row state.Installation) error {
	if s.recordEffect != nil {
		return s.recordEffect(row)
	}
	return s.Store.Record(row)
}

func (s *Skills) InstallOne(ctx context.Context, pkg catalog.Package, skill catalog.Skill, destination SkillDestination, key state.Key, staged string) (state.Installation, error) {
	pkg.Skill = &skill
	pkg.Skills = nil
	if skill.Source != "" {
		if filepath.IsAbs(skill.Source) {
			pkg.Dir = skill.Source
		} else {
			pkg.Dir = filepath.Join(pkg.Dir, skill.Source)
		}
	}
	engine := *s
	var effect state.Installation
	engine.recordEffect = func(row state.Installation) error { effect = row; return nil }
	err := engine.Install(ctx, pkg, destination, key, staged)
	return effect, err
}

func (s *Skills) RemoveOne(ctx context.Context, row state.Installation) error {
	engine := *s
	engine.removeDestination = row.Destination
	return engine.Uninstall(ctx, row.Key, SkillDestination{ID: row.AgentID, Home: row.AgentHome, Kind: row.AgentKind, SkillsDir: filepath.Dir(row.Destination)})
}
