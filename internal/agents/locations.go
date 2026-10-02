package agents

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

type Environment struct {
	ID, Kind, Home, SkillsDir, ConfigPath string
	Owned                                 map[string]Registration
}

func ResolveEnvironment(id, kind, home string) (Environment, error) {
	if home == "" {
		return Environment{}, fmt.Errorf("agent home must be explicit")
	}
	home, err := filepath.Abs(home)
	if err != nil {
		return Environment{}, err
	}
	e := Environment{ID: id, Kind: kind, Home: home}
	switch kind {
	case "codex":
		e.SkillsDir = filepath.Join(home, ".agents", "skills")
		e.ConfigPath = filepath.Join(home, ".codex", "config.toml")
	case "copilot", "copilot-cli":
		e.SkillsDir = filepath.Join(home, ".copilot", "skills")
		e.ConfigPath = filepath.Join(home, ".copilot", "mcp-config.json")
	case "opencode":
		e.SkillsDir = filepath.Join(home, ".config", "opencode", "skills")
		e.ConfigPath = filepath.Join(home, ".config", "opencode", "opencode.json")
	case "intellij", "intellij-ai-assistant":
		e.SkillsDir = filepath.Join(home, ".ai", "skills")
		e.ConfigPath = filepath.Join(home, ".ai", "mcp", "mcp.json")
	case "copilot-intellij":
		e.SkillsDir = filepath.Join(home, ".copilot", "skills")
		base := filepath.Join(home, ".config")
		if runtime.GOOS == "windows" {
			base = filepath.Join(home, "AppData", "Local")
		}
		e.ConfigPath = filepath.Join(base, "github-copilot", "intellij", "mcp.json")
	case "claude":
		e.SkillsDir = filepath.Join(home, ".claude", "skills")
		configRoot := home
		if override := os.Getenv("CLAUDE_CONFIG_DIR"); override != "" {
			configRoot, err = filepath.Abs(override)
			if err != nil {
				return Environment{}, err
			}
		}
		e.ConfigPath = filepath.Join(configRoot, ".claude.json")
	case "generic", "generic-mcp":
		e.SkillsDir = filepath.Join(home, ".agents", "skills")
	default:
		return Environment{}, fmt.Errorf("unsupported agent kind %q", kind)
	}
	return e, nil
}

type Registration struct {
	Name, URL, Transport string
	TimeoutMS            int
}
