package agents

import (
	"errors"
	"path/filepath"
)

// GlobalSkillsEnvironment is the shared skill destination used by agents that
// read ~/.agents/skills. The generic adapter owns this skills-only destination;
// this function remains a compatibility location facade.
func GlobalSkillsEnvironment(home string) (Environment, error) {
	if home == "" {
		return Environment{}, errors.New("home must be explicit for global skills")
	}
	absolute, err := filepath.Abs(home)
	if err != nil {
		return Environment{}, err
	}
	return Environment{ID: "all", Kind: "all", Home: absolute, SkillsDir: filepath.Join(absolute, ".agents", "skills")}, nil
}
