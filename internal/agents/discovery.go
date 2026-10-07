package agents

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	if probe.GOOS != "darwin" && probe.GOOS != "linux" && probe.GOOS != "windows" {
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
	if probe.GOOS != "darwin" && id != "codex" && id != "claude" && id != "opencode" && id != "copilot-cli" && id != "copilot" && id != "intellij" && id != "intellij-ai-assistant" && id != "copilot-intellij" {
		r.Detection = "unverified"
		r.Note = "Desktop or IDE plugin installation cannot be confirmed on this platform"
		return r
	}
	if probe.GOOS == "linux" && probe.Getenv("WSL_DISTRO_NAME") != "" {
		r.Note = "WSL uses this Linux user's agent configs; Windows agent configs are separate"
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
		if probe.GOOS == "darwin" && app("Codex.app") {
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
		if probe.GOOS == "darwin" {
			app("IntelliJ*.app")
		}
		if r.Evidence != "" {
			r.Evidence = "IDE " + r.Evidence
		}
		r.Detection = "unverified"
		addConfig(filepath.Join(probe.Home, ".ai", "mcp", "mcp.json"), "user", "effective", "JetBrains AI Assistant JSON")
		for _, path := range jetBrainsAISettingsFiles(probe.Home, probe.GOOS, probe.Getenv) {
			addConfig(path, "IDE user", "metadata", "JetBrains AI Assistant settings XML")
		}
		r.Note = "AI Assistant server definitions use the documented mcpServers JSON format. The installed plugin resolves its global JSON path through a Registry setting; AACT targets the default ~/.ai/mcp/mcp.json, so use an explicit config path if the IDE overrides it. Plugin/license state cannot be confirmed from config files."
	case "copilot-cli", "copilot":
		cli("copilot")
		root := probe.Getenv("COPILOT_HOME")
		if root == "" {
			root = filepath.Join(probe.Home, ".copilot")
		}
		addConfig(filepath.Join(root, "mcp-config.json"), "user", "effective", "GitHub Copilot CLI MCP JSON")
	case "copilot-intellij":
		root := filepath.Join(probe.Home, ".config")
		if probe.GOOS == "windows" {
			root = probe.Getenv("APPDATA")
			if root == "" {
				root = filepath.Join(probe.Home, "AppData", "Roaming")
			}
		}
		addConfig(filepath.Join(root, "github-copilot", "intellij", "mcp.json"), "user", "effective", "GitHub Copilot for JetBrains MCP JSON")
		r.Detection = "unverified"
		r.Note = "The MCP file is inspectable, but its presence does not prove that the Copilot IDE plugin is enabled, licensed, or loading this path"
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
