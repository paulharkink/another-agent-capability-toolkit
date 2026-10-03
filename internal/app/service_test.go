package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/agents"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/mcp"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func fixture(t *testing.T) (*Service, agents.Environment, *state.Store) {
	t.Helper()
	root := t.TempDir()
	pkg := filepath.Join(root, "pkg")
	os.MkdirAll(pkg, 0755)
	os.WriteFile(filepath.Join(pkg, "SKILL.md"), []byte("---\nname: demo\ndescription: demo\n---\nHello"), 0644)
	s, e := state.Open(filepath.Join(root, "state"))
	if e != nil {
		t.Fatal(e)
	}
	env, e := agents.ResolveEnvironment("codex", "codex", filepath.Join(root, "home"))
	if e != nil {
		t.Fatal(e)
	}
	src := config.Source{ID: "fixture", Root: root, ManifestPath: filepath.Join(root, "aact.toml"), Catalog: []catalog.Package{{ID: "demo", Name: "Demo", Dir: pkg, Skill: &catalog.Skill{Name: "demo"}}}, PackageDefaults: map[string]map[string]any{}}
	return New(src, s, Options{}), env, s
}

type fakeRuntime struct{ starts int }

type failingRuntime struct{ fakeRuntime }

type changedRuntime struct {
	starts, stops int
	failRestart   bool
}

func (r *changedRuntime) Start(context.Context, state.Key, mcp.RunSpec) (mcp.Instance, error) {
	r.starts++
	if r.starts == 1 {
		return mcp.Instance{}, mcp.ErrRunningWithDifferentSettings
	}
	if r.failRestart {
		return mcp.Instance{}, errors.New("Docker: port 9000 is already allocated")
	}
	return mcp.Instance{URL: "http://127.0.0.1:9000/mcp"}, nil
}
func (r *changedRuntime) Stop(context.Context, state.Key) error      { r.stops++; return nil }
func (*changedRuntime) List(context.Context) ([]mcp.Instance, error) { return nil, nil }
func (*changedRuntime) Logs(context.Context, state.Key) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}

func (f *failingRuntime) Start(context.Context, state.Key, mcp.RunSpec) (mcp.Instance, error) {
	f.starts++
	return mcp.Instance{}, errors.New("Docker: port 9000 is already allocated")
}

func (f *fakeRuntime) Start(context.Context, state.Key, mcp.RunSpec) (mcp.Instance, error) {
	f.starts++
	return mcp.Instance{URL: "http://127.0.0.1:8765/mcp"}, nil
}
func (*fakeRuntime) Stop(context.Context, state.Key) error        { return nil }
func (*fakeRuntime) List(context.Context) ([]mcp.Instance, error) { return nil, nil }
func (*fakeRuntime) Logs(context.Context, state.Key) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("fixture")), nil
}
func TestExternalURLDoesNotStartDocker(t *testing.T) {
	svc, env, s := fixture(t)
	env.Kind = "generic"
	env.ConfigPath = filepath.Join(t.TempDir(), "manual.json")
	svc.Source.Catalog[0].MCP = &catalog.MCP{Name: "demo", Transport: "streamable-http", RegistrationTimeoutMS: 60000}
	runtime := &fakeRuntime{}
	svc.Options.Runtime = runtime
	out, e := svc.Install(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{env}, ExternalURL: "https://fixture.invalid/mcp"})
	if e != nil {
		t.Fatal(e)
	}
	if runtime.starts != 0 || len(out.Changes) != 2 {
		t.Fatal(out, runtime.starts)
	}
	rows, _ := s.Installations()
	if len(rows) != 2 {
		t.Fatal(rows)
	}
	if rows[1].Component != "mcp" || rows[1].TimeoutMS != 60000 {
		t.Fatalf("MCP registration timeout: %+v", rows)
	}
	_, e = svc.Uninstall(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{env}})
	if e != nil {
		t.Fatal(e)
	}
}

