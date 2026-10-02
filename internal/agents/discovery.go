package agents

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
)

// DiscoveryProbe keeps platform observations separate from adapter writes and
// allows discovery tests to use temporary directories and executable evidence.
type DiscoveryProbe struct {
	GOOS     string
	Home     string
	AppRoots []string
	LookPath func(string) (string, error)
	Getenv   func(string) string
}

type DiscoveredConfig struct {
	Path       string
	Scope      string
	Precedence string
	Evidence   string
	Exists     bool
}

type AgentDiscovery struct {
	ID, Name, Detection, Evidence, Note string
	ConfigFiles                         []DiscoveredConfig
}

func DefaultDiscoveryProbe() (DiscoveryProbe, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return DiscoveryProbe{}, err
	}
	return DiscoveryProbe{
		GOOS: runtime.GOOS, Home: home,
		AppRoots: []string{"/Applications", filepath.Join(home, "Applications")},
		LookPath: exec.LookPath, Getenv: os.Getenv,
	}, nil
}

func DiscoverAgent(ctx context.Context, id string, probe DiscoveryProbe) AgentDiscovery {
	r := AgentDiscovery{ID: id, Name: discoveryName(id), Detection: "not-detected"}
	if err := ctx.Err(); err != nil {
		r.Detection, r.Note = "unverified", err.Error()
		return r
	}
	if probe.GOOS != "darwin" {
		r.Detection = "unverified"
		r.Note = "Discovery for this platform has not been verified locally"
		return r
	}
	if probe.LookPath == nil {
		probe.LookPath = exec.LookPath
	}
	if probe.Getenv == nil {
		probe.Getenv = os.Getenv
	}
	addConfig := func(path, scope, precedence, evidence string) {
		_, err := os.Stat(path)
		r.ConfigFiles = append(r.ConfigFiles, DiscoveredConfig{Path: path, Scope: scope, Precedence: precedence, Evidence: evidence, Exists: err == nil})
	}
	cli := func(program string) {
		if path, err := probe.LookPath(program); err == nil && path != "" {
			r.Detection, r.Evidence = "installed", fmt.Sprintf("CLI executable: %s", path)
		}
	}
	app := func(pattern string) bool {
		for _, root := range probe.AppRoots {
			paths, _ := filepath.Glob(filepath.Join(root, pattern))
			for _, path := range paths {
				if info, err := os.Stat(path); err == nil && info.IsDir() {
					r.Detection = "installed"
					if r.Evidence != "" {
						r.Evidence += "; "
					}
					r.Evidence += fmt.Sprintf("Application: %s", path)
					return true
				}
			}
		}
		return false
	}
	switch id {
	case "codex":
		cli("codex")
		if app("Codex.app") {
			r.Note = "Codex Desktop MCP reload behavior has not been verified"
		}
		root := probe.Getenv("CODEX_HOME")
		if root == "" {
			root = filepath.Join(probe.Home, ".codex")
		}
		addConfig(filepath.Join(root, "config.toml"), "user", "effective", "Codex CLI TOML")
	case "claude":
		cli("claude")
		root := probe.Getenv("CLAUDE_CONFIG_DIR")
		if root == "" {
			root = probe.Home
		}
		addConfig(filepath.Join(root, ".claude.json"), "user", "effective", "Claude Code user scope")
	case "opencode":
		cli("opencode")
		root := probe.Getenv("XDG_CONFIG_HOME")
		if root == "" {
			root = filepath.Join(probe.Home, ".config")
		}
		base := filepath.Join(root, "opencode")
		addConfig(filepath.Join(base, "opencode.json"), "user", "lower", "OpenCode JSON")
		addConfig(filepath.Join(base, "opencode.jsonc"), "user", "higher", "OpenCode JSONC")
	case "claude-desktop":
		app("Claude.app")
		addConfig(filepath.Join(probe.Home, "Library", "Application Support", "Claude", "claude_desktop_config.json"), "desktop user", "effective", "Claude Desktop JSON")
	case "opencode-desktop":
		app("OpenCode.app")
		r.Note = "Desktop-specific MCP config behavior has not been verified"
	case "intellij", "intellij-ai-assistant":
		app("IntelliJ*.app")
		if r.Evidence != "" {
			r.Evidence = "IDE " + r.Evidence
		}
		r.Detection = "unverified"
		pattern := filepath.Join(probe.Home, "Library", "Application Support", "JetBrains", "IntelliJIdea*", "options", "llm.mcpServers.xml")
		paths, _ := filepath.Glob(pattern)
		sort.Strings(paths)
		for _, path := range paths {
			addConfig(path, "IDE user", "version-specific", "JetBrains AI Assistant XML")
		}
		r.Note = "IDE installation and XML settings do not verify that the AI Assistant plugin is enabled or licensed; XML is read-only"
	case "copilot-cli", "copilot", "copilot-intellij":
		r.Detection = "unverified"
		r.Note = "Copilot client and config discovery have not been verified locally; OpenCode provider access is separate"
	default:
		r.Detection = "unverified"
		r.Note = "No installation discovery is defined for this agent"
	}
	return r
}

func discoveryName(id string) string {
	switch id {
	case "codex":
		return "Codex"
	case "claude":
		return "Claude Code"
	case "opencode":
		return "OpenCode CLI"
	case "claude-desktop":
		return "Claude Desktop"
	case "opencode-desktop":
		return "OpenCode Desktop"
	case "intellij", "intellij-ai-assistant":
		return "JetBrains AI Assistant"
	case "copilot-cli", "copilot":
		return "GitHub Copilot CLI"
	case "copilot-intellij":
		return "GitHub Copilot in JetBrains"
	default:
		return id
	}
}
