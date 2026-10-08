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

type uxActionExecutor struct {
	calls  []uxActionCall
	failOn string
	stderr string
	cancel context.CancelFunc
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
	svc, env, store := fixture(t)
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
	env.Kind = "generic"
	env.ID = "fixture-agent"
	env.ConfigPath = t.TempDir() + "/agent.json"
	result, err := svc.Install(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{env}, Inputs: map[string]any{"token": "submitted"}})
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
	request := viewmodel.SetupInstallRequest{SetupRequest: viewmodel.SetupRequest{PackageID: "demo"}, Inputs: map[string]any{"token": "auth-secret"}, DestinationIDs: []string{"codex"}}
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
	svc, env, store := fixture(t)
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
	env.Kind = "generic"
	env.ConfigPath = t.TempDir() + "/agent.json"
	result, err := svc.Install(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{env}, Inputs: map[string]any{"kubeconfig": kubeconfig}})
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

func TestUXCancelledAuthIsNotFailed(t *testing.T) {
	svc, env, store := fixture(t)
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
	env.Kind = "generic"
	env.ConfigPath = t.TempDir() + "/agent.json"
	result, err := svc.Install(ctx, InstallRequest{Package: "demo", Agents: []agents.Environment{env}, Inputs: map[string]any{"kubeconfig": kubeconfig}})
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
	svc, env, store := fixture(t)
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
	result, err := svc.Install(ctx, InstallRequest{Package: "demo", Agents: []agents.Environment{env}, Inputs: map[string]any{"kubeconfig": kubeconfig}})
	if err == nil || errors.Is(err, picker.ErrCancelled) || !strings.Contains(err.Error(), "remote endpoint rejected request") {
		t.Fatalf("unrelated error was hidden by cancellation: %v", err)
	}
	answers, readErr := store.Answers(state.Key{Source: "fixture", Package: "demo", Target: "default"})
	if readErr != nil || answers["kubeconfig"] != kubeconfig || !result.Saved || len(result.Errors) != 0 || len(result.Changes) != 0 {
		t.Fatalf("auth error lost saved state or claimed effects: result=%#v answers=%#v err=%v", result, answers, readErr)
	}
}

func TestUXCancelledGenerationIsNotFailed(t *testing.T) {
	svc, env, store := fixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc.Options.Runner = uxCancelExecutor{cancel: cancel}
	svc.Source.Catalog[0].Generator = &catalog.Command{Argv: []string{"fixture-generator"}}
	result, err := svc.Install(ctx, InstallRequest{Package: "demo", Agents: []agents.Environment{env}})
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
	svc, env, store := fixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	exec := &uxActionExecutor{failOn: "prepare", cancel: cancel}
	svc.Options.Runner = exec
	svc.Options.Runtime = &fakeRuntime{}
	svc.Source.Catalog[0].Skill = nil
	svc.Source.Catalog[0].Inputs = []catalog.Input{{Name: "port", Type: "integer", Required: true}}
	svc.Source.Catalog[0].MCP = &catalog.MCP{Image: "fixture", HostPortInput: "port", ContainerPort: 8765, Transport: "streamable-http", Actions: map[string]catalog.Command{"prepare": {Argv: []string{"fixture-prepare"}}}}
	result, err := svc.Install(ctx, InstallRequest{Package: "demo", Agents: []agents.Environment{env}, Inputs: map[string]any{"port": int64(9000)}})
	if !errors.Is(err, picker.ErrCancelled) {
		t.Fatalf("prepare cancellation became an operation failure: %v", err)
	}
	answers, readErr := store.Answers(state.Key{Source: "fixture", Package: "demo", Target: "default"})
	if readErr != nil || answers["port"] != json.Number("9000") || !result.Saved || len(result.Errors) != 0 || len(result.Changes) != 0 {
		t.Fatalf("cancelled prepare lost saved state or reported failed effects: result=%#v answers=%#v err=%v", result, answers, readErr)
	}
}

