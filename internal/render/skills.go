package render

import (
	"context"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"path/filepath"
)

func StageSkill(ctx context.Context, pkg catalog.Package, skill catalog.Skill, inputs map[string]any, profile config.Profile, parent string) (string, error) {
	return (Renderer{}).StageSkill(ctx, pkg, skill, inputs, profile, parent)
}
func (r Renderer) StageSkill(ctx context.Context, pkg catalog.Package, skill catalog.Skill, inputs map[string]any, profile config.Profile, parent string) (string, error) {
	pkg.Skill = &skill
	pkg.Skills = nil
	if skill.Source != "" {
		if filepath.IsAbs(skill.Source) {
			pkg.Dir = skill.Source
		} else {
			pkg.Dir = filepath.Join(pkg.Dir, skill.Source)
		}
	}
	pkg.Templates = skill.Templates
	pkg.Generator = skill.Generator
	r.Profile = &profile
	return r.Stage(ctx, pkg, inputs, config.Target{}, parent)
}
