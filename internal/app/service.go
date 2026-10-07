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
	"github.com/paulharkink/another-agent-capability-toolkit/internal/picker"
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
	DiscoveryProbe     *agents.DiscoveryProbe
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

func registrationTimeoutMS(m *catalog.MCP) int {
	if m != nil && m.RegistrationTimeoutMS > 0 {
		return m.RegistrationTimeoutMS
	}
	return 30000
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
	SkillsOnly                   bool
	ExternalURL                  string
	ExternalURLs                 map[string]string
	UpdateSource                 bool
}
type MCPRequest struct {
	Action, Package, Environment, Target string
	MCP                                  string
	Inputs                               map[string]any
	Interactive                          bool
}
type Result struct {
	Changes   []state.Installation `json:"changes"`
	Errors    []string             `json:"errors"`
	Saved     bool                 `json:"saved"`
	Instances []mcp.Instance       `json:"instances,omitempty"`
	Logs      string               `json:"logs,omitempty"`
	Message   string               `json:"message,omitempty"`
	Step      string               `json:"step,omitempty"`
	Target    string               `json:"target,omitempty"`
}
type InvalidInput struct{ Err error }

type operationFailure struct {
	step string
	err  error
}

func (e operationFailure) Error() string { return e.err.Error() }
func (e operationFailure) Unwrap() error { return e.err }

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