func TestUXCancelledStartIsNotFailed(t *testing.T) {
	svc, env, store := fixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runtime := &uxCancelRuntime{cancel: cancel}
	svc.Options.Runtime = runtime
	svc.Source.Catalog[0].Skill = nil
	svc.Source.Catalog[0].Inputs = []catalog.Input{{Name: "port", Type: "integer", Required: true}}
	svc.Source.Catalog[0].MCP = &catalog.MCP{Image: "fixture", HostPortInput: "port", ContainerPort: 8765, Transport: "streamable-http"}
	result, err := svc.Install(ctx, InstallRequest{Package: "demo", Agents: []agents.Environment{env}, Inputs: map[string]any{"port": int64(9000)}})
	if !errors.Is(err, picker.ErrCancelled) {
		t.Fatalf("start cancellation became an operation failure: %v", err)
	}
	answers, readErr := store.Answers(state.Key{Source: "fixture", Package: "demo", Target: "default"})
	if readErr != nil || answers["port"] != json.Number("9000") || !result.Saved || len(result.Errors) != 0 || len(result.Changes) != 0 || runtime.starts != 1 {
		t.Fatalf("cancelled start lost saved state or claimed effects: result=%#v answers=%#v starts=%d err=%v", result, answers, runtime.starts, readErr)
	}
}

