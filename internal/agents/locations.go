package agents

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type Environment struct {
	ID, Kind, Home, SkillsDir, ConfigPath string
	Owned                                 map[string]Registration
}

func ResolveEnvironment(id, kind, home string) (Environment, error) {
	return resolveEnvironment(id, kind, home, runtime.GOOS, os.Getenv)
}

// resolveEnvironment is kept injectable so platform path rules can be tested
// on a host other than the target OS.
func resolveEnvironment(id, kind, home, goos string, getenv func(string) string) (Environment, error) {
	if home == "" {
		return Environment{}, fmt.Errorf("agent home must be explicit")
	}
	if getenv == nil {
		getenv = os.Getenv
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
		// The installed AI Assistant plugin resolves this default below the user
		// home, and exposes its JSON path through its Registry key. AACT supports
		// the default plus an explicit Environment.ConfigPath for an override.
		e.ConfigPath = filepath.Join(home, ".ai", "mcp", "mcp.json")
	case "copilot-intellij":
		e.SkillsDir = filepath.Join(home, ".copilot", "skills")
		base := filepath.Join(home, ".config")
		if goos == "windows" {
			base = getenv("APPDATA")
			if base == "" {
				base = filepath.Join(home, "AppData", "Roaming")
			}
		}
		e.ConfigPath = filepath.Join(base, "github-copilot", "intellij", "mcp.json")
	case "claude":
		e.SkillsDir = filepath.Join(home, ".claude", "skills")
		configRoot := home
		if override := getenv("CLAUDE_CONFIG_DIR"); override != "" {
			configRoot, err = filepath.Abs(override)
			if err != nil {
				return Environment{}, err
			}
		}
		e.ConfigPath = filepath.Join(configRoot, ".claude.json")
	case "hermes":
		e.SkillsDir = filepath.Join(home, ".hermes", "skills")
	case "generic", "generic-mcp":
		e.SkillsDir = filepath.Join(home, ".agents", "skills")
	default:
		return Environment{}, fmt.Errorf("unsupported agent kind %q", kind)
	}
	return e, nil
}

func jetBrainsAISettingsFiles(home, goos string, getenv func(string) string) []string {
	if getenv == nil {
		getenv = os.Getenv
	}
	root := filepath.Join(home, ".config", "JetBrains")
	if goos == "darwin" {
		root = filepath.Join(home, "Library", "Application Support", "JetBrains")
	} else if goos == "windows" {
		appData := getenv("APPDATA")
		if appData == "" {
			appData = filepath.Join(home, "AppData", "Roaming")
		}
		root = filepath.Join(appData, "JetBrains")
	}
	paths, _ := filepath.Glob(filepath.Join(root, "*", "options", "llm.mcpServers.xml"))
	filtered := paths[:0]
	for _, path := range paths {
		product := strings.ToLower(filepath.Base(filepath.Dir(filepath.Dir(path))))
		if !strings.Contains(product, "backup") {
			filtered = append(filtered, path)
		}
	}
	return filtered
}

// ApplyNativeConfigOverrides follows config-root environment variables for the
// process's default agent home. Callers with an explicit custom agent home keep
// the paths returned by ResolveEnvironment instead.
func ApplyNativeConfigOverrides(env Environment) (Environment, error) {
	var root string
	switch env.Kind {
	case "codex":
		root = os.Getenv("CODEX_HOME")
		if root != "" {
			absolute, err := filepath.Abs(root)
			if err != nil {
				return env, err
			}
			env.ConfigPath = filepath.Join(absolute, "config.toml")
		}
	case "opencode":
		root = os.Getenv("XDG_CONFIG_HOME")
		if root != "" {
			absolute, err := filepath.Abs(root)
			if err != nil {
				return env, err
			}
			base := filepath.Join(absolute, "opencode")
			env.ConfigPath = filepath.Join(base, "opencode.json")
			env.SkillsDir = filepath.Join(base, "skills")
		}
	case "copilot", "copilot-cli":
		root = os.Getenv("COPILOT_HOME")
		if root != "" {
			absolute, err := filepath.Abs(root)
			if err != nil {
				return env, err
			}
			env.ConfigPath = filepath.Join(absolute, "mcp-config.json")
			env.SkillsDir = filepath.Join(absolute, "skills")
		}
	}
	return env, nil
}

// ResolveConfigWritePath returns the file the existing adapter will mutate.
// OpenCode's adapter writes JSONC when it exists, otherwise JSON when it
// exists, and defaults to JSONC when neither sibling exists.
func ResolveConfigWritePath(env Environment) (string, error) {
	path := env.ConfigPath
	if path == "" {
		resolved, err := ResolveEnvironment(env.ID, env.Kind, env.Home)
		if err != nil {
			return "", err
		}
		path = resolved.ConfigPath
	}
	if env.Kind != "opencode" {
		return path, nil
	}
	ext := filepath.Ext(path)
	if ext != ".json" && ext != ".jsonc" {
		return path, nil
	}
	jsonPath := strings.TrimSuffix(path, ext) + ".json"
	jsoncPath := strings.TrimSuffix(path, ext) + ".jsonc"
	if _, err := os.Stat(jsoncPath); err == nil {
		return jsoncPath, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if _, err := os.Stat(jsonPath); err == nil {
		return jsonPath, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	return jsoncPath, nil
}

type Registration struct {
	Name, URL, Transport string
	TimeoutMS            int
}
