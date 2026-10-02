package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/agents"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/forms"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/install"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/mcp"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/process"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/render"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Editor func(context.Context, []catalog.Input, map[string]any) (map[string]any, error)
type MCPRuntime interface {
	Start(context.Context, state.Key, mcp.RunSpec) (mcp.Instance, error)
	Stop(context.Context, state.Key) error
	List(context.Context) ([]mcp.Instance, error)
	Logs(context.Context, state.Key) (io.ReadCloser, error)
}
type Options struct {
	Runner             process.Executor
	Editor             Editor
	Runtime            MCPRuntime
	OnStderr           func([]byte)
	BundledRoot        string
	RecordInstallation func(state.Installation) error
	RemoveInstallation func(state.Installation) error
}
type Service struct {
	Source         config.Source
	Store          *state.Store
	Options        Options
	observationMu  sync.Mutex
	lastInstances  []mcp.Instance
	lastObservedAt time.Time
}

func New(src config.Source, s *state.Store, o Options) *Service {
	if o.Runner == nil {
		o.Runner = process.OSExecutor{}
	}
	if o.Runtime == nil {
		r := mcp.NewDockerRuntime(s)
		r.Executor = o.Runner
		r.OnStderr = o.OnStderr
		o.Runtime = r
	}
	return &Service{Source: src, Store: s, Options: o}
}

type InstallRequest struct {
	Package, Environment, Target string
	Agents                       []agents.Environment
	Inputs                       map[string]any
	Interactive                  bool
	ExternalURL                  string
	UpdateSource                 bool
}
type MCPRequest struct {
	Action, Package, Environment, Target string
	Inputs                               map[string]any
	Interactive                          bool
}
type Result struct {
	Changes   []state.Installation `json:"changes"`
	Errors    []string             `json:"errors"`
	Instances []mcp.Instance       `json:"instances,omitempty"`
	Logs      string               `json:"logs,omitempty"`
	Message   string               `json:"message,omitempty"`
}
type InvalidInput struct{ Err error }

