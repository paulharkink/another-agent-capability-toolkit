package app

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/agents"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
)

func validateGlobalSkillDestination(p catalog.Package, destinations []agents.Environment) error {
	for _, destination := range destinations {
		if destination.ID != "all" && destination.Kind != "all" {
			continue
		}
		if p.MCP != nil || p.Skill == nil {
			return errors.New("all is available only for skill-only packages")
		}
		global, err := agents.GlobalSkillsEnvironment(destination.Home)
		if err != nil {
			return err
		}
		if destination.ID != global.ID || destination.Kind != global.Kind || filepath.Clean(destination.SkillsDir) != global.SkillsDir || destination.ConfigPath != "" {
			return fmt.Errorf("invalid all destination: expected %s", global.SkillsDir)
		}
	}
	return nil
}
