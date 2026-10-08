package agents

import (
	"context"
	"fmt"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/process"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"os"
	"runtime"
	"strings"
)

type Dependencies struct {
	Store  *state.Store
	Runner process.Executor
	Probe  DiscoveryProbe
}
type Registry struct{ adapters []Adapter }
type registeredAdapter struct {
	kind string
	deps Dependencies
}

func NewRegistry(deps Dependencies) *Registry {
	if deps.Probe.GOOS == "" && deps.Probe.Home == "" {
		if probe, err := DefaultDiscoveryProbe(); err == nil {
			deps.Probe = probe
		}
	}
	if deps.Probe.GOOS == "" {
		deps.Probe.GOOS = runtime.GOOS
	}
	if deps.Probe.Home == "" {
		deps.Probe.Home, _ = os.UserHomeDir()
	}
	r := &Registry{}
	for _, kind := range []string{"codex", "claude", "opencode", "copilot-cli", "intellij", "copilot-intellij", "hermes", "generic"} {
		skill := &skillAdapter{registeredAdapter: &registeredAdapter{kind: kind, deps: deps}}
		if kind != "generic" && kind != "hermes" {
			mcp := &mcpAdapter{skillAdapter: skill}
			if kind == "claude" {
				r.adapters = append(r.adapters, &claudePluginAdapter{mcpAdapter: mcp})
			} else {
				r.adapters = append(r.adapters, mcp)
			}
		} else {
			r.adapters = append(r.adapters, skill)
		}
	}
	return r
}
func (r *Registry) Adapter(id string) (Adapter, error) {
	id, _, _ = strings.Cut(id, ":")
	if id == "all" {
		id = "generic"
	}
	if id == "copilot" {
		id = "copilot-cli"
	}
	if id == "intellij-ai-assistant" {
		id = "intellij"
	}
	for _, adapter := range r.adapters {
		if adapter.ID() == id {
			return adapter, nil
		}
	}
	return nil, fmt.Errorf("unsupported agent adapter %q", id)
}
func (r *Registry) Adapters() []Adapter { return append([]Adapter(nil), r.adapters...) }
func (a *registeredAdapter) ID() string { return a.kind }
func (a *registeredAdapter) Name() string {
	if a.kind == "generic" {
		return "All"
	}
	return discoveryName(a.kind)
}

// Features become available as their concrete optional managers are installed.
func (a *registeredAdapter) Features() FeatureSet { return FeatureSet{} }

func (a *registeredAdapter) scopedProbe(scope Scope) DiscoveryProbe {
	p := a.deps.Probe
	if scope.Home != "" {
		p.Home = scope.Home
	}
	if scope.ExplicitHome {
		p.Getenv = func(string) string { return "" }
	}
	return p
}
func (a *registeredAdapter) Detect(ctx context.Context, scope Scope) (Detection, error) {
	if err := ctx.Err(); err != nil {
		return Detection{State: "unverified", Reason: err.Error()}, err
	}
	environment, layoutErr := a.environment(scope)
	if layoutErr != nil {
		return Detection{State: "unverified", Reason: layoutErr.Error()}, layoutErr
	}
	if a.kind == "generic" {
		return Detection{Home: environment.Home, SkillsPath: environment.SkillsDir, State: "installed", Installed: true, Evidence: "Shared .agents/skills destination"}, nil
	}
	d := DiscoverAgent(ctx, a.kind, a.scopedProbe(scope))
	result := Detection{Home: environment.Home, SkillsPath: environment.SkillsDir, State: d.Detection, Installed: d.Detection == "installed", Evidence: d.Evidence, Reason: d.Note}
	for _, file := range d.ConfigFiles {
		result.ConfigFiles = append(result.ConfigFiles, ConfigFile{Path: file.Path, Scope: file.Scope, Precedence: file.Precedence, Evidence: file.Evidence, Exists: file.Exists})
	}
	if scope.ConfigPathOverride != "" {
		_, err := os.Stat(scope.ConfigPathOverride)
		result.ConfigFiles = []ConfigFile{{Path: scope.ConfigPathOverride, Scope: "explicit", Precedence: "effective", Evidence: "Explicit agent config override", Exists: err == nil}}
	}
	if _, err := For(a.kind, a.deps.Runner); err == nil && result.Installed {
		result.MCPDisabledReason = MCPDestinationDisabledReason(ctx, a.kind, environment.ConfigPath, d, nil, a.scopedProbe(scope))
		result.CanCreateConfig = result.MCPDisabledReason == ""
	}
	return result, nil
}
