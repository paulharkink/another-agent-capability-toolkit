package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/agents"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/forms"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/mcp"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/process"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/versioninfo"
	"io"
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
	Adapters           AdapterProvider
	AgentScopes        map[string]agents.Scope
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

// authenticationRequiredMessage keeps provider-specific diagnostics first so
// retry guidance does not bury the cause. An empty diagnostic keeps the
// established user-facing fallback unchanged.
func authenticationRequiredMessage(packageID, diagnostic string) string {
	retry := fmt.Sprintf("run aact mcp authenticate %s with credentials or --interactive", packageID)
	if diagnostic = strings.TrimSpace(diagnostic); diagnostic != "" {
		return diagnostic + "\nTo retry, " + retry
	}
	return fmt.Sprintf("%s authentication required; %s", packageID, retry)
}

type authenticationRequiredFailure struct{ message string }

func (e authenticationRequiredFailure) Error() string { return e.message }

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

func (s *Service) mcpImageChoice(ctx context.Context, definition catalog.MCP) (string, string, error) {
	if definition.ReleaseImage == "" {
		return "local", "", nil
	}
	settings, err := s.UISettings(ctx)
	if err != nil {
		return "", "", err
	}
	source := settings["image_source"]
	if source == "" {
		source = "release"
	}
	if source == "local" {
		return source, "", nil
	}
	image, err := versioninfo.ResolveImage(definition.ReleaseImage)
	if err != nil {
		return "", "", err
	}
	return "release", image, nil
}

type InstallRequest struct {
	Ref                          config.ProfileRef
	Package, Environment, Target string
	Agents                       []agents.Environment
	Inputs                       map[string]any
	ResetInputs                  []string
	Interactive                  bool
	SkillsOnly                   bool
	ExternalURL                  string
	ExternalURLs                 map[string]string
	UpdateSource                 bool
}
type MCPRequest struct {
	Ref                                           config.ProfileRef
	Action, Package, Environment, Target, Profile string
	MCP                                           string
	Inputs                                        map[string]any
	Interactive                                   bool
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

func packageMCPProfiles(p catalog.Package, values map[string]any) []catalog.MCP {
	profiles := p.MCPProfiles()
	enabled := make([]catalog.MCP, 0, len(profiles))
	for _, profile := range profiles {
		if profile.EnabledInput != "" {
			value, ok := values[profile.EnabledInput].(bool)
			if !ok || !value {
				continue
			}
		}
		enabled = append(enabled, profile)
	}
	return enabled
}

func sameCapabilityKey(a, b state.Key) bool {
	return a.Source == b.Source && a.Package == b.Package && a.Environment == b.Environment && a.Target == b.Target
}
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
	return s.saveAnswersWithReset(k, p, values, skillsOnly, nil)
}

func (s *Service) saveAnswersWithReset(k state.Key, p catalog.Package, values map[string]any, skillsOnly bool, resetInputs []string) error {
	return s.saveAnswersWithActiveGroups(k, p, values, skillsOnly, resetInputs, nil)
}

func (s *Service) saveAnswersWithActiveGroups(k state.Key, p catalog.Package, values map[string]any, skillsOnly bool, resetInputs []string, activeGroups map[string]string) error {
	reset := make(map[string]bool, len(resetInputs))
	for _, name := range resetInputs {
		reset[name] = true
	}
	safe := map[string]any{}
	for _, d := range p.Inputs {
		if d.Type != "secret" && !reset[d.Name] {
			if v, ok := values[d.Name]; ok {
				safe[d.Name] = v
			}
		}
	}
	if err := s.Store.SaveAnswersWithActiveInputGroups(k, safe, activeGroups); err != nil {
		return err
	}
	if p.HasMCP() && !skillsOnly {
		for _, profile := range packageMCPProfiles(p, values) {
			if err := s.Store.RecordProfile(state.ProfileRecord{Key: mcpProfileKey(k, p, profile)}); err != nil {
				return err
			}
		}
	}
	return nil
}