func (e *InvalidInput) Error() string { return e.Err.Error() }
func (e *InvalidInput) Unwrap() error { return e.Err }
func invalid(e error) error           { return &InvalidInput{e} }
func (s *Service) Catalog(ctx context.Context) ([]catalog.Package, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	return s.Source.Catalog, nil
}
func (s *Service) packageByID(id string) (catalog.Package, error) {
	for _, p := range s.Source.Catalog {
		if p.ID == id {
			return p, nil
		}
	}
	return catalog.Package{}, invalid(fmt.Errorf("unknown package %q", id))
}
func (s *Service) key(p, env, target string) state.Key {
	if target == "" {
		target = "default"
	}
	return state.Key{Source: s.Source.ID, Package: p, Environment: env, Target: target}
}
func (s *Service) resolve(ctx context.Context, p catalog.Package, env, target string, cli map[string]any, interactive, prepare bool) (map[string]any, config.Target, state.Key, error) {
	if env == "" && target == "default" {
		target = ""
	}
	k := s.key(p.ID, env, target)
	t := config.Target{Environment: env, Name: k.Target, Raw: map[string]any{}}
	if env != "" || target != "" {
		if env == "" || target == "" {
			return nil, t, k, invalid(errors.New("--environment and --target must be specified together"))
		}
		var e error
		t, e = config.LoadTarget(s.Source, p.ID, env, target)
		if e != nil {
			return nil, t, k, invalid(e)
		}
	}
	known := map[string]bool{}
	defaults := map[string]any{}
	for _, d := range p.Inputs {
		known[d.Name] = true
		if d.Default != nil {
			defaults[d.Name] = d.Default
		}
	}
	for n := range cli {
		if !known[n] {
			return nil, t, k, invalid(fmt.Errorf("unknown input %q", n))
		}
	}
	saved, e := s.Store.Answers(k)
	if e != nil {
		return nil, t, k, e
	}
	cwd, e := os.Getwd()
	if e != nil {
		return nil, t, k, e
	}
	files := []string{filepath.Join(p.Dir, "package.toml"), s.Source.ManifestPath, t.Path, filepath.Join(s.Store.Root(), "answers", k.ID()+".json"), filepath.Join(cwd, ".aact-inputs")}
	if files[1] == "" {
		files[1] = filepath.Join(s.Source.Root, "aact.toml")
	}
	layers := []map[string]any{defaults, s.Source.PackageDefaults[p.ID], t.Raw, saved, cli}
	for n := range layers {
		layers[n], e = config.ResolveInputPaths(p.Inputs, layers[n], files[n])
		if e != nil {
			return nil, t, k, invalid(e)
		}
	}
	fixed, e := fixedTargetInputs(p.Inputs, t, layers[2])
	if e != nil {
		return nil, t, k, invalid(e)
	}
	for n := range fixed {
		if _, overridden := cli[n]; overridden {
			return nil, t, k, invalid(fmt.Errorf("input %q is fixed by target %s", n, t.Path))
		}
	}
	values, e := forms.ResolvePartial(p.Inputs, layers...)
	if e != nil {
		return nil, t, k, invalid(e)
	}
	values = withFixed(values, fixed)
	if interactive {
		if s.Options.Editor == nil {
			return nil, t, k, invalid(errors.New("interactive editor is unavailable"))
		}
		seedDefs := append([]catalog.Input{}, p.Inputs...)
		if prepare && p.MCP != nil {
			if _, ok := p.MCP.Actions["prepare"]; ok {
				for n, d := range seedDefs {
					if (d.Type == "choice" || d.Type == "multichoice" || d.Type == "multiple-choice") && len(d.Options) == 0 {
						seedDefs[n].Required = false
						seedDefs[n].MinItems = nil
					}
				}
			}
		}
		visibleDefs, editableValues := withoutFixed(seedDefs, values, fixed)
		values, e = s.Options.Editor(ctx, visibleDefs, editableValues)
		if e != nil {
			return nil, t, k, e
		}
		values = withFixed(values, fixed)
		values, e = config.ResolveInputPaths(p.Inputs, values, filepath.Join(cwd, ".aact-inputs"))
		if e != nil {
			return nil, t, k, invalid(e)
		}
		if prepare && p.MCP != nil {
			if _, ok := p.MCP.Actions["prepare"]; ok {
				if e = forms.Validate(seedDefs, values); e != nil {
					return nil, t, k, invalid(e)
				}
				var result mcp.ActionResult
				e = s.Store.WithLock(ctx, func() error {
					var err error
					result, err = (&mcp.ActionRunner{Executor: s.Options.Runner, OnStderr: s.Options.OnStderr}).Run(ctx, p, mcp.ActionRequest{Action: "prepare", Target: t, Inputs: values, StateDir: s.Store.AuthDir(k), Interactive: false})
					return err
				})
				if e != nil {
					return nil, t, k, e
				}
				if len(result.Choices) > 0 {
					defs := withChoices(p.Inputs, result.Choices)
					visibleDefs, editableValues := withoutFixed(defs, values, fixed)
					values, e = s.Options.Editor(ctx, visibleDefs, editableValues)
					if e != nil {
						return nil, t, k, e
					}
					values = withFixed(values, fixed)
					values, e = config.ResolveInputPaths(defs, values, filepath.Join(cwd, ".aact-inputs"))
					if e != nil {
						return nil, t, k, invalid(e)
					}
					if e = forms.Validate(defs, values); e != nil {
						return nil, t, k, invalid(e)
					}
				}
			}
		}
	}
	values, e = forms.Resolve(p.Inputs, values)
	if e != nil {
		return nil, t, k, invalid(fmt.Errorf("%w; provide --set values or use --interactive", e))
	}
	return values, t, k, nil
}
func (s *Service) saveAnswers(k state.Key, p catalog.Package, values map[string]any) error {
	safe := map[string]any{}
	for _, d := range p.Inputs {
		if d.Type != "secret" {
			if v, ok := values[d.Name]; ok {
				safe[d.Name] = v
			}
		}
	}
	if err := s.Store.SaveAnswers(k, safe); err != nil {
		return err
	}
	if p.MCP != nil {
		return s.Store.RecordProfile(state.ProfileRecord{Key: k})
	}
	return nil
}
func registrationName(k state.Key) string {
	parts := []string{k.Package}
	if k.Environment != "" {
		parts = append(parts, k.Environment)
	}
	if k.Target != "default" {
		parts = append(parts, k.Target)
	}
	return strings.Join(parts, "-") + "-" + k.ID()[:16]
}
func (s *Service) ownedEnvironment(e agents.Environment, rows []state.Installation) agents.Environment {
	e.Owned = map[string]agents.Registration{}
	for _, r := range rows {
		if r.AgentID == e.ID && r.Component == "mcp" && r.Destination == e.ConfigPath {
			e.Owned[r.RegistrationName] = agents.Registration{Name: r.RegistrationName, URL: r.URL, Transport: r.Transport, TimeoutMS: r.TimeoutMS}
		}
	}
	return e
}
func (s *Service) Install(ctx context.Context, q InstallRequest) (out Result, err error) {
	out.Changes = []state.Installation{}
	out.Errors = []string{}
	if len(q.Agents) == 0 {
		return out, invalid(errors.New("select at least one --agent"))
	}
	p, e := s.packageByID(q.Package)
	if e != nil {
		return out, e
	}
	if e := validateGlobalSkillDestination(p, q.Agents); e != nil {
		return out, invalid(e)
	}
	if p.MCP != nil {
		for _, env := range q.Agents {
			if _, e := agents.For(env.Kind, s.Options.Runner); e != nil {
				return out, invalid(fmt.Errorf("agent %s: %w", env.ID, e))
			}
		}
	}
	refs, e := s.sourceRefs()
	if e != nil {
		return out, e
	}
	if !q.UpdateSource {
		for _, ref := range refs {
			if ref.ID == s.Source.ID && !sameSourceLocation(ref.Root, s.Source.Root) {
				return out, invalid(fmt.Errorf("source %s is registered at %s; use --update-source to select this checkout", s.Source.ID, ref.Root))
			}
		}
	}
	values, t, k, e := s.resolve(ctx, p, q.Environment, q.Target, q.Inputs, q.Interactive, q.ExternalURL == "")
	if e != nil {
		return out, e
	}
	// Save the requested configuration before applying it. An apply failure
	// leaves these answers available for correction and retry; installation
	// records below still describe only effects that actually succeeded.
	if e := s.Store.WithLock(ctx, func() error { return s.saveAnswers(k, p, values) }); e != nil {
		return out, e
	}
	generated := ""
	if p.Skill != nil && (len(p.Templates) > 0 || p.Generator != nil) {
		r := render.Renderer{Generator: &render.Generator{Executor: s.Options.Runner, OnStderr: s.Options.OnStderr}}
		stage, e := r.Stage(ctx, p, values, t, s.Store.GeneratedDir(k))
		if e != nil {
			return out, e
		}
		generated = filepath.Join(filepath.Dir(stage), "output-"+strings.TrimPrefix(filepath.Base(stage), ".aact-stage-"))
		if e = render.Publish(stage, generated); e != nil {
			os.RemoveAll(stage)
			return out, e
		}
		defer func() {
			rows, _ := s.Store.Installations()
			for _, r := range rows {
				if r.SourcePath == generated {
					return
				}
			}
			os.RemoveAll(generated)
		}()
	}
	err = s.Store.WithLock(ctx, func() error {
		applied := false
		succeeded := map[string]bool{}
		url := q.ExternalURL
		if p.MCP != nil && url == "" {
			instance, e := s.start(ctx, p, t, k, values, q.Interactive)
			if e != nil {
				return e
			}
			applied = true
			url = instance.URL
		}
		skills := install.NewSkills(s.Store)
		skills.AllowSourceUpdate = q.UpdateSource
		for _, env := range q.Agents {
			if e = ctx.Err(); e != nil {
				return e
			}
			agentErr := error(nil)
			if p.Skill != nil {
				agentErr = skills.Install(ctx, p, env, k, generated)
				if agentErr == nil {
					applied = true
					succeeded[env.ID+"\x00skill"] = true
				}
			}
			if agentErr == nil && p.MCP != nil {
				if env.ConfigPath == "" && agents.IsManual(env.Kind) {
					env.ConfigPath = s.manualConfigPath(env)
				}
				rows, e := s.Store.Installations()
				if e != nil {
					return e
				}
				env = s.ownedEnvironment(env, rows)
				adapter, e := agents.For(env.Kind, s.Options.Runner)
				if e != nil {
					agentErr = e
				} else {
					reg := agents.Registration{Name: registrationName(k), URL: url, Transport: p.MCP.Transport, TimeoutMS: 30000}
					files, snapshotErr := snapshotRegistration(env)
					agentErr = snapshotErr
					if agentErr == nil {
						agentErr = adapter.Register(ctx, env, reg)
					}
					if agentErr == nil {
						row := state.Installation{Key: k, AgentID: env.ID, AgentHome: env.Home, AgentKind: env.Kind, Component: "mcp", Destination: env.ConfigPath, Mode: "registration", RegistrationName: reg.Name, URL: reg.URL, Transport: reg.Transport, TimeoutMS: reg.TimeoutMS}
						if agents.IsManual(env.Kind) {
							row.Mode = "manual"
							out.Message = "Manual MCP configuration: " + env.ConfigPath
						}
						agentErr = s.recordRegistration(row)
						if agentErr != nil {
							agentErr = errors.Join(agentErr, restoreRegistration(files))
						} else {
							applied = true
							succeeded[env.ID+"\x00mcp"] = true
						}
					}
				}
			}
			if agentErr != nil {
				out.Errors = append(out.Errors, env.ID+": "+agentErr.Error())
			}
		}
		rows, e := s.Store.Installations()
		if e != nil {
			return e
		}
		for _, r := range rows {
			if r.Key == k && succeeded[r.AgentID+"\x00"+r.Component] {
				out.Changes = append(out.Changes, r)
			}
		}
		if applied {
			if e := s.rememberSource(); e != nil {
				return e
			}
		}
		if len(out.Errors) > 0 {
			return errors.New(strings.Join(out.Errors, "; "))
		}
		return nil
	})
	return out, err
}
func (s *Service) Uninstall(ctx context.Context, q InstallRequest) (out Result, err error) {
	out.Changes = []state.Installation{}
	out.Errors = []string{}
	if len(q.Agents) == 0 {
		return out, invalid(errors.New("select at least one --agent"))
	}
	p, e := s.packageByID(q.Package)
	if e != nil {
		return out, e
	}
	if e := validateGlobalSkillDestination(p, q.Agents); e != nil {
		return out, invalid(e)
	}
	k := s.key(q.Package, q.Environment, q.Target)
	err = s.Store.WithLock(ctx, func() error {
		skills := install.NewSkills(s.Store)
		for _, env := range q.Agents {
			if agents.IsManual(env.Kind) && env.ConfigPath == "" {
				env.ConfigPath = s.manualConfigPath(env)
			}
			rows, e := s.Store.Installations()
			if e != nil {
				return e
			}
			env = s.ownedEnvironment(env, rows)
			failed := false
			for _, r := range rows {
				if r.Key != k || r.AgentID != env.ID || r.Component != "mcp" || r.Destination != env.ConfigPath {
					continue
				}
				adapter, e := agents.For(env.Kind, s.Options.Runner)
				files, snapshotErr := snapshotRegistration(env)
				if e == nil {
					e = snapshotErr
				}
				if e == nil {
					e = adapter.Unregister(ctx, env, r.RegistrationName)
				}
				if e == nil {
					e = s.removeRegistration(r)
					if e != nil {
						e = errors.Join(e, restoreRegistration(files))
					}
				}
				if e != nil {
					out.Errors = append(out.Errors, env.ID+": "+e.Error())
					failed = true
				} else {
					out.Changes = append(out.Changes, r)
				}
			}
			if !failed {
				if e = skills.Uninstall(ctx, k, env); e != nil {
					out.Errors = append(out.Errors, env.ID+": "+e.Error())
				}
			}
		}
		if len(out.Errors) > 0 {
			return errors.New(strings.Join(out.Errors, "; "))
		}
		return nil
	})
	return out, err
}
func (s *Service) start(ctx context.Context, p catalog.Package, t config.Target, k state.Key, values map[string]any, interactive bool) (mcp.Instance, error) {
	if p.MCP == nil {
		return mcp.Instance{}, invalid(errors.New("package has no MCP"))
	}
	spec := mcp.RunSpec{Image: p.MCP.Image, Host: "127.0.0.1", ContainerPort: p.MCP.ContainerPort, Transport: p.MCP.Transport, EndpointPath: p.MCP.EndpointPath}
	if p.MCP.BuildContext != "" {
		spec.BuildContext = filepath.Join(p.Dir, p.MCP.BuildContext)
	}
	switch port := values[p.MCP.HostPortInput].(type) {
	case int64:
		spec.HostPort = int(port)
	case int:
		spec.HostPort = port
	}
	if h, ok := values["host"].(string); ok && h != "" {
		spec.Host = h
	}
	if _, ok := p.MCP.Actions["prepare"]; ok {
		runner := mcp.ActionRunner{Executor: s.Options.Runner, OnStderr: s.Options.OnStderr}
		result, e := runner.Run(ctx, p, mcp.ActionRequest{Action: "prepare", Target: t, Inputs: values, StateDir: s.Store.AuthDir(k), Interactive: interactive})
		if e != nil {
			return mcp.Instance{}, e
		}
		if result.AuthRequired {
			return mcp.Instance{}, fmt.Errorf("%s authentication required; run aact mcp authenticate %s with credentials or --interactive", p.ID, p.ID)
		}
		if e = forms.Validate(withChoices(p.Inputs, result.Choices), values); e != nil {
			return mcp.Instance{}, invalid(e)
		}
		if result.Runtime == nil {
			return mcp.Instance{}, errors.New("prepare action did not return runtime settings")
		}
		spec = *result.Runtime
	}
	instance, err := s.Options.Runtime.Start(ctx, k, spec)
	if errors.Is(err, mcp.ErrRunningWithDifferentSettings) {
		if stopErr := s.Options.Runtime.Stop(ctx, k); stopErr != nil {
			return mcp.Instance{}, fmt.Errorf("apply changed MCP settings: stop old instance: %w", stopErr)
		}
		return s.Options.Runtime.Start(ctx, k, spec)
	}
	return instance, err
}
func (s *Service) MCP(ctx context.Context, q MCPRequest) (out Result, err error) {
	switch q.Action {
	case "list", "status":
		out.Instances, err = s.Options.Runtime.List(ctx)
		return out, err
	}
	p, e := s.packageByID(q.Package)
	if e != nil {
		return out, e
	}
	k := s.key(p.ID, q.Environment, q.Target)
	switch q.Action {
	case "stop":
		err = s.Store.WithLock(ctx, func() error { return s.Options.Runtime.Stop(ctx, k) })
		return out, err
	case "logs":
		r, e := s.Options.Runtime.Logs(ctx, k)
		if e != nil {
			return out, e
		}
		defer r.Close()
		b, e := io.ReadAll(r)
		out.Logs = string(b)
		return out, e
	case "start", "authenticate", "prepare":
		values, t, k, e := s.resolve(ctx, p, q.Environment, q.Target, q.Inputs, q.Interactive, q.Action == "start" || q.Action == "prepare")
		if e != nil {
			return out, e
		}
		err = s.Store.WithLock(ctx, func() error {
			if e := s.rememberSource(); e != nil {
				return e
			}
			if q.Action == "start" {
				i, e := s.start(ctx, p, t, k, values, q.Interactive)
				out.Instances = []mcp.Instance{i}
				if e != nil {
					return e
				}
			} else {
				runner := mcp.ActionRunner{Executor: s.Options.Runner, OnStderr: s.Options.OnStderr}
				r, e := runner.Run(ctx, p, mcp.ActionRequest{Action: q.Action, Target: t, Inputs: values, StateDir: s.Store.AuthDir(k), Interactive: q.Interactive})
				if e != nil {
					return e
				}
				if r.AuthRequired {
					return errors.New("authentication required; provide credentials or use --interactive")
				}
			}
			return s.saveAnswers(k, p, values)
		})
		return out, err
	default:
		return out, invalid(fmt.Errorf("unknown MCP action %q", q.Action))
	}
}