func TestInstallUpdatesOwnedOpenCodeRegistrationTimeout(t *testing.T) {
	svc, initial, store := fixture(t)
	env, err := agents.ResolveEnvironment("opencode", "opencode", initial.Home)
	if err != nil {
		t.Fatal(err)
	}
	svc.Source.Catalog[0].MCP = &catalog.MCP{Name: "demo", Transport: "streamable-http"}
	request := InstallRequest{Package: "demo", Agents: []agents.Environment{env}, ExternalURL: "http://127.0.0.1:8765/mcp"}
	if _, err := svc.Install(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	svc.Source.Catalog[0].MCP.RegistrationTimeoutMS = 60000
	if _, err := svc.Install(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	rows, err := store.Installations()
	if err != nil {
		t.Fatal(err)
	}
	var registration state.Installation
	for _, row := range rows {
		if row.Component == "mcp" {
			registration = row
		}
	}
	if registration.TimeoutMS != 60000 {
		t.Fatalf("stored timeout = %d", registration.TimeoutMS)
	}
	content, err := os.ReadFile(env.ConfigPath)
	if os.IsNotExist(err) {
		content, err = os.ReadFile(strings.TrimSuffix(env.ConfigPath, ".json") + ".jsonc")
	}
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		MCP map[string]struct {
			Timeout int `json:"timeout"`
		} `json:"mcp"`
	}
	if err := json.Unmarshal(content, &config); err != nil {
		t.Fatal(err)
	}
	if config.MCP[registration.RegistrationName].Timeout != 60000 {
		t.Fatalf("OpenCode timeout = %d", config.MCP[registration.RegistrationName].Timeout)
	}
}

func TestFailedSkillInstallKeepsEditedAnswersWithoutClaimingInstallation(t *testing.T) {
	svc, env, store := fixture(t)
	svc.Source.Catalog[0].Inputs = []catalog.Input{{Name: "label", Type: "string", Required: true}}
	key := state.Key{Source: "fixture", Package: "demo", Target: "default"}
	if err := store.SaveAnswers(key, map[string]any{"label": "old"}); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(env.SkillsDir, "demo")
	if err := os.MkdirAll(destination, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, "SKILL.md"), []byte("user-owned"), 0644); err != nil {
		t.Fatal(err)
	}
	result, err := svc.Install(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{env}, Inputs: map[string]any{"label": "new"}})
	if err == nil || !strings.Contains(err.Error(), "refusing foreign skill") {
		t.Fatalf("concrete install failure missing: %v", err)
	}
	answers, err := store.Answers(key)
	if err != nil || answers["label"] != "new" {
		t.Fatalf("edited answers were reverted after failed apply: %#v, %v", answers, err)
	}
	rows, err := store.Installations()
	if err != nil || len(rows) != 0 || len(result.Changes) != 0 {
		t.Fatalf("failed destination claimed installed: rows=%#v result=%#v err=%v", rows, result, err)
	}
}

func TestFailedSkillGenerationKeepsEditedAnswers(t *testing.T) {
	svc, env, store := fixture(t)
	svc.Source.Catalog[0].Inputs = []catalog.Input{{Name: "label", Type: "string", Required: true}}
	svc.Source.Catalog[0].Templates = []catalog.Template{{Source: "missing.mustache", Destination: "SKILL.md"}}
	_, err := svc.Install(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{env}, Inputs: map[string]any{"label": "retry-me"}})
	if err == nil || !strings.Contains(err.Error(), "missing.mustache") {
		t.Fatalf("generation failure missing: %v", err)
	}
	answers, err := store.Answers(state.Key{Source: "fixture", Package: "demo", Target: "default"})
	if err != nil || answers["label"] != "retry-me" {
		t.Fatalf("generated skill failure discarded inputs: %#v, %v", answers, err)
	}
	rows, err := store.Installations()
	if err != nil || len(rows) != 0 {
		t.Fatalf("failed generation claimed installed: %#v, %v", rows, err)
	}
}

func TestFailedMCPStartKeepsEditedAnswersWithoutClaimingRuntimeOrRegistration(t *testing.T) {
	svc, env, store := fixture(t)
	svc.Source.Catalog[0].Skill = nil
	svc.Source.Catalog[0].MCP = &catalog.MCP{Image: "fixture", HostPortInput: "port", ContainerPort: 8765, Transport: "streamable-http"}
	svc.Source.Catalog[0].Inputs = []catalog.Input{{Name: "port", Type: "integer", Required: true}}
	env.Kind = "generic"
	env.ConfigPath = filepath.Join(t.TempDir(), "manual.json")
	runtime := &failingRuntime{}
	svc.Options.Runtime = runtime
	result, err := svc.Install(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{env}, Inputs: map[string]any{"port": int64(9000)}})
	if err == nil || !strings.Contains(err.Error(), "port 9000 is already allocated") {
		t.Fatalf("MCP startup cause hidden: %v", err)
	}
	answers, err := store.Answers(state.Key{Source: "fixture", Package: "demo", Target: "default"})
	if err != nil || answers["port"] != json.Number("9000") {
		t.Fatalf("failed start discarded edited port: %#v, %v", answers, err)
	}
	rows, err := store.Installations()
	if err != nil || len(rows) != 0 || len(result.Changes) != 0 {
		t.Fatalf("failed MCP claimed running or registered: rows=%#v result=%#v err=%v", rows, result, err)
	}
	profiles, err := store.Profiles()
	if err != nil || len(profiles) != 1 {
		t.Fatalf("desired profile not available for retry: %#v, %v", profiles, err)
	}
}

