package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/agents"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/mcp"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/picker"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

type uxActionCall struct {
	Action      string         `json:"action"`
	Inputs      map[string]any `json:"inputs"`
	Interactive bool           `json:"interactive"`
}

func profileTestAgent(t *testing.T, svc *Service, id string) (config.ProfileRef, string) {
	t.Helper()
	home := t.TempDir()
	configPath := filepath.Join(home, "agent.jsonc")
	if svc.Options.AgentScopes == nil {
		svc.Options.AgentScopes = map[string]agents.Scope{}
	}
	svc.Options.AgentScopes[id] = agents.Scope{ID: id, Home: home, ConfigPathOverride: configPath, ExplicitHome: true}
	return writeProfileForTest(t, svc, "demo", "default", ""), id
}

type uxReplacementRegistry struct {
	fallback     AdapterProvider
	replacements map[string]agents.Adapter
}

func (r uxReplacementRegistry) Adapter(id string) (agents.Adapter, error) {
	base, _, _ := strings.Cut(id, ":")
	if adapter := r.replacements[base]; adapter != nil {
		return adapter, nil
	}
	return r.fallback.Adapter(id)
}
func (r uxReplacementRegistry) Adapters() []agents.Adapter {
	rows := []agents.Adapter{}
	added := map[string]bool{}
	for _, adapter := range r.fallback.Adapters() {
		if replacement := r.replacements[adapter.ID()]; replacement != nil {
			rows = append(rows, replacement)
			added[adapter.ID()] = true
		} else {
			rows = append(rows, adapter)
		}
	}
	for id, adapter := range r.replacements {
		if !added[id] {
			rows = append(rows, adapter)
		}
	}
	return rows
}

type uxCancelDuringRegisterAdapter struct {
	agents.Adapter
	manager agents.MCPManager
	cancel  context.CancelFunc
}

func (a *uxCancelDuringRegisterAdapter) Register(_ context.Context, scope agents.Scope, request agents.MCPRequest) (agents.MCPRegistrationResult, error) {
	a.cancel()
	return a.manager.Register(context.Background(), scope, request)
}
func (a *uxCancelDuringRegisterAdapter) Unregister(ctx context.Context, scope agents.Scope, row state.Installation) error {
	return a.manager.Unregister(ctx, scope, row)
}

type uxActionExecutor struct {
	calls        []uxActionCall
	failOn       string
	stderr       string
	cancel       context.CancelFunc
	authRequired bool
	authCalls    int
	failNthAuth  int
}

type uxBlockingProgressExecutor struct {
	started chan struct{}
	release chan struct{}
}

func (e *uxBlockingProgressExecutor) Run(_ context.Context, _ []string, _ string, _ []byte, _ map[string]string, stderr func([]byte)) ([]byte, error) {
	if stderr != nil {
		stderr([]byte("auth-"))
		stderr([]byte("secret is being applied"))
	}
	close(e.started)
	<-e.release
	return nil, errors.New("child failed after progress: distinctive daemon rejection")
}

type uxFailExecutor struct {
	message string
}

func (e uxFailExecutor) Run(context.Context, []string, string, []byte, map[string]string, func([]byte)) ([]byte, error) {
	return nil, errors.New(e.message)
}

type uxCancelExecutor struct{ cancel context.CancelFunc }

func (e uxCancelExecutor) Run(_ context.Context, _ []string, _ string, _ []byte, _ map[string]string, _ func([]byte)) ([]byte, error) {
	e.cancel()
	return nil, context.Canceled
}

type uxCancelWithErrorExecutor struct {
	cancel context.CancelFunc
	err    error
}

func (e uxCancelWithErrorExecutor) Run(context.Context, []string, string, []byte, map[string]string, func([]byte)) ([]byte, error) {
	e.cancel()
	return nil, e.err
}

func (e *uxActionExecutor) Run(_ context.Context, argv []string, _ string, input []byte, _ map[string]string, stderr func([]byte)) ([]byte, error) {
	var call uxActionCall
	if err := json.Unmarshal(input, &call); err != nil {
		return nil, err
	}
	e.calls = append(e.calls, call)
	if call.Action == "authenticate" {
		e.authCalls++
		if e.failNthAuth > 0 && e.authCalls == e.failNthAuth {
			return nil, errors.New("selected child auth failed")
		}
	}
	if call.Action == e.failOn && e.cancel != nil {
		e.cancel()
		return nil, context.Canceled
	}
	if call.Action == e.failOn {
		if stderr != nil {
			stderr([]byte(e.stderr))
		}
		return nil, errors.New("child exited 9")
	}
	if call.Action == "prepare" {
		return []byte(`{"runtime":{"image":"fixture/image","host":"127.0.0.1","container_port":9000,"transport":"streamable-http","endpoint_path":"/mcp"}}`), nil
	}
	if call.Action == "authenticate" && e.authRequired {
		return []byte(`{"auth_required":true}`), nil
	}
	_ = argv
	return []byte(`{}`), nil
}

type uxOrderedRuntime struct {
	order *[]string
}

type uxForeignRuntime struct{ starts, stops int }

func (r *uxForeignRuntime) Start(context.Context, state.Key, mcp.RunSpec) (mcp.Instance, error) {
	r.starts++
	return mcp.Instance{}, errors.New("container name belongs to another installation or an unknown owner")
}
func (r *uxForeignRuntime) Stop(context.Context, state.Key) error {
	r.stops++
	return errors.New("foreign runtime cannot be stopped")
}
func (*uxForeignRuntime) List(context.Context) ([]mcp.Instance, error) { return nil, nil }
func (*uxForeignRuntime) Logs(context.Context, state.Key) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}

type uxCancelRuntime struct {
	starts int
	cancel context.CancelFunc
}

func (r *uxCancelRuntime) Start(context.Context, state.Key, mcp.RunSpec) (mcp.Instance, error) {
	r.starts++
	r.cancel()
	return mcp.Instance{}, context.Canceled
}
func (*uxCancelRuntime) Stop(context.Context, state.Key) error        { return nil }
func (*uxCancelRuntime) List(context.Context) ([]mcp.Instance, error) { return nil, nil }
func (*uxCancelRuntime) Logs(context.Context, state.Key) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}

type uxCancelAtErrContext struct {
	context.Context
	errCalls  int
	cancelAt  int
	done      chan struct{}
	cancelled bool
}

func (c *uxCancelAtErrContext) Err() error {
	c.errCalls++
	if c.errCalls >= c.cancelAt {
		if !c.cancelled {
			close(c.done)
			c.cancelled = true
		}
		return context.Canceled
	}
	return nil
}
func (c *uxCancelAtErrContext) Done() <-chan struct{} { return c.done }

func (r uxOrderedRuntime) Start(context.Context, state.Key, mcp.RunSpec) (mcp.Instance, error) {
	*r.order = append(*r.order, "start")
	return mcp.Instance{URL: "http://127.0.0.1:9000/mcp"}, nil
}
func (uxOrderedRuntime) Stop(context.Context, state.Key) error        { return nil }
func (uxOrderedRuntime) List(context.Context) ([]mcp.Instance, error) { return nil, nil }
func (uxOrderedRuntime) Logs(context.Context, state.Key) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}

