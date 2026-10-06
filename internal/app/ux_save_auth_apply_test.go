package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

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

type uxFailExecutor struct {
	message string
}

func (e uxFailExecutor) Run(context.Context, []string, string, []byte, map[string]string, func([]byte)) ([]byte, error) {
	return nil, errors.New(e.message)
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

func TestUXSaveAuthFailureRetainsAnswersAndExactStderr(t *testing.T) {
	svc, env, store := fixture(t)
	kubeconfig := t.TempDir() + "/source.kubeconfig"
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
	kubeconfig := t.TempDir() + "/source.kubeconfig"
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
	sourcePath := t.TempDir() + "/source.kubeconfig"
	if err := store.SaveAnswers(key, map[string]any{"kubeconfig": sourcePath}); err != nil {
		t.Fatal(err)
	}
	state, note := svc.credentialObservation(pkg, key)
	if state != "missing" || note == "" {
		t.Fatalf("missing managed credential observation = %q, %q", state, note)
	}
	managedPath := svc.Store.AuthDir(key) + "/kubeconfig"
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