func TestChangedRunningMCPParametersStopAndAttemptNewSettingsWithoutReverting(t *testing.T) {
	svc, env, store := fixture(t)
	svc.Source.Catalog[0].Skill = nil
	svc.Source.Catalog[0].MCP = &catalog.MCP{Image: "fixture", HostPortInput: "port", ContainerPort: 8765, Transport: "streamable-http"}
	svc.Source.Catalog[0].Inputs = []catalog.Input{{Name: "port", Type: "integer", Required: true}}
	env.Kind = "generic"
	env.ConfigPath = filepath.Join(t.TempDir(), "manual.json")
	runtime := &changedRuntime{failRestart: true}
	svc.Options.Runtime = runtime
	_, err := svc.Install(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{env}, Inputs: map[string]any{"port": int64(9000)}})
	if err == nil || !strings.Contains(err.Error(), "port 9000 is already allocated") {
		t.Fatalf("restart failure hidden: %v", err)
	}
	if runtime.starts != 2 || runtime.stops != 1 {
		t.Fatalf("changed settings did not attempt immediate replacement: starts=%d stops=%d", runtime.starts, runtime.stops)
	}
	answers, err := store.Answers(state.Key{Source: "fixture", Package: "demo", Target: "default"})
	if err != nil || answers["port"] != json.Number("9000") {
		t.Fatalf("new desired setting reverted: %#v, %v", answers, err)
	}
	rows, err := store.Installations()
	if err != nil || len(rows) != 0 {
		t.Fatalf("failed replacement claimed running: %#v, %v", rows, err)
	}
}

func TestFailedRegistrationUpdateDoesNotReportOldRegistrationAsApplied(t *testing.T) {
	svc, env, store := fixture(t)
	svc.Source.Catalog[0].Skill = nil
	svc.Source.Catalog[0].MCP = &catalog.MCP{Name: "demo", Transport: "streamable-http"}
	env.Kind = "generic"
	env.ConfigPath = filepath.Join(t.TempDir(), "manual.json")
	oldURL := "http://127.0.0.1:8765/mcp"
	newURL := "http://127.0.0.1:9000/mcp"
	if _, err := svc.Install(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{env}, ExternalURL: oldURL}); err != nil {
		t.Fatal(err)
	}
	svc.Options.RecordInstallation = func(state.Installation) error { return errors.New("ledger disk full") }
	result, err := svc.Install(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{env}, ExternalURL: newURL})
	if err == nil || !strings.Contains(err.Error(), "ledger disk full") {
		t.Fatalf("registration failure hidden: %v", err)
	}
	if len(result.Changes) != 0 {
		t.Fatalf("old registration incorrectly reported as newly applied: %#v", result.Changes)
	}
	rows, err := store.Installations()
	if err != nil || len(rows) != 1 || rows[0].URL != oldURL {
		t.Fatalf("failed update changed actual registration: %#v, %v", rows, err)
	}
}

func TestSourceRegistryFailureStillReportsAppliedRegistration(t *testing.T) {
	svc, env, store := fixture(t)
	svc.Source.Catalog[0].Skill = nil
	svc.Source.Catalog[0].MCP = &catalog.MCP{Name: "demo", Transport: "streamable-http"}
	env.Kind = "generic"
	env.ConfigPath = filepath.Join(t.TempDir(), "manual.json")
	svc.Options.RecordInstallation = func(row state.Installation) error {
		if err := store.Record(row); err != nil {
			return err
		}
		return os.Mkdir(filepath.Join(store.Root(), "manager", "sources.json"), 0700)
	}
	result, err := svc.Install(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{env}, ExternalURL: "http://127.0.0.1:8765/mcp"})
	if err == nil {
		t.Fatal("source registry write unexpectedly succeeded")
	}
	if len(result.Changes) != 1 || result.Changes[0].AgentID != env.ID || result.Changes[0].Component != "mcp" {
		t.Fatalf("successful agent registration hidden by later state error: %#v, %v", result, err)
	}
}