func hasSubmittedAuthentication(s *Service, p catalog.Package, k state.Key, submitted, previous map[string]any) bool {
	for _, input := range p.Inputs {
		if input.Type == "secret" && submitted[input.Name] != nil && submitted[input.Name] != "" {
			return true
		}
		if input.Type == "file" {
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
	if k.Profile != "" {
		parts = append(parts, k.Profile)
	} else if k.MCP != "" {
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
	if k.Profile != "" {
		identity += " profile " + k.Profile
	} else if k.MCP != "" {
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
func (s *Service) Install(ctx context.Context, q InstallRequest) (Result, error) {
	if q.Ref.CapabilityID == "" || q.Ref.Name == "" {
		return Result{}, invalid(errors.New("profile reference (Capability Pack, capability, and profile) is required"))
	}
	destinations := make([]string, 0, len(q.Agents))
	for _, agent := range q.Agents {
		destinations = append(destinations, agent.ID)
	}
	result, err := s.ApplyProfile(ctx, ProfileRequest{Ref: q.Ref, Inputs: q.Inputs, ResetInputs: q.ResetInputs, DestinationIDs: destinations, SkillsOnly: q.SkillsOnly, Interactive: q.Interactive, ExternalURLs: q.ExternalURLs})
	return Result{Changes: result.Changes, Errors: result.Errors, Saved: result.Saved, Message: result.Message, Step: result.Step, Target: result.Target}, err
}

// installCompatibility is retained for package-internal migration tests only.
// Production setup/apply entrypoints select capabilities through ProfileRef.
func (s *Service) validateRegistrationNames(p catalog.Package, definitions []catalog.MCP, key state.Key, values map[string]any, destinations []agents.Environment) error {
	used := map[string]string{}
	for _, definition := range definitions {
		child := mcpProfileKey(key, p, definition)
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
			child := mcpProfileKey(key, p, definition)
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
func (s *Service) Uninstall(ctx context.Context, q InstallRequest) (Result, error) {
	if q.Ref.CapabilityID == "" || q.Ref.Name == "" {
		return Result{}, invalid(errors.New("profile reference (Capability Pack, capability, and profile) is required"))
	}
	result, err := s.ApplyProfile(ctx, ProfileRequest{Ref: q.Ref, ItemIDs: []string{}, DestinationIDs: []string{}})
	return Result{Changes: result.Changes, Errors: result.Errors, Saved: result.Saved, Message: result.Message, Step: result.Step, Target: result.Target}, err
}

func (s *Service) startConfigurationProfile(ctx context.Context, p catalog.Package, profile config.Profile, k state.Key, values map[string]any, interactive bool) (mcp.Instance, error) {
	if p.MCP == nil {
		return mcp.Instance{}, invalid(errors.New("package has no MCP"))
	}
	resolvedRegistrationName, nameErr := declaredRegistrationName(*p.MCP, k, values)
	if nameErr != nil {
		return mcp.Instance{}, invalid(nameErr)
	}
	spec := mcp.RunSpec{Image: p.MCP.Image, Args: append([]string(nil), p.MCP.Args...), ContainerPort: p.MCP.ContainerPort, Transport: p.MCP.Transport, EndpointPath: p.MCP.EndpointPath}
	spec.RegistrationName = resolvedRegistrationName
	applyDeclaredRuntimeHosts := func() {
		if p.MCP.BindIPInput != "" {
			spec.BindIP, _ = values[p.MCP.BindIPInput].(string)
		}
		if p.MCP.AdvertisedHostInput != "" {
			spec.AdvertisedHost, _ = values[p.MCP.AdvertisedHostInput].(string)
		}
	}
	applyDeclaredRuntimeHosts()
	for name, value := range p.MCP.Env {
		if spec.Env == nil {
			spec.Env = map[string]string{}
		}
		spec.Env[name] = value
	}
	for name, inputName := range p.MCP.EnvInputs {
		if value, ok := values[inputName]; ok && value != nil {
			if spec.Env == nil {
				spec.Env = map[string]string{}
			}
			spec.Env[name] = fmt.Sprint(value)
		}
	}
	for name, inputName := range p.MCP.SecretEnvInputs {
		if value, ok := values[inputName].(string); ok && strings.TrimSpace(value) != "" {
			if spec.SecretEnv == nil {
				spec.SecretEnv = map[string]string{}
			}
			spec.SecretEnv[name] = value
		}
	}
	if p.MCP.TokenContainerEnv != "" {
		token, tokenErr := resolveProfileToken(*p.MCP, values)
		if tokenErr != nil {
			return mcp.Instance{}, operationFailure{step: "authenticate", err: tokenErr}
		}
		if spec.SecretEnv == nil {
			spec.SecretEnv = map[string]string{}
		}
		spec.SecretEnv[p.MCP.TokenContainerEnv] = token
	}
	if p.MCP.BuildContext != "" {
		spec.BuildContext = p.MCP.BuildContext
		if !filepath.IsAbs(spec.BuildContext) {
			spec.BuildContext = filepath.Join(p.Dir, spec.BuildContext)
		}
	}
	switch port := values[p.MCP.HostPortInput].(type) {
	case int64:
		spec.HostPort = int(port)
	case int:
		spec.HostPort = port
	}
	if _, ok := p.MCP.Actions["prepare"]; ok {
		runner := mcp.ActionRunner{Executor: s.Options.Runner, OnStderr: s.Options.OnStderr}
		imageSource, releaseImage, choiceErr := s.mcpImageChoice(ctx, *p.MCP)
		if choiceErr != nil {
			return mcp.Instance{}, operationFailure{step: "prepare", err: choiceErr}
		}
		result, e := runner.Run(ctx, p, mcp.ActionRequest{Action: "prepare", Profile: profile, Inputs: values, StateDir: s.Store.AuthDir(k), Interactive: interactive, ImageSource: imageSource, ReleaseImage: releaseImage})
		if e != nil {
			return mcp.Instance{}, operationFailure{step: "prepare", err: e}
		}
		if result.AuthRequired {
			return mcp.Instance{}, operationFailure{step: "prepare", err: authenticationRequiredFailure{message: authenticationRequiredMessage(p.ID, result.Diagnostic)}}
		}
		if e = forms.Validate(withChoices(p.Inputs, result.Choices), values); e != nil {
			return mcp.Instance{}, operationFailure{step: "prepare", err: invalid(e)}
		}
		if result.Runtime == nil {
			return mcp.Instance{}, operationFailure{step: "prepare", err: errors.New("prepare action did not return runtime settings")}
		}
		spec = *result.Runtime
		spec.RegistrationName = resolvedRegistrationName
		applyDeclaredRuntimeHosts()
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

func (s *Service) MCP(ctx context.Context, q MCPRequest) (Result, error) {
	if q.Action != "list" && q.Action != "status" && (q.Ref.CapabilityID == "" || q.Ref.Name == "") {
		return Result{}, invalid(errors.New("profile reference (Capability Pack, capability, and profile) is required"))
	}
	return s.RunProfileMCP(ctx, q.Action, ProfileRequest{Ref: q.Ref, Inputs: q.Inputs, Interactive: q.Interactive}, q.MCP)
}