func TestUXCancelledRegistrationAfterSuccessRetainsChanges(t *testing.T) {
	svc, first, store := fixture(t)
	svc.Source.Catalog[0].Skill = nil
	svc.Source.Catalog[0].MCP = &catalog.MCP{Transport: "streamable-http"}
	svc.Options.Runtime = &fakeRuntime{}
	first.Kind, first.ID, first.ConfigPath = "generic", "first-agent", t.TempDir()+"/first.json"
	second := first
	second.ID, second.ConfigPath = "second-agent", t.TempDir()+"/second.json"
	ctx := &uxCancelAtErrContext{Context: context.Background(), cancelAt: 4, done: make(chan struct{})}
	result, err := svc.Install(ctx, InstallRequest{Package: "demo", Agents: []agents.Environment{first, second}, ExternalURL: "http://fixture.example/mcp"})
	if !errors.Is(err, picker.ErrCancelled) {
		t.Fatalf("registration cancellation became an operation failure: %v", err)
	}
	if !result.Saved || len(result.Errors) != 0 || len(result.Changes) != 1 || result.Changes[0].AgentID != first.ID {
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
	if !registered[first.ID] || registered[second.ID] {
		t.Fatalf("registration ledger disagrees with cancellation effects: %#v", rows)
	}
}

func TestUXCancellationDoesNotHideEarlierRegistrationFailure(t *testing.T) {
	svc, first, _ := fixture(t)
	svc.Source.Catalog[0].Skill = nil
	svc.Source.Catalog[0].MCP = &catalog.MCP{Transport: "streamable-http"}
	svc.Options.Runtime = &fakeRuntime{}
	first.Kind, first.ID, first.ConfigPath = "generic", "bad-agent", t.TempDir()+"/bad.json"
	if err := os.WriteFile(first.ConfigPath, []byte(`{"servers":{},"servers":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	second := first
	second.ID, second.ConfigPath = "cancelled-agent", t.TempDir()+"/cancelled.json"
	ctx := &uxCancelAtErrContext{Context: context.Background(), cancelAt: 4, done: make(chan struct{})}
	result, err := svc.Install(ctx, InstallRequest{Package: "demo", Agents: []agents.Environment{first, second}, ExternalURL: "http://fixture.example/mcp"})
	if err == nil || !errors.Is(err, picker.ErrCancelled) || !strings.Contains(err.Error(), "duplicate agent config key") {
		t.Fatalf("cancellation hid prior registration failure: %v", err)
	}
	if len(result.Errors) != 1 || !strings.Contains(result.Errors[0], "duplicate agent config key") || len(result.Changes) != 0 {
		t.Fatalf("prior failure or actual effects were misreported: %#v", result)
	}
}

func TestUXGenerateFailureReportsChildErrorAndDoesNotMarkInstallation(t *testing.T) {
	svc, env, store := fixture(t)
	svc.Options.Runner = uxFailExecutor{message: "generator: fixture compilation failed"}
	pkg := &svc.Source.Catalog[0]
	pkg.Generator = &catalog.Command{Argv: []string{"fixture-generator"}}
	result, err := svc.Install(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{env}})
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
	svc, first, store := fixture(t)
	svc.Source.Catalog[0].Skill = nil
	svc.Source.Catalog[0].MCP = &catalog.MCP{Transport: "streamable-http"}
	svc.Options.Runtime = &fakeRuntime{}
	first.Kind = "generic"
	first.ID = "first-agent"
	first.ConfigPath = t.TempDir() + "/first.json"
	second := first
	second.ID = "second-agent"
	second.ConfigPath = t.TempDir() + "/second.json"
	if err := os.WriteFile(second.ConfigPath, []byte(`{"servers":{},"servers":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := svc.Install(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{first, second}})
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
	if !registered[first.ID] || registered[second.ID] || len(result.Changes) != 1 || result.Changes[0].AgentID != first.ID {
		t.Fatalf("registration outcomes misreported: result=%#v rows=%#v", result, rows)
	}
	if result.Step != "register" || result.Target == "" {
		t.Fatalf("registration failure omitted operation step/target: %#v", result)
	}
}

func TestUXCredentialObservationUsesManagedMaterialNotSavedSourcePath(t *testing.T) {
	svc, _, store := fixture(t)
	pkg := svc.Source.Catalog[0]
	pkg.ID = "cluster-inspector"
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
	managedPath := filepath.Join(svc.Store.AuthDir(key), "kubeconfig")
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
	pkg.ID = "cluster-inspector"
	pkg.Inputs = []catalog.Input{{Name: "kubeconfig", Label: "Source kubeconfig", Type: "file"}}
	svc.Source.Catalog[0] = *pkg
	key := state.Key{Source: "fixture", Package: pkg.ID, Target: "default"}
	sourcePath := t.TempDir() + "/source-kubeconfig"
	if err := store.SaveAnswers(key, map[string]any{"kubeconfig": sourcePath}); err != nil {
		t.Fatal(err)
	}
	preview, err := svc.UISetupPreview(context.Background(), viewmodel.SetupRequest{PackageID: pkg.ID})
	if err != nil {
		t.Fatal(err)
	}
	if preview.CredentialState != "missing" || !strings.Contains(strings.ToLower(preview.CredentialNote), "managed") {
		t.Fatalf("saved source path was mistaken for imported credentials: state=%q note=%q", preview.CredentialState, preview.CredentialNote)
	}
	if err := os.MkdirAll(store.AuthDir(key), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.AuthDir(key)+"/kubeconfig", []byte("managed fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	preview, err = svc.UISetupPreview(context.Background(), viewmodel.SetupRequest{PackageID: pkg.ID})
	if err != nil || preview.CredentialState != "present" {
		t.Fatalf("managed imported credentials not shown: state=%q note=%q err=%v", preview.CredentialState, preview.CredentialNote, err)
	}
}

func TestUXUIInstallExplicitEndpointRegistersWithoutStartingOrAuthenticating(t *testing.T) {
	svc, _, store := fixture(t)
	home := t.TempDir()
	configHome := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", configHome)
	editorCalls := 0
	svc.Options.Editor = func(context.Context, []catalog.Input, map[string]any) (map[string]any, error) {
		editorCalls++
		return nil, errors.New("legacy editor must not open")
	}
	runtime := &fakeRuntime{}
	svc.Options.Runtime = runtime
	svc.Source.Catalog[0].Skill = nil
	svc.Source.Catalog[0].Inputs = []catalog.Input{{Name: "token", Type: "secret"}}
	svc.Source.Catalog[0].MCP = &catalog.MCP{Transport: "streamable-http", Actions: map[string]catalog.Command{"authenticate": {Argv: []string{"auth"}}, "prepare": {Argv: []string{"prepare"}}}}
	endpoint := "http://foreign.example:8765/mcp"
	result, err := svc.UIInstall(context.Background(), viewmodel.SetupInstallRequest{
		SetupRequest: viewmodel.SetupRequest{PackageID: "demo"},
		Inputs:       map[string]any{"token": "submitted"}, DestinationIDs: []string{"opencode"}, ExternalURL: endpoint,
	})
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
	if !registered || !result.Saved || len(result.Changes) != 1 || result.Step != "register" || result.Target == "" {
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
	result, err := svc.UIInstall(context.Background(), viewmodel.SetupInstallRequest{
		SetupRequest:   viewmodel.SetupRequest{PackageID: "demo"},
		DestinationIDs: []string{"all"},
	})
	if err != nil || !result.Saved || editorCalls != 0 {
		t.Fatalf("TUI install used legacy editor: result=%#v editor calls=%d err=%v", result, editorCalls, err)
	}
}

func TestUXOwnedRunningParameterChangeAppliesImmediately(t *testing.T) {
	svc, _, _ := fixture(t)
	home := t.TempDir()
	configHome := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", configHome)
	svc.Source.Catalog[0].Skill = nil
	svc.Source.Catalog[0].MCP = &catalog.MCP{Image: "fixture", HostPortInput: "port", ContainerPort: 8765, Transport: "streamable-http"}
	svc.Source.Catalog[0].Inputs = []catalog.Input{{Name: "port", Type: "integer", Required: true}}
	runtime := &changedRuntime{}
	svc.Options.Runtime = runtime
	result, err := svc.UIInstall(context.Background(), viewmodel.SetupInstallRequest{
		SetupRequest: viewmodel.SetupRequest{PackageID: "demo"},
		Inputs:       map[string]any{"port": int64(9000)}, DestinationIDs: []string{"opencode"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if runtime.starts != 2 || runtime.stops != 1 {
		t.Fatalf("changed settings were not applied immediately: starts=%d stops=%d", runtime.starts, runtime.stops)
	}
	if !result.Saved || result.Step != "register" || result.Target == "" || len(result.Changes) != 1 {
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
	result, err := svc.UIInstall(context.Background(), viewmodel.SetupInstallRequest{
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
	svc, env, store := fixture(t)
	pkg := &svc.Source.Catalog[0]
	pkg.ID = "cluster-inspector"
	pkg.Skill = nil
	pkg.Inputs = []catalog.Input{{Name: "kubeconfig", Type: "file", Required: true}, {Name: "port", Type: "integer"}}
	pkg.MCP = &catalog.MCP{Image: "fixture/image", Transport: "streamable-http", ContainerPort: 9000, Actions: map[string]catalog.Command{
		"authenticate": {Argv: []string{"fixture-auth"}},
		"prepare":      {Argv: []string{"fixture-prepare"}},
	}}
	key := state.Key{Source: "fixture", Package: pkg.ID, Target: "default"}
	sourcePath := t.TempDir() + "/removed-source.kubeconfig"
	if err := store.SaveAnswers(key, map[string]any{"kubeconfig": sourcePath, "port": 9000}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(store.AuthDir(key), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.AuthDir(key)+"/kubeconfig", []byte("imported fixture material"), 0600); err != nil {
		t.Fatal(err)
	}
	exec := &uxActionExecutor{}
	svc.Options.Runner = exec
	svc.Options.Runtime = &fakeRuntime{}
	env.Kind = "generic"
	env.ConfigPath = t.TempDir() + "/agent.json"
	result, err := svc.Install(context.Background(), InstallRequest{Package: pkg.ID, Agents: []agents.Environment{env}, Inputs: map[string]any{"kubeconfig": sourcePath, "port": 8765}})
	if err != nil {
		t.Fatal(err)
	}
	if len(exec.calls) != 1 || exec.calls[0].Action != "prepare" {
		t.Fatalf("existing managed credentials were reimported: %#v", exec.calls)
	}
	if !result.Saved || result.Step != "register" || len(result.Changes) != 1 {
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
			key := svc.key("demo", "", "default")
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
			if _, err := svc.UIRun(context.Background(), action, "fixture", "demo", "", "", "", "default"); err != nil {
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