func TestInstallRejectsUnsupportedMCPAdapterBeforeRuntimeStart(t *testing.T) {
	svc, env, store := fixture(t)
	svc.Source.Catalog[0].Skill = nil
	svc.Source.Catalog[0].MCP = &catalog.MCP{Name: "demo", Transport: "streamable-http"}
	runtime := &fakeRuntime{}
	svc.Options.Runtime = runtime
	env.Kind = "intellij"
	_, err := svc.Install(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{env}})
	if err == nil || !strings.Contains(err.Error(), "JetBrains") {
		t.Fatalf("unsupported adapter accepted: %v", err)
	}
	if runtime.starts != 0 {
		t.Fatalf("unsupported destination started Docker %d time(s)", runtime.starts)
	}
	rows, err := store.Installations()
	if err != nil || len(rows) != 0 {
		t.Fatalf("unsupported destination changed state: %+v, %v", rows, err)
	}
}

func TestDefaultInventoryKeyCanStart(t *testing.T) {
	svc, _, _ := fixture(t)
	svc.Source.Catalog[0].MCP = &catalog.MCP{Image: "fixture", ContainerPort: 8765, HostPortInput: "port"}
	svc.Source.Catalog[0].Inputs = []catalog.Input{{Name: "port", Type: "integer", Default: int64(18765)}}
	svc.Options.Runtime = &fakeRuntime{}
	_, e := svc.MCP(context.Background(), MCPRequest{Action: "start", Package: "demo", Target: "default"})
	if e != nil {
		t.Fatal(e)
	}
}
func TestUninstallMCPPreservesOtherHome(t *testing.T) {
	svc, a, s := fixture(t)
	svc.Source.Catalog[0].Skill = nil
	svc.Source.Catalog[0].MCP = &catalog.MCP{Name: "demo", Transport: "streamable-http"}
	a.Kind = "generic"
	a.ConfigPath = filepath.Join(a.Home, "manual.json")
	b := a
	b.Home = t.TempDir()
	b.ConfigPath = filepath.Join(b.Home, "manual.json")
	for _, env := range []agents.Environment{a, b} {
		_, e := svc.Install(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{env}, ExternalURL: "https://fixture.invalid/mcp"})
		if e != nil {
			t.Fatal(e)
		}
	}
	_, e := svc.Uninstall(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{a}})
	if e != nil {
		t.Fatal(e)
	}
	rows, _ := s.Installations()
	if len(rows) != 1 || rows[0].Destination != b.ConfigPath {
		t.Fatal(rows)
	}
}
func TestRegistrationNamesAreUnambiguous(t *testing.T) {
	a := state.Key{Source: "same", Package: "foo", Environment: "dev-west", Target: "prod"}
	b := state.Key{Source: "same", Package: "foo-dev", Environment: "west", Target: "prod"}
	if registrationName(a) == registrationName(b) {
		t.Fatal(registrationName(a))
	}
}

func TestUIUninstallUsesPersistedCustomAgentHome(t *testing.T) {
	svc, env, s := fixture(t)
	env.ID = "generic:work"
	env.Kind = "generic"
	env.ConfigPath = filepath.Join(env.Home, "manual.json")
	svc.Source.Catalog[0].MCP = &catalog.MCP{Name: "demo", Transport: "streamable-http"}
	_, e := svc.Install(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{env}, ExternalURL: "https://fixture.invalid/mcp"})
	if e != nil {
		t.Fatal(e)
	}
	_, e = svc.UIRun(context.Background(), "uninstall", "fixture", "demo", "generic:work", "", "default")
	if e != nil {
		t.Fatal(e)
	}
	rows, _ := s.Installations()
	if len(rows) != 0 {
		t.Fatal(rows)
	}
}
func TestUISettingsPreservesMigratedAgentPreference(t *testing.T) {
	svc, _, s := fixture(t)
	if e := state.WriteJSON(filepath.Join(s.Root(), "manager", "settings.json"), map[string]any{"agents": []string{"opencode", "codex"}}); e != nil {
		t.Fatal(e)
	}
	v, e := svc.UISettings(context.Background())
	if e != nil || v["default_agents"] != "opencode,codex" {
		t.Fatal(v, e)
	}
}

