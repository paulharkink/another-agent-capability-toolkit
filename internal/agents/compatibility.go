package agents

import (
	"context"
	"errors"
	"fmt"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/install"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func MCPDestinationDisabledReason(ctx context.Context, kind, configPath string, discovery AgentDiscovery, adapterErr error, probe DiscoveryProbe) string {
	if adapterErr != nil {
		return adapterErr.Error()
	}
	lookPath := probe.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	switch kind {
	case "codex":
		if _, err := lookPath("codex"); err != nil {
			if discovery.Detection == "installed" {
				return "Codex desktop detected; this adapter requires the codex CLI, which is unavailable"
			}
			return "Install the codex CLI to manage MCP registrations"
		}
	case "copilot-cli", "copilot":
		if _, err := lookPath("copilot"); err != nil {
			return "Install the Copilot CLI to manage MCP registrations"
		}
	case "opencode", "claude":
		if discovery.Detection != "installed" {
			return fmt.Sprintf("%s was not detected; install it before adding MCP registrations", discovery.Name)
		}
	case "copilot-intellij":
		if _, err := os.Stat(configPath); err != nil {
			return "Copilot IntelliJ config is missing; open Copilot Chat and select Add MCP Tools first"
		}
	default:
		if discovery.Detection != "installed" {
			return fmt.Sprintf("%s was not detected", discovery.Name)
		}
	}
	if err := ctx.Err(); err != nil {
		return "Discovery was interrupted; try loading setup again"
	}
	return ""
}

func NativeConfigOverride(kind string, env Environment) string {
	var variable string
	switch kind {
	case "codex":
		variable = "CODEX_HOME"
	case "opencode":
		variable = "XDG_CONFIG_HOME"
	case "claude":
		variable = "CLAUDE_CONFIG_DIR"
	}
	if variable == "" {
		return ""
	}
	if value := os.Getenv(variable); value != "" {
		return "Uses process-native " + variable + " for agent config paths: " + value
	}
	return ""
}

func NativePlannedConfigPath(id, kind, home string) (string, error) {
	env, err := ResolveEnvironment(id, kind, home)
	if err != nil {
		return "", err
	}
	if kind == "codex" || kind == "opencode" {
		env, err = ApplyNativeConfigOverrides(env)
		if err != nil {
			return "", err
		}
	}
	return ResolveConfigWritePath(env)
}

type ConfigSnapshot struct {
	path    string
	data    []byte
	mode    os.FileMode
	existed bool
}

func SnapshotRegistration(env Environment) ([]ConfigSnapshot, error) {
	paths := []string{env.ConfigPath}
	if env.Kind == "opencode" {
		ext := filepath.Ext(env.ConfigPath)
		if ext == ".json" {
			paths = append(paths, strings.TrimSuffix(env.ConfigPath, ext)+".jsonc")
		} else if ext == ".jsonc" {
			paths = append(paths, strings.TrimSuffix(env.ConfigPath, ext)+".json")
		}
	}
	out := []ConfigSnapshot{}
	for _, path := range paths {
		if path == "" {
			continue
		}
		b, e := os.ReadFile(path)
		if os.IsNotExist(e) {
			out = append(out, ConfigSnapshot{path: path})
			continue
		}
		if e != nil {
			return nil, e
		}
		info, e := os.Stat(path)
		if e != nil {
			return nil, e
		}
		out = append(out, ConfigSnapshot{path: path, data: b, mode: info.Mode().Perm(), existed: true})
	}
	return out, nil
}
func RestoreRegistration(files []ConfigSnapshot) error {
	errs := []error{}
	for _, f := range files {
		var e error
		if f.existed {
			e = state.WriteAtomic(f.path, f.data, f.mode)
		} else {
			e = os.Remove(f.path)
			if os.IsNotExist(e) {
				e = nil
			}
		}
		errs = append(errs, e)
	}
	return errors.Join(errs...)
}

// CompatibilitySkillManager keeps the old state migration facade inside agents.
// New installs use the optional SkillManager interface.
type CompatibilitySkillManager struct{ *install.Skills }

func NewCompatibilitySkills(store *state.Store) *CompatibilitySkillManager {
	return &CompatibilitySkillManager{install.NewSkills(store)}
}
func (s *CompatibilitySkillManager) Install(ctx context.Context, p catalog.Package, e Environment, k state.Key, dir string) error {
	return s.Skills.Install(ctx, p, install.SkillDestination{ID: e.ID, Home: e.Home, Kind: e.Kind, SkillsDir: e.SkillsDir}, k, dir)
}
func (s *CompatibilitySkillManager) Uninstall(ctx context.Context, k state.Key, e Environment) error {
	return s.Skills.Uninstall(ctx, k, install.SkillDestination{ID: e.ID, Home: e.Home, Kind: e.Kind, SkillsDir: e.SkillsDir})
}
func CompatibilityIDs() []string {
	return []string{"all", "codex", "opencode", "claude", "copilot-cli", "intellij", "copilot-intellij", "generic"}
}
func DesktopDiscoveryIDs() []string { return []string{"claude-desktop", "opencode-desktop"} }
func EffectiveCompatibilityConfig(env Environment) (Environment, error) {
	if env.Kind == "opencode" {
		path, err := ResolveConfigWritePath(env)
		env.ConfigPath = path
		return env, err
	}
	return env, nil
}
func DisplayName(id string) string {
	switch strings.ToLower(id) {
	case "all", "generic":
		return "All"
	case "opencode":
		return "OpenCode"
	case "claude-code":
		return "Claude Code"
	default:
		return discoveryName(id)
	}
}