func TestUXSaveAuthenticatesWithSubmittedValuesBeforePrepareAndRegistersActualEffects(t *testing.T) {
	svc, _, store := fixture(t)
	ref, agentID := profileTestAgent(t, svc, "opencode")
	order := []string{}
	exec := &uxActionExecutor{}
	svc.Options.Runner = exec
	svc.Options.Runtime = uxOrderedRuntime{order: &order}
	pkg := &svc.Source.Catalog[0]
	pkg.Skill = nil
	pkg.Inputs = []catalog.Input{{Name: "token", Type: "secret", Required: true}}
	pkg.MCP = &catalog.MCP{Image: "fixture/image", Transport: "streamable-http", ContainerPort: 9000, EndpointPath: "/mcp", Actions: map[string]catalog.Command{
		"authenticate": {Argv: []string{"fixture-auth"}},
		"prepare":      {Argv: []string{"fixture-prepare"}},
	}}
	result, err := svc.ApplyProfile(context.Background(), ProfileRequest{Ref: ref, DestinationIDs: []string{agentID}, Inputs: map[string]any{"token": "submitted"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(exec.calls) != 2 || exec.calls[0].Action != "authenticate" || exec.calls[1].Action != "prepare" {
		t.Fatalf("action order = %#v; want authenticate then prepare", exec.calls)
	}
	if exec.calls[0].Inputs["token"] != "submitted" || exec.calls[0].Interactive || exec.calls[1].Interactive {
		t.Fatalf("submitted auth values or noninteractive boundary lost: %#v", exec.calls)
	}
	if len(order) != 1 || order[0] != "start" {
		t.Fatalf("runtime effects = %#v", order)
	}
	if !result.Saved || len(result.Changes) != 1 || result.Changes[0].Component != "mcp" {
		t.Fatalf("actual installation effects not recorded: %#v", result)
	}
	if _, err := store.Answers(state.Key{Source: "fixture", Package: "demo", Target: "default"}); err != nil {
		t.Fatal(err)
	}
}

func TestUXUIInstallProgressStreamsRedactedChildOutputBeforeFailure(t *testing.T) {
	svc, _, _ := fixture(t)
	ref, agentID := profileTestAgent(t, svc, "opencode")
	isolateUXUserHome(t, t.TempDir())
	pkg := &svc.Source.Catalog[0]
	pkg.Skill = nil
	pkg.Inputs = []catalog.Input{{Name: "token", Type: "secret", Required: true}}
	pkg.MCP = &catalog.MCP{Transport: "streamable-http", Actions: map[string]catalog.Command{"authenticate": {Argv: []string{"fixture-auth"}}}}
	executor := &uxBlockingProgressExecutor{started: make(chan struct{}), release: make(chan struct{})}
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(executor.release) })
	svc.Options.Runner = executor
	progress := make(chan viewmodel.OperationProgress, 8)
	ctx := viewmodel.WithOperationProgress(context.Background(), func(event viewmodel.OperationProgress) { progress <- event })
	request := viewmodel.SetupInstallRequest{Ref: ref, Inputs: map[string]any{"token": "auth-secret"}, DestinationIDs: []string{agentID}}
	resultDone := make(chan struct {
		result viewmodel.OperationResult
		err    error
	}, 1)
	go func() {
		result, err := svc.UIInstall(ctx, request)
		resultDone <- struct {
			result viewmodel.OperationResult
			err    error
		}{result, err}
	}()
	select {
	case <-executor.started:
	case completed := <-resultDone:
		t.Fatalf("install completed before child progress started: result=%+v err=%v", completed.result, completed.err)
	case <-time.After(3 * time.Second):
		t.Fatal("install did not start the blocking child")
	}
	var streamed strings.Builder
	stepSeen := false
	deadline := time.After(3 * time.Second)
	for !strings.Contains(streamed.String(), "is bei") {
		var event viewmodel.OperationProgress
		select {
		case event = <-progress:
		case <-deadline:
			t.Fatalf("timed out waiting for streamed child output: %q", streamed.String())
		}
		if event.Step == "authenticate" {
			stepSeen = true
		}
		streamed.WriteString(event.Output)
	}
	select {
	case <-resultDone:
		t.Fatal("service returned before the blocking child was released")
	default:
	}
	if !stepSeen || !strings.Contains(streamed.String(), "is bei") || strings.Contains(streamed.String(), "auth-secret") {
		t.Fatalf("in-flight progress was missing phase, child text, or redaction: step=%t output=%q", stepSeen, streamed.String())
	}
	releaseOnce.Do(func() { close(executor.release) })
	var completed struct {
		result viewmodel.OperationResult
		err    error
	}
	select {
	case completed = <-resultDone:
	case <-time.After(3 * time.Second):
		t.Fatal("install did not return after child release")
	}
	if completed.err == nil || !strings.Contains(completed.err.Error(), "distinctive daemon rejection") || strings.Contains(completed.err.Error(), "auth-secret") {
		t.Fatalf("completion lost real child failure or leaked secret: result=%+v err=%v", completed.result, completed.err)
	}
}

func TestUXSaveAuthFailureRetainsAnswersAndExactStderr(t *testing.T) {
	svc, _, store := fixture(t)
	ref, agentID := profileTestAgent(t, svc, "opencode")
	kubeconfig := filepath.Join(t.TempDir(), "source.kubeconfig")
	if err := os.WriteFile(kubeconfig, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	exec := &uxActionExecutor{failOn: "authenticate", stderr: "login refused: fixture account"}
	svc.Options.Runner = exec
	runtime := &fakeRuntime{}
	svc.Options.Runtime = runtime
	pkg := &svc.Source.Catalog[0]
	pkg.Skill = nil
	pkg.Inputs = []catalog.Input{{Name: "kubeconfig", Type: "file", Required: true}}
	pkg.MCP = &catalog.MCP{Transport: "streamable-http", Actions: map[string]catalog.Command{"authenticate": {Argv: []string{"fixture-auth"}}, "prepare": {Argv: []string{"fixture-prepare"}}}}
	result, err := svc.ApplyProfile(context.Background(), ProfileRequest{Ref: ref, DestinationIDs: []string{agentID}, Inputs: map[string]any{"kubeconfig": kubeconfig}})
	if err == nil || !strings.Contains(err.Error(), "login refused: fixture account") {
		t.Fatalf("exact auth error missing: %v", err)
	}
	answers, readErr := store.Answers(state.Key{Source: "fixture", Package: "demo", Target: "default"})
	if readErr != nil || answers["kubeconfig"] != kubeconfig {
		t.Fatalf("submitted answer was not retained: %#v, %v", answers, readErr)
	}
	if !result.Saved || len(result.Changes) != 0 || runtime.starts != 0 {
		t.Fatalf("failed authentication claimed apply: result=%#v starts=%d", result, runtime.starts)
	}
	if result.Step != "authenticate" || !strings.Contains(result.Target, "demo") || !strings.Contains(result.Target, "default") {
		t.Fatalf("authentication failure omitted operation step/target: %#v", result)
	}
}

func TestActiveCredentialMethodSurvivesFailedApplyAndProfileReopen(t *testing.T) {
	svc, _, store := fixture(t)
	ref, agentID := profileTestAgent(t, svc, "opencode")
	secret := "opaque-credential-that-must-not-persist"
	exec := &uxActionExecutor{failOn: "authenticate", stderr: "login refused"}
	svc.Options.Runner = exec
	svc.Options.Runtime = &fakeRuntime{}
	pkg := &svc.Source.Catalog[0]
	pkg.Skill = nil
	pkg.Inputs = []catalog.Input{
		{Name: "access_key", Type: "secret", ExclusiveGroup: "access_method", Required: true},
		{Name: "source_document", Type: "file", ExclusiveGroup: "access_method"},
	}
	pkg.MCP = &catalog.MCP{Transport: "streamable-http", Actions: map[string]catalog.Command{"authenticate": {Argv: []string{"fixture-auth"}}}}
	key := state.Key{Source: "fixture", Package: "demo", Target: "default"}
	result, err := svc.ApplyProfile(context.Background(), ProfileRequest{
		Ref: ref, DestinationIDs: []string{agentID},
		Inputs:            map[string]any{"access_key": secret, "source_document": ""},
		ActiveInputGroups: map[string]string{"access_method": "access_key"},
	})
	if err == nil || result.Step != "authenticate" || !result.Saved {
		t.Fatalf("failed auth result = %#v, %v", result, err)
	}
	answers, err := store.Answers(key)
	if err != nil || answers["access_key"] != nil {
		t.Fatalf("secret persisted in answer values: %#v, %v", answers, err)
	}
	if value, ok := answers["source_document"]; !ok || value != "" {
		t.Fatalf("empty alternate value was not preserved: %#v", answers)
	}
	groups, err := store.ActiveInputGroups(key)
	if err != nil || groups["access_method"] != "access_key" {
		t.Fatalf("active method = %#v, %v", groups, err)
	}
	raw, err := os.ReadFile(filepath.Join(store.Root(), "answers", key.ID()+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), secret) {
		t.Fatalf("plain answer record leaked secret: %s", raw)
	}
	preview, err := svc.PreviewProfile(context.Background(), ProfileRequest{Ref: ref})
	if err != nil || preview.ActiveInputGroups["access_method"] != "access_key" {
		t.Fatalf("reopened preview lost selected method: %#v, %v", preview.ActiveInputGroups, err)
	}
	for _, input := range preview.Inputs {
		if input.Definition.Name == "access_key" && input.HasValue {
			t.Fatalf("reopened preview synthesized a secret value: %#v", input)
		}
	}
	exec.failOn = ""
	result, err = svc.ApplyProfile(context.Background(), ProfileRequest{
		Ref: ref, DestinationIDs: []string{agentID},
		Inputs:            map[string]any{"access_key": secret, "source_document": ""},
		ActiveInputGroups: preview.ActiveInputGroups,
	})
	if err != nil || !result.Saved {
		t.Fatalf("retry with re-entered selected credential = %#v, %v", result, err)
	}
	if len(exec.calls) == 0 || exec.calls[len(exec.calls)-1].Action != "authenticate" || exec.calls[len(exec.calls)-1].Inputs["access_key"] != secret {
		t.Fatalf("retry did not authenticate using the selected credential: %#v", exec.calls)
	}
}

func TestActiveInputGroupResolutionUsesDeclaredMembersAndFixedProfileValues(t *testing.T) {
	defs := []catalog.Input{
		{Name: "key_material", Type: "secret", ExclusiveGroup: "credential-choice"},
		{Name: "source_bundle", Type: "file", ExclusiveGroup: "credential-choice"},
		{Name: "unrelated", Type: "string"},
	}
	values := map[string]any{"source_bundle": "/tmp/source"}
	saved := map[string]string{"credential-choice": "key_material"}
	if got := effectiveActiveInputGroups(defs, values, nil, nil, saved)["credential-choice"]; got != "source_bundle" {
		t.Fatalf("legacy value inference = %q; want declared populated member", got)
	}
	if got := effectiveActiveInputGroups(defs, values, nil, map[string]string{"credential-choice": "key_material"}, saved)["credential-choice"]; got != "key_material" {
		t.Fatalf("explicit group selection = %q; want key_material", got)
	}
	if got := effectiveActiveInputGroups(defs, values, map[string]any{"source_bundle": "/fixed"}, map[string]string{"credential-choice": "key_material"}, saved)["credential-choice"]; got != "source_bundle" {
		t.Fatalf("fixed profile member lost precedence: %q", got)
	}
	if got := effectiveActiveInputGroups(defs, values, map[string]any{"source_bundle": "/fixed"}, map[string]string{"credential-choice": "key_material"}, map[string]string{"credential-choice": "key_material"})["credential-choice"]; got != "source_bundle" {
		t.Fatalf("fixed profile member lost precedence over both form and saved selection: %q", got)
	}
	if err := validateActiveInputGroups(defs, map[string]string{"credential-choice": "unrelated"}); err == nil {
		t.Fatal("input outside declared group was accepted")
	}
}

func TestActiveInputGroupResolutionIgnoresFixedFalseMemberSelection(t *testing.T) {
	defs := []catalog.Input{
		{Name: "use_source", Type: "boolean", ExclusiveGroup: "credential-choice"},
		{Name: "access_token", Type: "secret", ExclusiveGroup: "credential-choice"},
	}
	fixed := map[string]any{"use_source": false}
	values := map[string]any{"use_source": false, "access_token": "secret"}

	for _, tc := range []struct {
		name     string
		explicit map[string]string
		saved    map[string]string
	}{
		{name: "explicit", explicit: map[string]string{"credential-choice": "use_source"}},
		{name: "saved", saved: map[string]string{"credential-choice": "use_source"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := effectiveActiveInputGroups(defs, values, fixed, tc.explicit, tc.saved)
			if got["credential-choice"] != "access_token" {
				t.Fatalf("fixed false member displaced populated editable member: %#v", got)
			}
		})
	}
	if got := effectiveActiveInputGroups(defs, map[string]any{"use_source": false}, fixed, map[string]string{"credential-choice": "use_source"}, nil)["credential-choice"]; got != "" {
		t.Fatalf("fixed false member was treated as an active auth method without an alternate: %q", got)
	}
	if got := effectiveActiveInputGroups(defs, map[string]any{"use_source": false}, fixed, nil, map[string]string{"credential-choice": "use_source"})["credential-choice"]; got != "" {
		t.Fatalf("stale fixed false saved method was treated as active without an alternate: %q", got)
	}
	if got := effectiveActiveInputGroups(defs, map[string]any{}, fixed, map[string]string{"credential-choice": "access_token"}, nil)["credential-choice"]; got != "access_token" {
		t.Fatalf("editable omitted secret could not remain selected for re-entry: %q", got)
	}
}

func TestFixedEmptyStringInputPolicyIsRejected(t *testing.T) {
	defs := []catalog.Input{{Name: "source_bundle", Type: "file", ExclusiveGroup: "credential-choice"}}
	_, err := fixedTargetInputs(defs, config.Target{Path: "profile.toml", InputPolicy: map[string]string{"source_bundle": "fixed"}}, map[string]any{"source_bundle": ""})
	if err == nil {
		t.Fatal("fixed empty string should be rejected before active-method resolution")
	}
}

func TestSwitchingCredentialMethodDoesNotReuseAnotherMethodsManagedCredential(t *testing.T) {
	svc, _, store := fixture(t)
	ref, agentID := profileTestAgent(t, svc, "opencode")
	exec := &uxActionExecutor{failOn: "authenticate", stderr: "new method rejected"}
	svc.Options.Runner = exec
	svc.Options.Runtime = &fakeRuntime{}
	pkg := &svc.Source.Catalog[0]
	pkg.Skill = nil
	pkg.Inputs = []catalog.Input{
		{Name: "credential_a", Type: "secret", ExclusiveGroup: "credential-choice"},
		{Name: "credential_b_file", Type: "file", ExclusiveGroup: "credential-choice"},
	}
	pkg.MCP = &catalog.MCP{Transport: "streamable-http", CredentialFiles: []string{"managed.bin"}, Actions: map[string]catalog.Command{"authenticate": {Argv: []string{"fixture-auth"}}}}
	key := state.Key{Source: "fixture", Package: "demo", Target: "default"}
	if err := store.SaveAnswersWithActiveInputGroups(key, map[string]any{}, map[string]string{"credential-choice": "credential_a"}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(store.AuthDir(key), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.AuthDir(key), "managed.bin"), []byte("old method material"), 0600); err != nil {
		t.Fatal(err)
	}
	request := ProfileRequest{Ref: ref, DestinationIDs: []string{agentID}, ActiveInputGroups: map[string]string{"credential-choice": "credential_b_file"}}
	if _, err := svc.ApplyProfile(context.Background(), request); err == nil || !strings.Contains(err.Error(), "new method rejected") {
		t.Fatalf("switch did not attempt authentication: %v", err)
	}
	exec.failOn = ""
	if _, err := svc.ApplyProfile(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if len(exec.calls) != 2 || exec.calls[0].Action != "authenticate" || exec.calls[1].Action != "authenticate" {
		t.Fatalf("retry reused old method's managed material: %#v", exec.calls)
	}
}

func TestPendingCredentialTransitionSurvivesAuthRequiredResult(t *testing.T) {
	svc, _, store := fixture(t)
	ref, agentID := profileTestAgent(t, svc, "opencode")
	exec := &uxActionExecutor{authRequired: true}
	svc.Options.Runner = exec
	svc.Options.Runtime = &fakeRuntime{}
	pkg := &svc.Source.Catalog[0]
	pkg.Skill = nil
	pkg.Inputs = []catalog.Input{
		{Name: "auth_secret", Type: "secret", ExclusiveGroup: "auth_mode"},
		{Name: "auth_source", Type: "file", ExclusiveGroup: "auth_mode"},
	}
	pkg.MCP = &catalog.MCP{Name: "demo", Transport: "streamable-http", CredentialFiles: []string{"managed.bin"}, Actions: map[string]catalog.Command{"authenticate": {Argv: []string{"fixture-auth"}}}}
	key := state.Key{Source: "fixture", Package: "demo", Target: "default"}
	if err := store.SaveAnswersWithActiveInputGroups(key, map[string]any{}, map[string]string{"auth_mode": "auth_secret"}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(store.AuthDir(key), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.AuthDir(key), "managed.bin"), []byte("old method material"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := svc.ApplyProfile(context.Background(), ProfileRequest{Ref: ref, DestinationIDs: []string{agentID}, ActiveInputGroups: map[string]string{"auth_mode": "auth_source"}})
	if err != nil {
		t.Fatal(err)
	}
	child := mcpProfileKey(key, *pkg, *pkg.MCP)
	pending, err := store.PendingAuthInputGroups(key, child.ID())
	if err != nil || pending["auth_mode"] != "auth_source" {
		t.Fatalf("unresolved auth transition was cleared: %#v, %v", pending, err)
	}
}

func TestPendingCredentialTransitionsAreIsolatedAcrossMCPChildren(t *testing.T) {
	svc, _, store := fixture(t)
	ref, agentID := profileTestAgent(t, svc, "opencode")
	exec := &uxActionExecutor{failNthAuth: 2}
	svc.Options.Runner = exec
	svc.Options.Runtime = &fakeRuntime{}
	pkg := &svc.Source.Catalog[0]
	pkg.Skill = nil
	pkg.MCP = nil
	pkg.Inputs = []catalog.Input{
		{Name: "first_method", Type: "secret", ExclusiveGroup: "credential_mode"},
		{Name: "second_method", Type: "file", ExclusiveGroup: "credential_mode"},
	}
	definitions := []catalog.MCP{
		{Name: "alpha", Transport: "streamable-http", CredentialFiles: []string{"alpha.bin"}, Actions: map[string]catalog.Command{"authenticate": {Argv: []string{"auth-alpha"}}}},
		{Name: "beta", Transport: "streamable-http", CredentialFiles: []string{"beta.bin"}, Actions: map[string]catalog.Command{"authenticate": {Argv: []string{"auth-beta"}}}},
	}
	pkg.MCPs = definitions
	key := state.Key{Source: "fixture", Package: "demo", Target: "default"}
	if err := store.SaveAnswersWithActiveInputGroups(key, map[string]any{}, map[string]string{"credential_mode": "first_method"}); err != nil {
		t.Fatal(err)
	}
	for _, definition := range definitions {
		child := mcpProfileKey(key, *pkg, definition)
		if err := os.MkdirAll(store.AuthDir(child), 0700); err != nil {
			t.Fatal(err)
		}
		filename := definition.CredentialFiles[0]
		if err := os.WriteFile(filepath.Join(store.AuthDir(child), filename), []byte("old method material"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	_, err := svc.ApplyProfile(context.Background(), ProfileRequest{Ref: ref, DestinationIDs: []string{agentID}, ActiveInputGroups: map[string]string{"credential_mode": "second_method"}})
	if err == nil || !strings.Contains(err.Error(), "selected child auth failed") {
		t.Fatalf("second child failure not returned: %v", err)
	}
	alpha := mcpProfileKey(key, *pkg, definitions[0])
	beta := mcpProfileKey(key, *pkg, definitions[1])
	alphaPending, err := store.PendingAuthInputGroups(key, alpha.ID())
	if err != nil {
		t.Fatal(err)
	}
	betaPending, err := store.PendingAuthInputGroups(key, beta.ID())
	if err != nil {
		t.Fatal(err)
	}
	if len(alphaPending) != 0 || betaPending["credential_mode"] != "second_method" {
		t.Fatalf("child transition markers crossed: alpha=%#v beta=%#v", alphaPending, betaPending)
	}
}

func TestUXCancelledAuthIsNotFailed(t *testing.T) {
	svc, _, store := fixture(t)
	ref, agentID := profileTestAgent(t, svc, "opencode")
	kubeconfig := filepath.Join(t.TempDir(), "source.kubeconfig")
	if err := os.WriteFile(kubeconfig, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	exec := &uxActionExecutor{failOn: "authenticate", cancel: cancel}
	svc.Options.Runner = exec
	runtime := &fakeRuntime{}
	svc.Options.Runtime = runtime
	pkg := &svc.Source.Catalog[0]
	pkg.Skill = nil
	pkg.Inputs = []catalog.Input{{Name: "kubeconfig", Type: "file", Required: true}}
	pkg.MCP = &catalog.MCP{Transport: "streamable-http", Actions: map[string]catalog.Command{"authenticate": {Argv: []string{"fixture-auth"}}, "prepare": {Argv: []string{"fixture-prepare"}}}}
	result, err := svc.ApplyProfile(ctx, ProfileRequest{Ref: ref, DestinationIDs: []string{agentID}, Inputs: map[string]any{"kubeconfig": kubeconfig}})
	if !errors.Is(err, picker.ErrCancelled) {
		t.Fatalf("auth cancellation became an operation failure: %v", err)
	}
	if !result.Saved || len(result.Errors) != 0 || len(result.Changes) != 0 || runtime.starts != 0 {
		t.Fatalf("cancelled auth reported failed or applied effects: result=%#v starts=%d", result, runtime.starts)
	}
	answers, readErr := store.Answers(state.Key{Source: "fixture", Package: "demo", Target: "default"})
	if readErr != nil || answers["kubeconfig"] != kubeconfig {
		t.Fatalf("cancelled auth discarded submitted path: %#v %v", answers, readErr)
	}
}

func TestUXCancellationPhraseInUnrelatedAuthErrorRemainsVisible(t *testing.T) {
	svc, _, store := fixture(t)
	ref, agentID := profileTestAgent(t, svc, "opencode")
	kubeconfig := filepath.Join(t.TempDir(), "source.kubeconfig")
	if err := os.WriteFile(kubeconfig, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc.Options.Runner = uxCancelWithErrorExecutor{cancel: cancel, err: errors.New("child failed: context canceled: remote endpoint rejected request")}
	pkg := &svc.Source.Catalog[0]
	pkg.Skill = nil
	pkg.Inputs = []catalog.Input{{Name: "kubeconfig", Type: "file", Required: true}}
	pkg.MCP = &catalog.MCP{Actions: map[string]catalog.Command{"authenticate": {Argv: []string{"fixture-auth"}}}}
	result, err := svc.ApplyProfile(ctx, ProfileRequest{Ref: ref, DestinationIDs: []string{agentID}, Inputs: map[string]any{"kubeconfig": kubeconfig}})
	if err == nil || errors.Is(err, picker.ErrCancelled) || !strings.Contains(err.Error(), "remote endpoint rejected request") {
		t.Fatalf("unrelated error was hidden by cancellation: %v", err)
	}
	answers, readErr := store.Answers(state.Key{Source: "fixture", Package: "demo", Target: "default"})
	if readErr != nil || answers["kubeconfig"] != kubeconfig || !result.Saved || len(result.Errors) != 1 || !strings.Contains(result.Errors[0], "remote endpoint rejected request") || len(result.Changes) != 0 {
		t.Fatalf("auth error lost saved state or claimed effects: result=%#v answers=%#v err=%v", result, answers, readErr)
	}
}

func TestUXCancelledGenerationIsNotFailed(t *testing.T) {
	svc, _, store := fixture(t)
	ref, agentID := profileTestAgent(t, svc, "codex")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc.Options.Runner = uxCancelExecutor{cancel: cancel}
	svc.Source.Catalog[0].Generator = &catalog.Command{Argv: []string{"fixture-generator"}}
	result, err := svc.ApplyProfile(ctx, ProfileRequest{Ref: ref, DestinationIDs: []string{agentID}})
	if !errors.Is(err, picker.ErrCancelled) {
		t.Fatalf("generation cancellation became an operation failure: %v", err)
	}
	if !result.Saved || len(result.Errors) != 0 || len(result.Changes) != 0 {
		t.Fatalf("cancelled generation reported failed or applied effects: %#v", result)
	}
	answers, readErr := store.Answers(state.Key{Source: "fixture", Package: "demo", Target: "default"})
	if readErr != nil || len(answers) != 0 {
		t.Fatalf("cancelled generation lost saved configuration: %#v, %v", answers, readErr)
	}
}

func TestUXCancelledPrepareIsNotFailed(t *testing.T) {
	svc, _, store := fixture(t)
	ref, agentID := profileTestAgent(t, svc, "opencode")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	exec := &uxActionExecutor{failOn: "prepare", cancel: cancel}
	svc.Options.Runner = exec
	svc.Options.Runtime = &fakeRuntime{}
	svc.Source.Catalog[0].Skill = nil
	svc.Source.Catalog[0].Inputs = []catalog.Input{{Name: "port", Type: "integer", Required: true}}
	svc.Source.Catalog[0].MCP = &catalog.MCP{Image: "fixture", HostPortInput: "port", ContainerPort: 8765, Transport: "streamable-http", Actions: map[string]catalog.Command{"prepare": {Argv: []string{"fixture-prepare"}}}}
	result, err := svc.ApplyProfile(ctx, ProfileRequest{Ref: ref, DestinationIDs: []string{agentID}, Inputs: map[string]any{"port": int64(9000)}})
	if !errors.Is(err, picker.ErrCancelled) {
		t.Fatalf("prepare cancellation became an operation failure: %v", err)
	}
	answers, readErr := store.Answers(state.Key{Source: "fixture", Package: "demo", Target: "default"})
	if readErr != nil || answers["port"] != json.Number("9000") || !result.Saved || len(result.Errors) != 0 || len(result.Changes) != 0 {
		t.Fatalf("cancelled prepare lost saved state or reported failed effects: result=%#v answers=%#v err=%v", result, answers, readErr)
	}
}

func TestUXCancelledStartIsNotFailed(t *testing.T) {
	svc, _, store := fixture(t)
	ref, agentID := profileTestAgent(t, svc, "opencode")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runtime := &uxCancelRuntime{cancel: cancel}
	svc.Options.Runtime = runtime
	svc.Source.Catalog[0].Skill = nil
	svc.Source.Catalog[0].Inputs = []catalog.Input{{Name: "port", Type: "integer", Required: true}}
	svc.Source.Catalog[0].MCP = &catalog.MCP{Image: "fixture", HostPortInput: "port", ContainerPort: 8765, Transport: "streamable-http"}
	result, err := svc.ApplyProfile(ctx, ProfileRequest{Ref: ref, DestinationIDs: []string{agentID}, Inputs: map[string]any{"port": int64(9000)}})
	if !errors.Is(err, picker.ErrCancelled) {
		t.Fatalf("start cancellation became an operation failure: %v", err)
	}
	answers, readErr := store.Answers(state.Key{Source: "fixture", Package: "demo", Target: "default"})
	if readErr != nil || answers["port"] != json.Number("9000") || !result.Saved || len(result.Errors) != 0 || len(result.Changes) != 0 || runtime.starts != 1 {
		t.Fatalf("cancelled start lost saved state or claimed effects: result=%#v answers=%#v starts=%d err=%v", result, answers, runtime.starts, readErr)
	}
}

func TestUXCancelledRegistrationAfterSuccessRetainsChanges(t *testing.T) {
	svc, _, store := fixture(t)
	ref, firstID := profileTestAgent(t, svc, "claude")
	_, secondID := profileTestAgent(t, svc, "opencode")
	svc.Source.Catalog[0].Skill = nil
	svc.Source.Catalog[0].MCP = &catalog.MCP{Name: "demo", Transport: "streamable-http"}
	svc.Options.Runtime = &fakeRuntime{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	registerSteps := 0
	ctx = viewmodel.WithOperationProgress(ctx, func(event viewmodel.OperationProgress) {
		if event.Step == "register" {
			registerSteps++
			if registerSteps == 2 {
				cancel()
			}
		}
	})
	result, err := svc.ApplyProfile(ctx, ProfileRequest{Ref: ref, ItemIDs: []string{"mcp:demo"}, DestinationIDs: []string{firstID, secondID}, ExternalURLs: map[string]string{"demo": "http://fixture.example/mcp"}})
	if !errors.Is(err, picker.ErrCancelled) {
		t.Fatalf("registration cancellation became an operation failure: %v", err)
	}
	if !result.Saved || len(result.Errors) != 0 || len(result.Changes) != 1 || result.Changes[0].AgentID != firstID {
		t.Fatalf("cancelled second registration lost actual first effect: %#v", result)
	}
	rows, readErr := store.Installations()
	if readErr != nil {
		t.Fatal(readErr)
	}
	registered := map[string]bool{}
	for _, row := range rows {
		if row.Component == "mcp" {
			registered[row.AgentID] = true
		}
	}
	if !registered[firstID] || registered[secondID] {
		t.Fatalf("registration ledger disagrees with cancellation effects: %#v", rows)
	}
}

func TestUXCancellationDoesNotHideRealRegistrationFailure(t *testing.T) {
	svc, _, _ := fixture(t)
	ref, agentID := profileTestAgent(t, svc, "opencode")
	svc.Source.Catalog[0].Skill = nil
	svc.Source.Catalog[0].MCP = &catalog.MCP{Name: "demo", Transport: "streamable-http"}
	configPath := svc.Options.AgentScopes[agentID].ConfigPathOverride
	if err := os.WriteFile(configPath, []byte(`{"mcp":{},"mcp":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	fallback := svc.adapterRegistry()
	baseAdapter, err := fallback.Adapter(agentID)
	if err != nil {
		t.Fatal(err)
	}
	manager, ok := baseAdapter.(agents.MCPManager)
	if !ok {
		t.Fatal("OpenCode adapter lacks MCP manager")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wrapped := &uxCancelDuringRegisterAdapter{Adapter: baseAdapter, manager: manager, cancel: cancel}
	svc.Options.Adapters = uxReplacementRegistry{fallback: fallback, replacements: map[string]agents.Adapter{agentID: wrapped}}
	result, err := svc.ApplyProfile(ctx, ProfileRequest{Ref: ref, ItemIDs: []string{"mcp:demo"}, DestinationIDs: []string{agentID}, ExternalURLs: map[string]string{"demo": "http://fixture.example/mcp"}})
	if err == nil || !strings.Contains(err.Error(), "duplicate agent config key") || errors.Is(err, picker.ErrCancelled) {
		t.Fatalf("cancellation hid real registration failure: %v", err)
	}
	if len(result.Errors) != 1 || !strings.Contains(result.Errors[0], "duplicate agent config key") || len(result.Changes) != 0 {
		t.Fatalf("registration failure or actual effects were misreported: %#v", result)
	}
}

func TestUXGenerateFailureReportsChildErrorAndDoesNotMarkInstallation(t *testing.T) {
	svc, env, store := fixture(t)
	svc.Options.Runner = uxFailExecutor{message: "generator: fixture compilation failed"}
	pkg := &svc.Source.Catalog[0]
	pkg.Generator = &catalog.Command{Argv: []string{"fixture-generator"}}
	result, err := svc.applyProfileFixture(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{env}})
	if err == nil || !strings.Contains(err.Error(), "generator: fixture compilation failed") {
		t.Fatalf("generator child error missing: %v", err)
	}
	if !result.Saved || result.Step != "generate" || result.Target == "" || len(result.Changes) != 0 {
		t.Fatalf("generation failure result = %#v", result)
	}
	rows, readErr := store.Installations()
	if readErr != nil || len(rows) != 0 {
		t.Fatalf("failed generation recorded installation: %#v, %v", rows, readErr)
	}
}

func TestUXOneAgentFailureDoesNotMarkThatRegistration(t *testing.T) {
	svc, _, store := fixture(t)
	ref, firstID := profileTestAgent(t, svc, "claude")
	_, secondID := profileTestAgent(t, svc, "opencode")
	svc.Source.Catalog[0].Skill = nil
	svc.Source.Catalog[0].MCP = &catalog.MCP{Name: "demo", Transport: "streamable-http"}
	svc.Options.Runtime = &fakeRuntime{}
	secondPath := svc.Options.AgentScopes[secondID].ConfigPathOverride
	if err := os.WriteFile(secondPath, []byte(`{"mcp":{},"mcp":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := svc.ApplyProfile(context.Background(), ProfileRequest{Ref: ref, ItemIDs: []string{"mcp:demo"}, DestinationIDs: []string{firstID, secondID}, ExternalURLs: map[string]string{"demo": "http://fixture.example/mcp"}})
	if err == nil || !strings.Contains(err.Error(), "duplicate agent config key") {
		t.Fatalf("second agent failure missing: %v", err)
	}
	rows, readErr := store.Installations()
	if readErr != nil {
		t.Fatal(readErr)
	}
	registered := map[string]bool{}
	for _, row := range rows {
		if row.Component == "mcp" {
			registered[row.AgentID] = true
		}
	}
	if !registered[firstID] || registered[secondID] || len(result.Changes) != 1 || result.Changes[0].AgentID != firstID {
		t.Fatalf("registration outcomes misreported: result=%#v rows=%#v", result, rows)
	}
	if result.Step != "register" || result.Target == "" {
		t.Fatalf("registration failure omitted operation step/target: %#v", result)
	}
}

func TestUXCredentialObservationUsesManagedMaterialNotSavedSourcePath(t *testing.T) {
	svc, _, store := fixture(t)
	pkg := svc.Source.Catalog[0]
	pkg.ID = "neutral-capability"
	if pkg.MCP == nil {
		pkg.MCP = &catalog.MCP{}
	}
	pkg.MCP.CredentialFiles = []string{"session.bin"}
	svc.Source.Catalog[0] = pkg
	key := state.Key{Source: "fixture", Package: pkg.ID, Target: "default"}
	sourcePath := filepath.Join(t.TempDir(), "source.kubeconfig")
	if err := store.SaveAnswers(key, map[string]any{"kubeconfig": sourcePath}); err != nil {
		t.Fatal(err)
	}
	state, note := svc.credentialObservation(pkg, key)
	if state != "missing" || note == "" {
		t.Fatalf("missing managed credential observation = %q, %q", state, note)
	}
	managedPath := filepath.Join(svc.Store.AuthDir(key), "session.bin")
	if err := os.MkdirAll(svc.Store.AuthDir(key), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(managedPath, []byte("managed fixture credential"), 0600); err != nil {
		t.Fatal(err)
	}
	state, note = svc.credentialObservation(pkg, key)
	if state != "present" || !strings.Contains(note, managedPath) {
		t.Fatalf("present managed credential observation = %q, %q", state, note)
	}
	if _, err := os.Stat(sourcePath); !os.IsNotExist(err) {
		t.Fatalf("fixture source path should remain absent: %v", err)
	}
	pkg.ID = "unrecognized-auth-package"
	pkg.MCP.CredentialFiles = nil
	if err := os.WriteFile(svc.Store.AuthDir(key)+"/custom-state", []byte("opaque"), 0600); err != nil {
		t.Fatal(err)
	}
	state, note = svc.credentialObservation(pkg, key)
	if state != "unknown" || note == "" {
		t.Fatalf("unrecognized credential material was over-interpreted: %q, %q", state, note)
	}
}

func TestUXSetupPreviewReportsManagedCredentialObservation(t *testing.T) {
	svc, _, store := fixture(t)
	pkg := &svc.Source.Catalog[0]
	pkg.ID = "neutral-capability"
	if pkg.MCP == nil {
		pkg.MCP = &catalog.MCP{}
	}
	pkg.MCP.CredentialFiles = []string{"session.bin"}
	pkg.Inputs = []catalog.Input{{Name: "kubeconfig", Label: "Source kubeconfig", Type: "file"}}
	svc.Source.Catalog[0] = *pkg
	key := state.Key{Source: "fixture", Package: pkg.ID, Target: "default"}
	ref := writeProfileForTest(t, svc, pkg.ID, "default", "")
	sourcePath := t.TempDir() + "/source-kubeconfig"
	if err := store.SaveAnswers(key, map[string]any{"kubeconfig": sourcePath}); err != nil {
		t.Fatal(err)
	}
	preview, err := svc.PreviewProfile(context.Background(), ProfileRequest{Ref: ref})
	if err != nil {
		t.Fatal(err)
	}
	if preview.CredentialState != "missing" || !strings.Contains(strings.ToLower(preview.CredentialNote), "managed") {
		t.Fatalf("saved source path was mistaken for imported credentials: state=%q note=%q", preview.CredentialState, preview.CredentialNote)
	}
	if err := os.MkdirAll(store.AuthDir(key), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.AuthDir(key)+"/session.bin", []byte("managed fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	preview, err = svc.PreviewProfile(context.Background(), ProfileRequest{Ref: ref})
	if err != nil || preview.CredentialState != "present" {
		t.Fatalf("managed imported credentials not shown: state=%q note=%q err=%v", preview.CredentialState, preview.CredentialNote, err)
	}
}

func TestUXUIInstallExplicitEndpointRegistersWithoutStartingOrAuthenticating(t *testing.T) {
	svc, _, store := fixture(t)
	ref, agentID := profileTestAgent(t, svc, "opencode")
	editorCalls := 0
	svc.Options.Editor = func(context.Context, []catalog.Input, map[string]any) (map[string]any, error) {
		editorCalls++
		return nil, errors.New("legacy editor must not open")
	}
	runtime := &fakeRuntime{}
	svc.Options.Runtime = runtime
	svc.Source.Catalog[0].Skill = nil
	svc.Source.Catalog[0].Inputs = []catalog.Input{{Name: "token", Type: "secret"}}
	svc.Source.Catalog[0].MCP = &catalog.MCP{Name: "demo", Transport: "streamable-http", Actions: map[string]catalog.Command{"authenticate": {Argv: []string{"auth"}}, "prepare": {Argv: []string{"prepare"}}}}
	endpoint := "http://foreign.example:8765/mcp"
	result, err := svc.UIInstall(context.Background(), viewmodel.SetupInstallRequest{Ref: ref, Inputs: map[string]any{"token": "submitted"}, DestinationIDs: []string{agentID}, ExternalURLs: map[string]string{"demo": endpoint}})
	if err != nil {
		t.Fatal(err)
	}
	if editorCalls != 0 || runtime.starts != 0 {
		t.Fatalf("external registration invoked local controls: editor=%d starts=%d", editorCalls, runtime.starts)
	}
	rows, err := store.Installations()
	if err != nil {
		t.Fatal(err)
	}
	registered := false
	for _, row := range rows {
		if row.Component == "mcp" && row.URL == endpoint {
			registered = true
		}
	}
	if !registered || !result.Saved || len(result.Changes) != 1 || result.Step != "complete" || result.Target == "" || result.Changes[0].URL != endpoint {
		t.Fatalf("explicit endpoint result is inaccurate: result=%#v rows=%#v", result, rows)
	}
}

func TestUXNoTUIInstallInvokesLegacyInteractiveEditor(t *testing.T) {
	svc, _, _ := fixture(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	editorCalls := 0
	svc.Options.Editor = func(context.Context, []catalog.Input, map[string]any) (map[string]any, error) {
		editorCalls++
		return nil, errors.New("legacy interactive editor was invoked")
	}
	result, err := svc.applyProfileFixtureUI(context.Background(), viewmodel.SetupInstallRequest{
		SetupRequest:   viewmodel.SetupRequest{PackageID: "demo"},
		DestinationIDs: []string{"all"},
	})
	if err != nil || !result.Saved || editorCalls != 0 {
		t.Fatalf("TUI install used legacy editor: result=%#v editor calls=%d err=%v", result, editorCalls, err)
	}
}

func TestUXOwnedRunningParameterChangeAppliesImmediately(t *testing.T) {
	svc, _, _ := fixture(t)
	ref, agentID := profileTestAgent(t, svc, "opencode")
	svc.Source.Catalog[0].Skill = nil
	svc.Source.Catalog[0].MCP = &catalog.MCP{Name: "demo", Image: "fixture", HostPortInput: "port", ContainerPort: 8765, Transport: "streamable-http"}
	svc.Source.Catalog[0].Inputs = []catalog.Input{{Name: "port", Type: "integer", Required: true}}
	runtime := &changedRuntime{}
	svc.Options.Runtime = runtime
	result, err := svc.UIInstall(context.Background(), viewmodel.SetupInstallRequest{Ref: ref, Inputs: map[string]any{"port": int64(9000)}, DestinationIDs: []string{agentID}})
	if err != nil {
		t.Fatal(err)
	}
	if runtime.starts != 2 || runtime.stops != 1 {
		t.Fatalf("changed settings were not applied immediately: starts=%d stops=%d", runtime.starts, runtime.stops)
	}
	if !result.Saved || result.Step != "complete" || result.Target == "" || len(result.Changes) != 1 || result.Changes[0].Component != "mcp" {
		t.Fatalf("save result omitted actual applied effects: %#v", result)
	}
}

func TestUXForeignRuntimeSaveCannotRestartIt(t *testing.T) {
	svc, _, _ := fixture(t)
	home := t.TempDir()
	configHome := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", configHome)
	svc.Source.Catalog[0].Skill = nil
	svc.Source.Catalog[0].MCP = &catalog.MCP{Image: "fixture", HostPortInput: "port", ContainerPort: 8765, Transport: "streamable-http"}
	svc.Source.Catalog[0].Inputs = []catalog.Input{{Name: "port", Type: "integer", Required: true}}
	runtime := &uxForeignRuntime{}
	svc.Options.Runtime = runtime
	result, err := svc.applyProfileFixtureUI(context.Background(), viewmodel.SetupInstallRequest{
		SetupRequest: viewmodel.SetupRequest{PackageID: "demo"},
		Inputs:       map[string]any{"port": int64(9000)}, DestinationIDs: []string{"opencode"},
	})
	if err == nil || !strings.Contains(err.Error(), "another installation") {
		t.Fatalf("foreign runtime control did not fail with ownership: %v", err)
	}
	if runtime.starts != 1 || runtime.stops != 0 {
		t.Fatalf("foreign runtime was restarted or stopped: starts=%d stops=%d", runtime.starts, runtime.stops)
	}
	if !result.Saved || len(result.Changes) != 0 || result.Step != "start" {
		t.Fatalf("foreign runtime apply result is inaccurate: %#v", result)
	}
}

func TestUXChangedParametersDoNotReimportRemovedSourceWhenManagedCredentialsExist(t *testing.T) {
	svc, _, store := fixture(t)
	pkg := &svc.Source.Catalog[0]
	pkg.ID = "neutral-capability"
	if pkg.MCP == nil {
		pkg.MCP = &catalog.MCP{}
	}
	pkg.MCP.CredentialFiles = []string{"session.bin"}
	pkg.Skill = nil
	pkg.Inputs = []catalog.Input{{Name: "kubeconfig", Type: "file", Required: true}, {Name: "port", Type: "integer"}}
	pkg.MCP = &catalog.MCP{Name: "demo", CredentialFiles: []string{"session.bin"}, Image: "fixture/image", HostPortInput: "port", Transport: "streamable-http", ContainerPort: 9000, Actions: map[string]catalog.Command{
		"authenticate": {Argv: []string{"fixture-auth"}},
		"prepare":      {Argv: []string{"fixture-prepare"}},
	}}
	ref := writeProfileForTest(t, svc, pkg.ID, "default", "")
	key, keyErr := store.ResolveProfileKey("fixture", pkg.ID, "default")
	if keyErr != nil {
		t.Fatal(keyErr)
	}
	sourcePath := filepath.Join(t.TempDir(), "removed-source.kubeconfig")
	if err := store.SaveAnswers(key, map[string]any{"kubeconfig": sourcePath, "port": 9000}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(store.AuthDir(key), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.AuthDir(key), "session.bin"), []byte("imported fixture material"), 0600); err != nil {
		t.Fatal(err)
	}
	if status, _ := svc.credentialObservation(*pkg, key); status != "present" {
		t.Fatalf("fixture managed credentials were not observable before apply: %q", status)
	}
	exec := &uxActionExecutor{}
	svc.Options.Runner = exec
	svc.Options.Runtime = &fakeRuntime{}
	profileTestAgent(t, svc, "opencode")
	result, err := svc.ApplyProfile(context.Background(), ProfileRequest{Ref: ref, ItemIDs: []string{"mcp:demo"}, DestinationIDs: []string{"opencode"}, Inputs: map[string]any{"kubeconfig": sourcePath, "port": 8765}})
	if err != nil {
		t.Fatal(err)
	}
	if len(exec.calls) != 1 || exec.calls[0].Action != "prepare" {
		t.Fatalf("existing managed credentials were reimported: %#v", exec.calls)
	}
	if !result.Saved || result.Step != "complete" || len(result.Changes) != 1 {
		t.Fatalf("parameter update result = %#v", result)
	}
	if _, err := os.Stat(sourcePath); !os.IsNotExist(err) {
		t.Fatalf("source fixture unexpectedly exists: %v", err)
	}
}

func TestUXRunStartAndAuthenticateNeverInvokeLegacyEditor(t *testing.T) {
	for _, action := range []string{"start", "authenticate"} {
		t.Run(action, func(t *testing.T) {
			svc, _, store := fixture(t)
			home := t.TempDir()
			configHome := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("XDG_CONFIG_HOME", configHome)
			svc.Source.Catalog[0].Inputs = []catalog.Input{{Name: "token", Type: "secret", Required: true}}
			svc.Source.Catalog[0].Skill = nil
			svc.Source.Catalog[0].MCP = &catalog.MCP{
				Name: "demo", Transport: "streamable-http", ContainerPort: 9000,
				Actions: map[string]catalog.Command{
					"authenticate": {Argv: []string{"auth"}},
					"prepare":      {Argv: []string{"prepare"}},
				},
			}
			ref := writeProfileForTest(t, svc, "demo", "default", "")
			key, keyErr := store.ResolveProfileKey(ref.PackID, ref.CapabilityID, ref.Name)
			if keyErr != nil {
				t.Fatal(keyErr)
			}
			if err := store.SaveAnswers(key, map[string]any{"token": "saved-token"}); err != nil {
				t.Fatal(err)
			}
			editorCalls := 0
			svc.Options.Editor = func(context.Context, []catalog.Input, map[string]any) (map[string]any, error) {
				editorCalls++
				return nil, errors.New("legacy editor must not run from the TUI")
			}
			executor := &uxActionExecutor{}
			svc.Options.Runner = executor
			if action == "start" {
				svc.Options.Runtime = &fakeRuntime{}
			}
			if _, err := svc.UIRun(context.Background(), action, ref.PackID, ref.CapabilityID, ref.Name, "", "", ""); err != nil {
				t.Fatal(err)
			}
			if editorCalls != 0 {
				t.Fatalf("UIRun(%s) invoked legacy editor %d time(s)", action, editorCalls)
			}
			if len(executor.calls) == 0 {
				t.Fatal("expected package action to run")
			}
			for _, call := range executor.calls {
				if call.Interactive {
					t.Fatalf("UIRun(%s) marked %s action interactive", action, call.Action)
				}
			}
		})
	}
}