// A future-install preference must not discard migration metadata or mutate registrations.
func TestUISetDefaultAgentsPreservesOtherSettings(t *testing.T) {
	svc, _, store := fixture(t)
	path := filepath.Join(store.Root(), "manager", "settings.json")
	if err := state.WriteJSON(path, map[string]any{"agents": []string{"codex"}, "environment_root": "/team", "extra": map[string]any{"enabled": true}}); err != nil {
		t.Fatal(err)
	}
	if err := svc.UISetDefaultAgents(context.Background(), []string{"claude", "opencode"}); err != nil {
		t.Fatal(err)
	}
	var saved map[string]any
	data, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(data, &saved) != nil {
		t.Fatal(err, string(data))
	}
	if got := saved["agents"]; !reflect.DeepEqual(got, []any{"claude", "opencode"}) {
		t.Fatalf("defaults = %#v", got)
	}
	if saved["environment_root"] != "/team" || !reflect.DeepEqual(saved["extra"], map[string]any{"enabled": true}) {
		t.Fatalf("other settings lost: %#v", saved)
	}
	rows, err := store.Installations()
	if err != nil || len(rows) != 0 {
		t.Fatalf("registrations changed: %#v, %v", rows, err)
	}
}

func TestUISetDefaultAgentsRejectsUnsupportedMCPDestinationsWithoutChangingSettings(t *testing.T) {
	svc, _, store := fixture(t)
	path := filepath.Join(store.Root(), "manager", "settings.json")
	if err := state.WriteJSON(path, map[string]any{"agents": []string{"codex"}}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	for _, ids := range [][]string{{"all"}, {"intellij"}, {"codex", "no-such-agent"}} {
		if err := svc.UISetDefaultAgents(context.Background(), ids); err == nil {
			t.Fatalf("accepted %#v", ids)
		}
		after, _ := os.ReadFile(path)
		if !bytes.Equal(before, after) {
			t.Fatalf("invalid %#v changed preferences", ids)
		}
	}
}

func TestUIAgentDefaultOptionsExcludesAllAndUnsupportedAdapters(t *testing.T) {
	svc, _, _ := fixture(t)
	ids, err := svc.UIAgentDefaultOptions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"codex", "claude", "opencode"} {
		if !slices.Contains(ids, want) {
			t.Fatalf("missing %s: %#v", want, ids)
		}
	}
	if slices.Contains(ids, "all") || slices.Contains(ids, "intellij") {
		t.Fatalf("unsupported default option exposed: %#v", ids)
	}
}

type prepareExecutor struct{ output string }

func (f prepareExecutor) Run(context.Context, []string, string, []byte, map[string]string, func([]byte)) ([]byte, error) {
	return []byte(f.output), nil
}

type countingPrepareExecutor struct {
	calls int
}

func (f *countingPrepareExecutor) Run(context.Context, []string, string, []byte, map[string]string, func([]byte)) ([]byte, error) {
	f.calls++
	return []byte(`{"runtime":{"image":"fixture","host":"127.0.0.1","host_port":9000,"container_port":8765,"transport":"streamable-http","endpoint_path":"/mcp"}}`), nil
}

func TestChangedRunningMCPPreparesOnlyOnce(t *testing.T) {
	svc, env, _ := fixture(t)
	svc.Source.Catalog[0].Skill = nil
	svc.Source.Catalog[0].MCP = &catalog.MCP{Transport: "streamable-http", Actions: map[string]catalog.Command{"prepare": {Argv: []string{"fixture-prepare"}}}}
	env.Kind = "generic"
	env.ConfigPath = filepath.Join(t.TempDir(), "manual.json")
	prepare := &countingPrepareExecutor{}
	svc.Options.Runner = prepare
	runtime := &changedRuntime{}
	svc.Options.Runtime = runtime
	_, err := svc.Install(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{env}})
	if err != nil {
		t.Fatal(err)
	}
	if prepare.calls != 1 || runtime.starts != 2 || runtime.stops != 1 {
		t.Fatalf("Save repeated prepare or skipped replacement: prepare=%d starts=%d stops=%d", prepare.calls, runtime.starts, runtime.stops)
	}
}