func sameCapabilityKey(a, b state.Key) bool {
	return a.Source == b.Source && a.Package == b.Package && a.Environment == b.Environment && a.Target == b.Target
}
func (s *Service) resolve(ctx context.Context, p catalog.Package, env, target string, cli map[string]any, interactive, prepare, skillsOnly bool) (map[string]any, config.Target, state.Key, error) {
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
		seedDefs := skillInstallInputs(p, skillsOnly)
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
		visibleDefs, editableValues := editableWithFixedContext(seedDefs, values, fixed)
		if len(visibleDefs) > 0 {
			edited, editErr := s.Options.Editor(ctx, visibleDefs, editableValues)
			e = editErr
			if e != nil {
				return nil, t, k, e
			}
			values = mergeEditedValues(values, edited)
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
					visibleDefs, editableValues := editableWithFixedContext(defs, values, fixed)
					if len(visibleDefs) > 0 {
						edited, editErr := s.Options.Editor(ctx, visibleDefs, editableValues)
						e = editErr
						if e != nil {
							return nil, t, k, e
						}
						values = mergeEditedValues(values, edited)
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
	values, e = forms.Resolve(skillInstallInputs(p, skillsOnly), values)
	if e != nil {
		return nil, t, k, invalid(fmt.Errorf("%w; provide --set values or use --interactive", e))
	}
	return values, t, k, nil
}

// skillInstallInputs keeps MCP-only target inputs optional when a package is
// installed solely for its skill. Skill templates may still consume configured
// values, but a skill install does not need an MCP endpoint or credentials.
func skillInstallInputs(p catalog.Package, skillsOnly bool) []catalog.Input {
	defs := append([]catalog.Input{}, p.Inputs...)
	if !skillsOnly || p.MCP == nil {
		return defs
	}
	for n := range defs {
		if defs[n].ConfigKey != "" || defs[n].ExclusiveGroup != "" {
			defs[n].Required = false
			defs[n].MinItems = nil
		}
	}
	return defs
}
func (s *Service) saveAnswers(k state.Key, p catalog.Package, values map[string]any, skillsOnly bool) error {
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
	if p.MCP != nil && !skillsOnly {
		return s.Store.RecordProfile(state.ProfileRecord{Key: k})
	}
	return nil
}

func hasSubmittedAuthentication(s *Service, p catalog.Package, k state.Key, submitted, previous map[string]any) bool {
	for _, input := range p.Inputs {
		if input.Type == "secret" && submitted[input.Name] != nil && submitted[input.Name] != "" {
			return true
		}
		if input.Type == "file" && (input.Name == "kubeconfig" || strings.Contains(strings.ToLower(input.Label), "kubeconfig")) {
			value, ok := submitted[input.Name].(string)
			if !ok || value == "" {
				continue
			}
			oldValue, _ := previous[input.Name].(string)
			if value == oldValue {
				state, _ := s.credentialObservation(p, k)
				if state == "present" {
					continue
				}
			}
			return true
		}
	}
	return false
}
func registrationName(k state.Key) string {
	parts := []string{k.Package}
	if k.Environment != "" {
		parts = append(parts, k.Environment)
	}
	if k.Target != "default" {
		parts = append(parts, k.Target)
	}
	if k.MCP != "" {
		parts = append(parts, k.MCP)
	}
	return strings.Join(parts, "-")
}

func declaredRegistrationName(definition catalog.MCP, key state.Key, inputs map[string]any) (string, error) {
	name := ""
	if definition.RegistrationNameInput != "" {
		value, ok := inputs[definition.RegistrationNameInput]
		if !ok {
			return "", fmt.Errorf("MCP %s registration name input %q is missing", definition.Name, definition.RegistrationNameInput)
		}
		var isString bool
		name, isString = value.(string)
		if !isString {
			return "", fmt.Errorf("MCP %s registration name input %q must be a string", definition.Name, definition.RegistrationNameInput)
		}
	} else {
		name = registrationName(key)
	}
	if name == "" || strings.HasPrefix(name, "-") || strings.ContainsAny(name, "\r\n\x00") {
		return "", fmt.Errorf("MCP %s registration name %q is invalid", definition.Name, name)
	}
	return name, nil
}

func operationTarget(k state.Key) string {
	target := k.Target
	if target == "" {
		target = "default"
	}
	identity := k.Source + "/" + k.Package
	if k.MCP != "" {
		identity += " MCP " + k.MCP
	}
	if k.Environment != "" {
		return identity + " — " + k.Environment + "/" + target
	}
	return identity + " — " + target
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
	mcpDefinitions := p.MCPDefinitions()
	multiMCP := len(p.MCPs) > 0
	if p.MCP == nil && len(mcpDefinitions) > 0 {
		primary := mcpDefinitions[0]
		p.MCP = &primary
	}
	if multiMCP {
		for name := range q.ExternalURLs {
			found := false
			for _, definition := range mcpDefinitions {
				if definition.Name == name {
					found = true
					break
				}
			}
			if !found {
				return out, invalid(fmt.Errorf("endpoint supplied for unknown MCP %q", name))
			}
		}
		if q.ExternalURL != "" {
			return out, invalid(errors.New("a single external URL cannot attach a multi-MCP capability; provide one endpoint per MCP"))
		}
	} else if len(q.ExternalURLs) > 0 {
		return out, invalid(errors.New("named external URLs require a multi-MCP capability"))
	}
	if q.SkillsOnly && p.Skill == nil {
		return out, invalid(errors.New("package has no skill to install with --skills-only"))
	}
	if e := validateGlobalSkillDestination(p, q.Agents); e != nil {
		return out, invalid(e)
	}
	if p.MCP != nil && !q.SkillsOnly {
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
	wantsRuntime := false
	resolutionPackage := p
	for _, definition := range mcpDefinitions {
		provided := q.ExternalURLs[definition.Name]
		if !multiMCP {
			provided = q.ExternalURL
		}
		if provided == "" {
			wantsRuntime = true
			if resolutionPackage.MCP == nil || (multiMCP && resolutionPackage.MCP.Name != definition.Name) {
				resolutionPackage.MCP = &definition
			}
		}
	}
	values, t, k, e := s.resolve(ctx, resolutionPackage, q.Environment, q.Target, q.Inputs, q.Interactive, wantsRuntime, q.SkillsOnly)
	if e != nil {
		return out, e
	}
	if !q.SkillsOnly && len(mcpDefinitions) > 0 {
		if e := s.validateRegistrationNames(p, mcpDefinitions, k, values, q.Agents); e != nil {
			return out, invalid(e)
		}
	}
	previousAnswers, e := s.Store.Answers(k)
	if e != nil {
		return out, e
	}
	// Save the requested configuration before applying it. An apply failure
	// leaves these answers available for correction and retry; installation
	// records below still describe only effects that actually succeeded.
	if e := s.Store.WithLock(ctx, func() error { return s.saveAnswers(k, p, values, q.SkillsOnly) }); e != nil {
		return out, e
	}
	out.Saved = true
	generated := ""
	if p.Skill != nil && (len(p.Templates) > 0 || p.Generator != nil) {
		out.Step = "generate"
		reportOperationStep(ctx, out.Step)
		out.Target = operationTarget(k)
		r := render.Renderer{Generator: &render.Generator{Executor: s.Options.Runner, OnStderr: s.Options.OnStderr}}
		stage, e := r.Stage(ctx, p, values, t, s.Store.GeneratedDir(k))
		if e != nil {
			return cancellationResult(out, e)
		}
		generated = filepath.Join(filepath.Dir(stage), "output-"+strings.TrimPrefix(filepath.Base(stage), ".aact-stage-"))
		if e = render.Publish(stage, generated); e != nil {
			os.RemoveAll(stage)
			return cancellationResult(out, e)
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
		successfulRows := []state.Installation{}
		var cancellation error
		urls := map[string]string{}
		for _, definition := range mcpDefinitions {
			url := q.ExternalURLs[definition.Name]
			if !multiMCP {
				url = q.ExternalURL
			}
			if url != "" {
				urls[definition.Name] = url
				continue
			}
			if q.SkillsOnly {
				continue
			}
			mcpKey := k
			packageForMCP := p
			packageForMCP.MCP = &definition
			if multiMCP {
				mcpKey.MCP = definition.Name
			}
			if _, hasAuthenticate := definition.Actions["authenticate"]; hasAuthenticate && hasSubmittedAuthentication(s, packageForMCP, mcpKey, q.Inputs, previousAnswers) {
				out.Step = "authenticate"
				reportOperationStep(ctx, out.Step)
				out.Target = operationTarget(mcpKey)
				runner := mcp.ActionRunner{Executor: s.Options.Runner, OnStderr: s.Options.OnStderr}
				auth, authErr := runner.Run(ctx, packageForMCP, mcp.ActionRequest{Action: "authenticate", Target: t, Inputs: values, StateDir: s.Store.AuthDir(mcpKey), Interactive: false})
				if authErr != nil {
					if ordinaryCancellation(authErr) {
						return picker.ErrCancelled
					}
					return authErr
				}
				if auth.AuthRequired {
					return fmt.Errorf("%s authentication action still requires authentication", p.ID)
				}
			}
			out.Step = "start"
			reportOperationStep(ctx, out.Step)
			out.Target = operationTarget(mcpKey)
			instance, startErr := s.start(ctx, packageForMCP, t, mcpKey, values, q.Interactive)
			if startErr != nil {
				var failure operationFailure
				if errors.As(startErr, &failure) {
					out.Step = failure.step
					reportOperationStep(ctx, out.Step)
				}
				return startErr
			}
			applied = true
			urls[definition.Name] = instance.URL
		}
		skills := install.NewSkills(s.Store)
		skills.AllowSourceUpdate = q.UpdateSource
		for _, env := range q.Agents {
			out.Step = "register"
			reportOperationStep(ctx, out.Step)
			if p.Skill != nil {
				out.Step = "install"
				reportOperationStep(ctx, out.Step)
			}
			out.Target = operationTarget(k)
			if e = ctx.Err(); e != nil {
				cancellation = e
				break
			}
			agentErr := error(nil)
			if p.Skill != nil {
				agentErr = skills.Install(ctx, p, env, k, generated)
				if agentErr == nil {
					applied = true
					destination, absErr := filepath.Abs(filepath.Join(env.SkillsDir, p.Skill.Name))
					if absErr != nil {
						return absErr
					}
					rows, rowsErr := s.Store.Installations()
					if rowsErr != nil {
						return rowsErr
					}
					for _, row := range rows {
						if row.Key == k && row.AgentID == env.ID && row.Component == "skill" && row.Destination == destination {
							successfulRows = append(successfulRows, row)
							break
						}
					}
				}
			}
			if agentErr == nil && len(mcpDefinitions) > 0 && !q.SkillsOnly {
				for _, definition := range mcpDefinitions {
					out.Step = "register"
					reportOperationStep(ctx, out.Step)
					mcpKey := k
					if multiMCP {
						mcpKey.MCP = definition.Name
					}
					out.Target = operationTarget(mcpKey)
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
						name, nameErr := declaredRegistrationName(definition, mcpKey, values)
						if nameErr != nil {
							agentErr = nameErr
							break
						}
						for _, old := range rows {
							if sameCapabilityKey(old.Key, mcpKey) && old.Key.MCP == mcpKey.MCP && old.AgentID == env.ID && old.Component == "mcp" && old.Destination == env.ConfigPath && old.RegistrationName != name {
								files, snapshotErr := snapshotRegistration(env)
								if snapshotErr == nil {
									snapshotErr = adapter.Unregister(ctx, env, old.RegistrationName)
								}
								if snapshotErr == nil {
									snapshotErr = s.removeRegistration(old)
								}
								if snapshotErr != nil {
									agentErr = errors.Join(fmt.Errorf("remove previous registration %q before renaming: %w", old.RegistrationName, snapshotErr), restoreRegistration(files))
									break
								}
								successfulRows = append(successfulRows, old)
								rows, e = s.Store.Installations()
								if e != nil {
									agentErr = e
									break
								}
								env = s.ownedEnvironment(env, rows)
								break
							}
						}
						if agentErr != nil {
							break
						}
						reg := agents.Registration{Name: name, URL: urls[definition.Name], Transport: definition.Transport, TimeoutMS: registrationTimeoutMS(&definition)}
						files, snapshotErr := snapshotRegistration(env)
						agentErr = snapshotErr
						if agentErr == nil {
							agentErr = adapter.Register(ctx, env, reg)
						}
						if agentErr == nil {
							row := state.Installation{Key: mcpKey, AgentID: env.ID, AgentHome: env.Home, AgentKind: env.Kind, Component: "mcp", Destination: env.ConfigPath, Mode: "registration", RegistrationName: reg.Name, URL: reg.URL, Transport: reg.Transport, TimeoutMS: reg.TimeoutMS, ExternalRegistration: (multiMCP && q.ExternalURLs[definition.Name] != "") || (!multiMCP && q.ExternalURL != "")}
							if agents.IsManual(env.Kind) {
								row.Mode = "manual"
								out.Message = "Manual MCP configuration: " + env.ConfigPath
							}
							agentErr = s.recordRegistration(row)
							if agentErr != nil {
								agentErr = errors.Join(agentErr, restoreRegistration(files))
							} else {
								applied = true
								successfulRows = append(successfulRows, row)
							}
						}
					}
					if agentErr != nil {
						if multiMCP {
							agentErr = fmt.Errorf("MCP %s: %w", definition.Name, agentErr)
						}
						break
					}
				}
			}
			if agentErr != nil {
				if ordinaryCancellation(agentErr) {
					cancellation = agentErr
					break
				}
				out.Errors = append(out.Errors, env.ID+": "+agentErr.Error())
			}
		}
		out.Changes = append(out.Changes, successfulRows...)
		if applied {
			if e := s.rememberSource(); e != nil {
				return e
			}
		}
		if len(out.Errors) > 0 {
			applyErr := errors.New(strings.Join(out.Errors, "; "))
			if cancellation != nil {
				return errors.Join(applyErr, picker.ErrCancelled)
			}
			return applyErr
		}
		if cancellation != nil {
			return picker.ErrCancelled
		}
		return nil
	})
	return cancellationResult(out, err)
}

func (s *Service) validateRegistrationNames(p catalog.Package, definitions []catalog.MCP, key state.Key, values map[string]any, destinations []agents.Environment) error {
	used := map[string]string{}
	for _, definition := range definitions {
		child := key
		if len(p.MCPs) > 0 {
			child.MCP = definition.Name
		}
		name, err := declaredRegistrationName(definition, child, values)
		if err != nil {
			return err
		}
		if prior, exists := used[name]; exists {
			return fmt.Errorf("MCP registrations %s and %s resolve to the same name %q; give each service a distinct registration name", prior, definition.Name, name)
		}
		used[name] = definition.Name
	}
	rows, err := s.Store.Installations()
	if err != nil {
		return err
	}
	for _, env := range destinations {
		for _, definition := range definitions {
			child := key
			if len(p.MCPs) > 0 {
				child.MCP = definition.Name
			}
			name, _ := declaredRegistrationName(definition, child, values)
			for _, row := range rows {
				if row.Component != "mcp" || row.AgentID != env.ID || filepath.Clean(row.Destination) != filepath.Clean(env.ConfigPath) || row.RegistrationName != name {
					continue
				}
				if row.Key == child {
					continue
				}
				return fmt.Errorf("MCP registration name %q is already used by %s/%s for %s; choose a different declared registration name", name, row.Key.Source, row.Key.Package, row.Key.Target)
			}
		}
	}
	return nil
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
				if !sameCapabilityKey(r.Key, k) || r.AgentID != env.ID || r.Component != "mcp" || r.Destination != env.ConfigPath {
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
			return mcp.Instance{}, operationFailure{step: "prepare", err: e}
		}
		if result.AuthRequired {
			return mcp.Instance{}, operationFailure{step: "prepare", err: fmt.Errorf("%s authentication required; run aact mcp authenticate %s with credentials or --interactive", p.ID, p.ID)}
		}
		if e = forms.Validate(withChoices(p.Inputs, result.Choices), values); e != nil {
			return mcp.Instance{}, operationFailure{step: "prepare", err: invalid(e)}
		}
		if result.Runtime == nil {
			return mcp.Instance{}, operationFailure{step: "prepare", err: errors.New("prepare action did not return runtime settings")}
		}
		spec = *result.Runtime
	}
	instance, err := s.Options.Runtime.Start(ctx, k, spec)
	if errors.Is(err, mcp.ErrRunningWithDifferentSettings) {
		if stopErr := s.Options.Runtime.Stop(ctx, k); stopErr != nil {
			return mcp.Instance{}, operationFailure{step: "start", err: fmt.Errorf("apply changed MCP settings: stop old instance: %w", stopErr)}
		}
		instance, err = s.Options.Runtime.Start(ctx, k, spec)
	}
	if err != nil {
		return instance, operationFailure{step: "start", err: err}
	}
	return instance, nil
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
	definitions := p.MCPDefinitions()
	k := s.key(p.ID, q.Environment, q.Target)
	if q.MCP != "" {
		found := false
		for _, definition := range definitions {
			if definition.Name == q.MCP {
				found = true
				p.MCP = &definition
				break
			}
		}
		if !found {
			return out, invalid(fmt.Errorf("unknown MCP %q for package %s", q.MCP, p.ID))
		}
		if len(p.MCPs) > 0 {
			k.MCP = q.MCP
		}
	} else if len(definitions) == 1 {
		definition := definitions[0]
		p.MCP = &definition
		if len(p.MCPs) > 0 {
			k.MCP = definition.Name
		}
	} else if len(definitions) > 1 && q.Action != "start" && q.Action != "stop" {
		return out, invalid(errors.New("select an MCP name for this action"))
	}
	switch q.Action {
	case "stop":
		err = s.Store.WithLock(ctx, func() error {
			if q.MCP != "" || len(definitions) <= 1 {
				return s.Options.Runtime.Stop(ctx, k)
			}
			var stopErrors []error
			for _, definition := range definitions {
				child := k
				child.MCP = definition.Name
				if stopErr := s.Options.Runtime.Stop(ctx, child); stopErr != nil {
					stopErrors = append(stopErrors, stopErr)
				}
			}
			return errors.Join(stopErrors...)
		})
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
		values, t, k, e := s.resolve(ctx, p, q.Environment, q.Target, q.Inputs, q.Interactive, q.Action == "start" || q.Action == "prepare", false)
		if e != nil {
			return out, e
		}
		answerKey := k
		if len(p.MCPs) > 0 {
			if q.MCP != "" {
				k.MCP = q.MCP
			} else if len(definitions) == 1 {
				k.MCP = definitions[0].Name
			}
		}
		err = s.Store.WithLock(ctx, func() error {
			if e := s.rememberSource(); e != nil {
				return e
			}
			if q.Action == "start" {
				startDefinitions := definitions
				if q.MCP != "" {
					for _, definition := range definitions {
						if definition.Name == q.MCP {
							startDefinitions = []catalog.MCP{definition}
							break
						}
					}
				}
				for _, definition := range startDefinitions {
					child, childPackage := k, p
					childPackage.MCP = &definition
					if q.MCP == "" && len(p.MCPs) > 0 {
						child.MCP = definition.Name
					}
					i, startErr := s.start(ctx, childPackage, t, child, values, q.Interactive)
					if startErr != nil {
						return startErr
					}
					out.Instances = append(out.Instances, i)
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
			return s.saveAnswers(answerKey, p, values, false)
		})
		return out, err
	default:
		return out, invalid(fmt.Errorf("unknown MCP action %q", q.Action))
	}
}