func TestExplicitCredentialSwitchOverridesEnvironmentPrefill(t *testing.T) {
	svc, _, _ := fixture(t)
	svc.Source.Catalog[0].Inputs = []catalog.Input{
		{Name: "token", Type: "secret", ExclusiveGroup: "cluster_credentials"},
		{Name: "kubeconfig", Type: "file", ExclusiveGroup: "cluster_credentials"},
	}
	svc.Source.EnvironmentRoot = filepath.Join(t.TempDir(), "environments")
	targetPath := filepath.Join(svc.Source.EnvironmentRoot, "company", "demo", "production.toml")
	if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetPath, []byte("kubeconfig = './source.yaml'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	values, _, _, err := svc.resolve(context.Background(), svc.Source.Catalog[0], "company", "production", map[string]any{"token": "new-token", "kubeconfig": ""}, false, false)
	if err != nil || values["token"] != "new-token" || values["kubeconfig"] != "" {
		t.Fatalf("explicit switch did not clear target kubeconfig: %#v, %v", values, err)
	}
}
func TestPrepareChoicesReachEditableForm(t *testing.T) {
	svc, env, _ := fixture(t)
	svc.Source.Catalog[0].Skill = nil
	svc.Source.Catalog[0].Inputs = []catalog.Input{{Name: "connection", Type: "string", Required: true}}
	svc.Source.Catalog[0].MCP = &catalog.MCP{Transport: "streamable-http", Actions: map[string]catalog.Command{"prepare": {Argv: []string{"fixture-helper"}}}}
	svc.Options.Runner = prepareExecutor{output: `{"choices":{"connection":[{"value":"selected","label":"Selected"}]},"runtime":{"image":"fixture","host":"127.0.0.1","host_port":8765,"container_port":8765}}`}
	svc.Options.Runtime = &fakeRuntime{}
	seen := false
	svc.Options.Editor = func(_ context.Context, defs []catalog.Input, values map[string]any) (map[string]any, error) {
		if len(defs[0].Options) > 0 {
			seen = true
			values["connection"] = "selected"
		} else {
			values["connection"] = "initial"
		}
		return values, nil
	}
	env.Kind = "generic"
	env.ConfigPath = filepath.Join(env.Home, "manual.json")
	_, e := svc.Install(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{env}, Interactive: true})
	if e != nil {
		t.Fatal(e)
	}
	if !seen {
		t.Fatal("prepare choices never reached form")
	}
}

func TestRequiredDynamicChoiceDoesNotNeedInitialGuess(t *testing.T) {
	svc, env, _ := fixture(t)
	svc.Source.Catalog[0].Skill = nil
	svc.Source.Catalog[0].Inputs = []catalog.Input{{Name: "connection", Type: "choice", Required: true}}
	svc.Source.Catalog[0].MCP = &catalog.MCP{Transport: "streamable-http", Actions: map[string]catalog.Command{"prepare": {Argv: []string{"fixture-helper"}}}}
	svc.Options.Runner = prepareExecutor{output: `{"choices":{"connection":[{"value":"selected"}]},"runtime":{"image":"fixture","host":"127.0.0.1","host_port":8765,"container_port":8765}}`}
	svc.Options.Runtime = &fakeRuntime{}
	svc.Options.Editor = func(_ context.Context, defs []catalog.Input, values map[string]any) (map[string]any, error) {
		if len(defs[0].Options) == 0 {
			if defs[0].Required {
				return nil, errors.New("required before options available")
			}
		} else {
			values["connection"] = "selected"
		}
		return values, nil
	}
	env.Kind = "generic"
	env.ConfigPath = filepath.Join(env.Home, "manual.json")
	_, e := svc.Install(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{env}, Interactive: true})
	if e != nil {
		t.Fatal(e)
	}
}
func TestCopiedSourceRequiresExplicitRegistryUpdate(t *testing.T) {
	svc, env, s := fixture(t)
	_, e := svc.Install(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{env}})
	if e != nil {
		t.Fatal(e)
	}
	originalRoot := svc.Source.Root
	next := svc.Source
	next.Root = t.TempDir()
	next.ManifestPath = filepath.Join(next.Root, "aact.toml")
	dir := filepath.Join(next.Root, "pkg")
	os.MkdirAll(dir, 0755)
	b, _ := os.ReadFile(filepath.Join(next.Catalog[0].Dir, "SKILL.md"))
	os.WriteFile(filepath.Join(dir, "SKILL.md"), b, 0644)
	next.Catalog = append([]catalog.Package{}, next.Catalog...)
	next.Catalog[0].Dir = dir
	copySvc := New(next, s, Options{})
	_, e = copySvc.Install(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{env}})
	if e == nil || !strings.Contains(e.Error(), "--update-source") {
		t.Fatal(e)
	}
	refs, _ := copySvc.sourceRefs()
	if len(refs) != 1 || refs[0].Root != originalRoot {
		t.Fatal(refs)
	}
	_, e = copySvc.Install(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{env}, UpdateSource: true})
	if e != nil {
		t.Fatal(e)
	}
	refs, _ = copySvc.sourceRefs()
	if refs[0].Root != next.Root {
		t.Fatal(refs)
	}
}
func TestRegistrationLedgerFailureRestoresAgentFile(t *testing.T) {
	svc, env, _ := fixture(t)
	svc.Source.Catalog[0].Skill = nil
	svc.Source.Catalog[0].MCP = &catalog.MCP{Transport: "streamable-http"}
	env.Kind = "generic"
	env.ConfigPath = filepath.Join(env.Home, "manual.json")
	os.MkdirAll(env.Home, 0755)
	original := []byte("{\n // Preserve this comment\n \"servers\": {}, \"unrelated\":true\n}\n")
	os.WriteFile(env.ConfigPath, original, 0600)
	svc.Options.RecordInstallation = func(state.Installation) error { return errors.New("synthetic ledger failure") }
	_, e := svc.Install(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{env}, ExternalURL: "https://fixture.invalid/mcp"})
	if e == nil {
		t.Fatal("ledger failure ignored")
	}
	got, _ := os.ReadFile(env.ConfigPath)
	if !bytes.Equal(got, original) {
		t.Fatalf("prior file changed: %s", got)
	}
}
func TestUnregisterLedgerFailureRestoresAgentFile(t *testing.T) {
	svc, env, _ := fixture(t)
	svc.Source.Catalog[0].Skill = nil
	svc.Source.Catalog[0].MCP = &catalog.MCP{Transport: "streamable-http"}
	env.Kind = "generic"
	env.ConfigPath = filepath.Join(env.Home, "manual.json")
	_, e := svc.Install(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{env}, ExternalURL: "https://fixture.invalid/mcp"})
	if e != nil {
		t.Fatal(e)
	}
	original, _ := os.ReadFile(env.ConfigPath)
	svc.Options.RemoveInstallation = func(state.Installation) error { return errors.New("synthetic ledger failure") }
	_, e = svc.Uninstall(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{env}})
	if e == nil {
		t.Fatal("ledger failure ignored")
	}
	got, _ := os.ReadFile(env.ConfigPath)
	if !bytes.Equal(got, original) {
		t.Fatalf("prior file changed: %s", got)
	}
}
func TestConfiguredInstallHasNoPrompt(t *testing.T) {
	svc, env, _ := fixture(t)
	called := false
	svc.Options.Editor = func(context.Context, []catalog.Input, map[string]any) (map[string]any, error) {
		called = true
		return nil, nil
	}
	out, e := svc.Install(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{env}})
	if e != nil {
		t.Fatal(e)
	}
	if called || len(out.Changes) != 1 {
		t.Fatal(called, out)
	}
	if _, e = os.Stat(filepath.Join(env.SkillsDir, "demo", "SKILL.md")); e != nil {
		t.Fatal(e)
	}
}
func TestMissingInputSuggestsInteractive(t *testing.T) {
	svc, env, _ := fixture(t)
	svc.Source.Catalog[0].Inputs = []catalog.Input{{Name: "root", Type: "directory", Required: true}}
	_, e := svc.Install(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{env}})
	if e == nil || !strings.Contains(e.Error(), "--interactive") {
		t.Fatal(e)
	}
	if _, e = os.Stat(env.SkillsDir); !os.IsNotExist(e) {
		t.Fatal("wrote agent before input validation")
	}
}
func TestNoImplicitAllAgents(t *testing.T) {
	svc, _, _ := fixture(t)
	_, e := svc.Install(context.Background(), InstallRequest{Package: "demo"})
	if e == nil {
		t.Fatal("no agents accepted")
	}
}
func TestPartialAgentFailureRecorded(t *testing.T) {
	svc, env, s := fixture(t)
	bad := env
	bad.ID = "blocked"
	bad.SkillsDir = filepath.Join(t.TempDir(), "blocked")
	os.WriteFile(bad.SkillsDir, []byte("file"), 0600)
	r, e := svc.Install(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{env, bad}})
	if e == nil || len(r.Errors) != 1 {
		t.Fatal(r, e)
	}
	rows, e := s.Installations()
	if e != nil || len(rows) != 1 || rows[0].AgentID != "codex" {
		t.Fatal(rows, e)
	}
}
func TestAnswersKeepLastValidEditAfterFailedApplyWithoutPersistingSecrets(t *testing.T) {
	svc, env, s := fixture(t)
	svc.Source.Catalog[0].Inputs = []catalog.Input{{Name: "label", Type: "string"}, {Name: "token", Type: "secret"}}
	_, e := svc.Install(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{env}, Inputs: map[string]any{"label": "yes", "token": "never-persist"}})
	if e != nil {
		t.Fatal(e)
	}
	a, e := s.Answers(state.Key{Source: "fixture", Package: "demo", Target: "default"})
	if e != nil || a["label"] != "yes" || a["token"] != nil {
		t.Fatal(a, e)
	}
	bad := env
	bad.ID = "bad"
	bad.SkillsDir = filepath.Join(t.TempDir(), "blocked")
	os.WriteFile(bad.SkillsDir, []byte("file"), 0600)
	_, e = svc.Install(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{bad}, Inputs: map[string]any{"label": "failed"}})
	if e == nil {
		t.Fatal("expected failure")
	}
	a, _ = s.Answers(state.Key{Source: "fixture", Package: "demo", Target: "default"})
	if a["label"] != "failed" || a["token"] != nil {
		t.Fatal(a)
	}
}
func TestRenderedInputWithoutGeneratorAndRemove(t *testing.T) {
	svc, env, _ := fixture(t)
	p := &svc.Source.Catalog[0]
	p.Inputs = []catalog.Input{{Name: "label", Type: "string", Required: true}}
	p.Templates = []catalog.Template{{Source: "SKILL.md.mustache", Destination: "SKILL.md"}}
	os.WriteFile(filepath.Join(p.Dir, "SKILL.md.mustache"), []byte("Hello {{{inputs.label}}}"), 0644)
	_, e := svc.Install(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{env}, Inputs: map[string]any{"label": "world"}})
	if e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(filepath.Join(env.SkillsDir, "demo", "SKILL.md"))
	if e != nil || string(b) != "Hello world" {
		t.Fatal(string(b), e)
	}
	_, e = svc.Uninstall(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{env}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = os.Lstat(filepath.Join(env.SkillsDir, "demo")); !os.IsNotExist(e) {
		t.Fatal(e)
	}
}

func TestDomainReviewFailedSourceUpdateRetainsRegistry(t *testing.T) {
	svc, env, store := fixture(t)
	if _, err := svc.Install(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{env}}); err != nil {
		t.Fatal(err)
	}
	oldRoot := svc.Source.Root
	next := svc.Source
	next.Root = t.TempDir()
	next.ManifestPath = filepath.Join(next.Root, "aact.toml")
	next.Catalog = append([]catalog.Package{}, next.Catalog...)
	next.Catalog[0].Dir = filepath.Join(next.Root, "pkg")
	os.MkdirAll(next.Catalog[0].Dir, 0755)
	data, _ := os.ReadFile(filepath.Join(svc.Source.Catalog[0].Dir, "SKILL.md"))
	os.WriteFile(filepath.Join(next.Catalog[0].Dir, "SKILL.md"), data, 0644)
	blocked := env
	blocked.SkillsDir = filepath.Join(t.TempDir(), "blocked")
	os.WriteFile(blocked.SkillsDir, []byte("file"), 0600)
	copySvc := New(next, store, Options{})
	if _, err := copySvc.Install(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{blocked}, UpdateSource: true}); err == nil {
		t.Fatal("expected failed update")
	}
	refs, err := copySvc.sourceRefs()
	if err != nil || refs[0].Root != oldRoot {
		t.Fatalf("failed update replaced selected source: %#v %v", refs, err)
	}
}
func TestDomainReviewGenericHomesPreservedWithDefaultArtifact(t *testing.T) {
	svc, a, store := fixture(t)
	svc.Source.Catalog[0].Skill = nil
	svc.Source.Catalog[0].MCP = &catalog.MCP{Transport: "streamable-http"}
	a.Kind = "generic"
	a.ID = "generic"
	a.ConfigPath = ""
	b := a
	b.Home = t.TempDir()
	for _, env := range []agents.Environment{a, b} {
		if _, err := svc.Install(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{env}, ExternalURL: "https://fixture.invalid/mcp"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.Uninstall(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{a}}); err != nil {
		t.Fatal(err)
	}
	rows, err := store.Installations()
	if err != nil || len(rows) != 1 || rows[0].AgentHome != b.Home {
		t.Fatalf("uninstall A lost B default manual artifact: %#v %v", rows, err)
	}
}
