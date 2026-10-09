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
	// Agent adapters must only inspect isolated fixture homes. These tests
	// must never read or write the developer's live agent configuration.
	isolateUXUserHome(t, filepath.Join(root, "user-home"))
	t.Setenv("CODEX_HOME", "")
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
	probeHome := filepath.Join(root, "home")
	probe := &agents.DiscoveryProbe{
		GOOS: "linux", Home: probeHome,
		Getenv: os.Getenv,
		LookPath: func(name string) (string, error) {
			switch name {
			case "codex", "opencode", "claude":
				return filepath.Join(root, "bin", name), nil
			default:
				return "", os.ErrNotExist
			}
		},
	}
	src := config.Source{ID: "fixture", Root: root, ManifestPath: filepath.Join(root, "aact.toml"), Catalog: []catalog.Package{{ID: "demo", Name: "Demo", Dir: pkg, Skill: &catalog.Skill{Name: "demo"}}}, PackageDefaults: map[string]map[string]any{}}
	return New(src, s, Options{DiscoveryProbe: probe}), env, s
}

func TestFixtureDiscoveryProbeIsIndependentOfHostPATH(t *testing.T) {
	svc, _, _ := fixture(t)
	if svc.Options.DiscoveryProbe == nil {
		t.Fatal("fixture must provide an explicit discovery probe")
	}
	for _, name := range []string{"codex", "opencode", "claude"} {
		if path, err := svc.Options.DiscoveryProbe.LookPath(name); err != nil || path == "" {
			t.Errorf("fixture CLI %q was not discoverable: path=%q err=%v", name, path, err)
		}
	}
	if path, err := svc.Options.DiscoveryProbe.LookPath("unknown-cli"); err == nil || path != "" {
		t.Errorf("unknown CLI unexpectedly discovered: path=%q err=%v", path, err)
	}
}

func isolateUXUserHome(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
}

func setFixtureProbeHome(svc *Service, home string) {
	if svc.Options.DiscoveryProbe != nil {
		svc.Options.DiscoveryProbe.Home = home
	}
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
	env.Kind = "opencode"
	env.ID = "opencode"
	env.ConfigPath = filepath.Join(t.TempDir(), "manual.json")
	svc.Source.Catalog[0].MCP = &catalog.MCP{Name: "demo", Transport: "streamable-http", RegistrationTimeoutMS: 60000}
	runtime := &fakeRuntime{}
	svc.Options.Runtime = runtime
	out, e := svc.applyProfileFixture(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{env}, ExternalURL: "https://fixture.invalid/mcp"})
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
	if !rows[1].ExternalRegistration {
		t.Fatalf("explicit ExternalURL registration lost its provenance: %+v", rows[1])
	}
	_, e = svc.removeProfileFixture(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{env}})
	if e != nil {
		t.Fatal(e)
	}
}

func TestInstallBindsEveryNamedMCPToItsOwnExternalEndpoint(t *testing.T) {
	svc, agent, store := fixture(t)
	svc.Source.Catalog[0].MCPs = []catalog.MCP{
		{Name: "primary", Transport: "streamable-http"},
		{Name: "secondary", Transport: "streamable-http"},
	}
	agent.Kind = "opencode"
	agent.ID = "opencode"
	agent.ConfigPath = filepath.Join(agent.Home, "mcp.json")
	result, err := svc.applyProfileFixture(context.Background(), InstallRequest{
		Package: "demo", Agents: []agents.Environment{agent}, ExternalURLs: map[string]string{
			"primary": "http://primary.invalid/mcp", "secondary": "http://secondary.invalid/mcp",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := store.Installations()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]string{}
	for _, row := range rows {
		if row.Component == "mcp" {
			seen[row.Key.MCP] = row.URL
		}
	}
	if seen["primary"] != "http://primary.invalid/mcp" || seen["secondary"] != "http://secondary.invalid/mcp" || len(result.Errors) != 0 {
		t.Fatalf("MCP endpoints were not independently bound: result=%+v rows=%+v", result, rows)
	}
}

func TestMultiMCPRecordFailureReportsAchievedChildRegistration(t *testing.T) {
	svc, agent, store := fixture(t)
	svc.Source.Catalog[0].MCPs = []catalog.MCP{{Name: "alpha", Transport: "streamable-http"}, {Name: "beta", Transport: "streamable-http"}}
	agent.Kind = "opencode"
	agent.ID = "opencode"
	agent.ConfigPath = filepath.Join(agent.Home, "mcp.json")
	staleBeta := state.Installation{Key: state.Key{Source: "fixture", Package: "demo", Target: "default", MCP: "beta"}, AgentID: agent.ID, AgentHome: agent.Home, AgentKind: agent.Kind, Component: "mcp", Destination: agent.ConfigPath, RegistrationName: "demo-beta", URL: "http://stale.invalid/mcp"}
	if err := store.Record(staleBeta); err != nil {
		t.Fatal(err)
	}
	svc.Options.RecordInstallation = func(row state.Installation) error {
		if row.Key.MCP == "beta" {
			return errors.New("beta ledger failure")
		}
		return store.Record(row)
	}
	result, err := svc.applyProfileFixture(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{agent}, ExternalURLs: map[string]string{"alpha": "http://alpha.invalid/mcp", "beta": "http://beta.invalid/mcp"}})
	if err == nil {
		t.Fatal("expected second child registration to fail")
	}
	if !strings.Contains(err.Error(), "beta ledger failure") || !strings.Contains(result.Target, "fixture/demo") || result.Step != "record" {
		t.Fatalf("child failure lost target/step detail: target=%q step=%q err=%v", result.Target, result.Step, err)
	}
	var changed []string
	for _, row := range result.Changes {
		if row.Component == "mcp" {
			changed = append(changed, row.Key.MCP)
		}
	}
	if !slices.Contains(changed, "alpha") || !slices.Contains(changed, "beta") || len(changed) != 2 {
		t.Fatalf("partial result omitted achieved registration after record failure: changed=%v result=%+v", changed, result)
	}
	rows, readErr := store.Installations()
	if readErr != nil {
		t.Fatal(readErr)
	}
	for _, row := range rows {
		if row.Key.MCP == "beta" && row.URL != "http://stale.invalid/mcp" {
			t.Fatalf("failed ledger write replaced ownership row: %+v", row)
		}
	}
}

func TestMultiMCPChangesExcludeStaleRowsFromOtherConfigPath(t *testing.T) {
	svc, agent, store := fixture(t)
	svc.Source.Catalog[0].MCPs = []catalog.MCP{{Name: "alpha", Transport: "streamable-http"}}
	agent.Kind = "opencode"
	agent.ID = "opencode"
	agent.ConfigPath = filepath.Join(agent.Home, "current.json")
	stale := state.Installation{Key: state.Key{Source: "fixture", Package: "demo", Target: "default", MCP: "alpha"}, AgentID: agent.ID, AgentHome: agent.Home, AgentKind: agent.Kind, Component: "mcp", Destination: filepath.Join(agent.Home, "old.json"), RegistrationName: "demo-alpha", URL: "http://old.invalid/mcp"}
	if err := store.Record(stale); err != nil {
		t.Fatal(err)
	}
	result, err := svc.applyProfileFixture(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{agent}, ExternalURLs: map[string]string{"alpha": "http://new.invalid/mcp"}})
	if err != nil {
		t.Fatal(err)
	}
	expectedPath, err := agents.ResolveConfigWritePath(agent)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range result.Changes {
		if row.Component == "mcp" && row.Destination != expectedPath {
			t.Fatalf("untouched old config path reported as applied: %+v", result.Changes)
		}
	}
}

type keyedMCPRuntime struct {
	starts []state.Key
	stops  []state.Key
}

func (r *keyedMCPRuntime) Start(_ context.Context, key state.Key, _ mcp.RunSpec) (mcp.Instance, error) {
	r.starts = append(r.starts, key)
	return mcp.Instance{Key: key, URL: "http://local-" + key.MCP + ".example/mcp"}, nil
}
func (r *keyedMCPRuntime) Stop(_ context.Context, key state.Key) error {
	r.stops = append(r.stops, key)
	return nil
}
func (*keyedMCPRuntime) List(context.Context) ([]mcp.Instance, error) { return nil, nil }
func (*keyedMCPRuntime) Logs(context.Context, state.Key) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}

func TestInstallCanAttachOneMCPExternallyAndStartItsLocalSibling(t *testing.T) {
	svc, agent, store := fixture(t)
	svc.Source.Catalog[0].MCPs = []catalog.MCP{
		{Name: "external", Transport: "streamable-http"},
		{Name: "local", Transport: "streamable-http"},
	}
	agent.Kind = "opencode"
	agent.ID = "opencode"
	agent.ConfigPath = filepath.Join(agent.Home, "mcp.json")
	runtime := &keyedMCPRuntime{}
	svc.Options.Runtime = runtime
	_, err := svc.applyProfileFixture(context.Background(), InstallRequest{
		Package: "demo", Agents: []agents.Environment{agent},
		ExternalURLs: map[string]string{"external": "http://foreign.example/mcp"},
	})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := store.Installations()
	if err != nil {
		t.Fatal(err)
	}
	endpoints := map[string]string{}
	for _, row := range rows {
		if row.Component == "mcp" {
			endpoints[row.Key.MCP] = row.URL
			if row.ExternalRegistration != (row.Key.MCP == "external") {
				t.Errorf("external provenance for %s = %v", row.Key.MCP, row.ExternalRegistration)
			}
		}
	}
	if endpoints["external"] != "http://foreign.example/mcp" || endpoints["local"] != "http://local-local.example/mcp" || len(runtime.starts) != 1 || runtime.starts[0].MCP != "local" {
		t.Fatalf("mixed endpoints were not kept child-specific: endpoints=%v starts=%+v", endpoints, runtime.starts)
	}
}

func TestRunProfileMCPStartAndStopUseNamedChildIdentity(t *testing.T) {
	svc, _, _ := fixture(t)
	svc.Source.Catalog[0].MCPs = []catalog.MCP{{Name: "primary", Transport: "streamable-http"}}
	runtime := &keyedMCPRuntime{}
	svc.Options.Runtime = runtime
	ref := profileRefForFixture(svc, svc.Source.ID, "demo", "default")
	if _, err := svc.RunProfileMCP(context.Background(), "start", ProfileRequest{Ref: ref}, "primary"); err != nil {
		t.Fatal(err)
	}
	if len(runtime.starts) != 1 || runtime.starts[0].MCP != "primary" {
		t.Fatalf("single list MCP start used wrong key: %+v", runtime.starts)
	}
	if _, err := svc.RunProfileMCP(context.Background(), "stop", ProfileRequest{Ref: ref}, "primary"); err != nil {
		t.Fatal(err)
	}
	if len(runtime.stops) != 1 || runtime.stops[0].MCP != "primary" {
		t.Fatalf("single list MCP stop used wrong key: %+v", runtime.stops)
	}
}

func TestInstallDoesNotRequireHiddenConditionalInput(t *testing.T) {
	svc, agent, _ := fixture(t)
	svc.Source.Catalog[0].Inputs = []catalog.Input{
		{Name: "mode", Type: "string", Default: "basic"},
		{Name: "secret", Type: "string", Required: true, VisibleWhen: map[string]any{"mode": "advanced"}},
	}
	if _, err := svc.applyProfileFixture(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{agent}}); err != nil {
		t.Fatalf("hidden required input blocked install: %v", err)
	}
}

func TestExternalCapabilityAttachDoesNotRunLocalMCPAuthOrRuntime(t *testing.T) {
	svc, agent, _ := fixture(t)
	svc.Source.Catalog[0].MCP = &catalog.MCP{Name: "demo", Transport: "streamable-http", Actions: map[string]catalog.Command{"authenticate": {Argv: []string{"fixture-auth"}}}}
	svc.Source.Catalog[0].Inputs = []catalog.Input{{Name: "token", Type: "secret", Required: false, ConfigKey: "service.token"}}
	agent.Kind = "opencode"
	agent.ID = "opencode"
	agent.ConfigPath = filepath.Join(agent.Home, "mcp.json")
	executor := &countingPrepareExecutor{}
	svc.Options.Runner = executor
	runtime := &changedRuntime{}
	svc.Options.Runtime = runtime
	if _, err := svc.applyProfileFixture(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{agent}, ExternalURL: "http://foreign.example/mcp"}); err != nil {
		t.Fatalf("external attach failed: %v", err)
	}
	if executor.calls != 0 || runtime.starts != 0 || runtime.stops != 0 {
		t.Fatalf("external attach ran local MCP setup: auth=%d starts=%d stops=%d", executor.calls, runtime.starts, runtime.stops)
	}
}

func TestExternalCapabilityAttachStillRequiresDeclaredSkillInputs(t *testing.T) {
	svc, agent, store := fixture(t)
	svc.Source.Catalog[0].MCP = &catalog.MCP{Name: "demo", Transport: "streamable-http"}
	svc.Source.Catalog[0].Inputs = []catalog.Input{{Name: "skill_output", Type: "string", Required: true, ConfigKey: "skill.output"}}
	agent.Kind = "opencode"
	agent.ID = "opencode"
	agent.ConfigPath = filepath.Join(agent.Home, "mcp.json")
	if _, err := svc.applyProfileFixture(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{agent}, ExternalURL: "http://foreign.example/mcp"}); err == nil || !strings.Contains(err.Error(), "skill_output") {
		t.Fatalf("required manifest input was skipped for external attach: %v", err)
	}
	rows, err := store.Installations()
	if err != nil || len(rows) != 0 {
		t.Fatalf("failed validation caused capability effects: %+v, %v", rows, err)
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
	if _, err := svc.applyProfileFixture(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	svc.Source.Catalog[0].MCP.RegistrationTimeoutMS = 60000
	if _, err := svc.applyProfileFixture(context.Background(), request); err != nil {
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
	result, err := svc.applyProfileFixture(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{env}, Inputs: map[string]any{"label": "new"}})
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
	_, err := svc.applyProfileFixture(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{env}, Inputs: map[string]any{"label": "retry-me"}})
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
	env.Kind = "opencode"
	env.ID = "opencode"
	env.ConfigPath = filepath.Join(t.TempDir(), "manual.json")
	runtime := &failingRuntime{}
	svc.Options.Runtime = runtime
	result, err := svc.applyProfileFixture(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{env}, Inputs: map[string]any{"port": int64(9000)}})
	if err == nil || !strings.Contains(err.Error(), "port 9000 is already allocated") {
		t.Fatalf("MCP startup cause hidden: %v", err)
	}
	if !result.Saved {
		t.Fatal("failed apply did not report that answers were saved")
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
	env.Kind = "opencode"
	env.ID = "opencode"
	env.ConfigPath = filepath.Join(t.TempDir(), "manual.json")
	runtime := &changedRuntime{failRestart: true}
	svc.Options.Runtime = runtime
	_, err := svc.applyProfileFixture(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{env}, Inputs: map[string]any{"port": int64(9000)}})
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

func TestFailedRegistrationLedgerUpdateReportsAchievedNewRegistration(t *testing.T) {
	svc, env, store := fixture(t)
	svc.Source.Catalog[0].Skill = nil
	svc.Source.Catalog[0].MCP = &catalog.MCP{Name: "demo", Transport: "streamable-http"}
	env.Kind = "opencode"
	env.ID = "opencode"
	env.ConfigPath = filepath.Join(t.TempDir(), "manual.json")
	oldURL := "http://127.0.0.1:8765/mcp"
	newURL := "http://127.0.0.1:9000/mcp"
	if _, err := svc.applyProfileFixture(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{env}, ExternalURL: oldURL}); err != nil {
		t.Fatal(err)
	}
	svc.Options.RecordInstallation = func(state.Installation) error { return errors.New("ledger disk full") }
	result, err := svc.applyProfileFixture(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{env}, ExternalURL: newURL})
	if err == nil || !strings.Contains(err.Error(), "ledger disk full") {
		t.Fatalf("registration failure hidden: %v", err)
	}
	if len(result.Changes) != 1 || result.Changes[0].URL != newURL {
		t.Fatalf("achieved registration update missing from result: %#v", result.Changes)
	}
	rows, err := store.Installations()
	if err != nil || len(rows) != 1 || rows[0].URL != oldURL {
		t.Fatalf("failed update changed actual registration: %#v, %v", rows, err)
	}
}

func TestRegistrationLedgerFailureStillReportsAppliedRegistration(t *testing.T) {
	svc, env, _ := fixture(t)
	svc.Source.Catalog[0].Skill = nil
	svc.Source.Catalog[0].MCP = &catalog.MCP{Name: "demo", Transport: "streamable-http"}
	env.Kind = "opencode"
	env.ID = "opencode"
	env.ConfigPath = filepath.Join(t.TempDir(), "manual.json")
	svc.Options.RecordInstallation = func(state.Installation) error { return errors.New("registration ledger disk full") }
	result, err := svc.applyProfileFixture(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{env}, ExternalURL: "http://127.0.0.1:8765/mcp"})
	if err == nil {
		t.Fatal("registration ledger failure unexpectedly succeeded")
	}
	if len(result.Changes) != 1 || result.Changes[0].AgentID != env.ID || result.Changes[0].Component != "mcp" {
		t.Fatalf("successful agent registration hidden by later state error: %#v, %v", result, err)
	}
}

func TestApplyProfileRejectsSkillsOnlyAdapterForMCPBeforeRuntimeStart(t *testing.T) {
	svc, env, store := fixture(t)
	svc.Source.Catalog[0].Skill = nil
	svc.Source.Catalog[0].MCP = &catalog.MCP{Name: "demo", Transport: "streamable-http"}
	runtime := &fakeRuntime{}
	svc.Options.Runtime = runtime
	ref := profileRefForFixture(svc, svc.Source.ID, "demo", "default")
	svc.Options.AgentScopes = map[string]agents.Scope{"generic": {ID: "generic", Home: env.Home, ExplicitHome: true}}
	_, err := svc.ApplyProfile(context.Background(), ProfileRequest{Ref: ref, DestinationIDs: []string{"generic"}})
	if err == nil || !strings.Contains(err.Error(), "cannot install every selected capability component") {
		t.Fatalf("skills-only adapter accepted selected MCP or misreported: %v", err)
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
	svc.Source.Catalog[0].MCP = &catalog.MCP{Name: "demo", Image: "fixture", ContainerPort: 8765, HostPortInput: "port"}
	svc.Source.Catalog[0].Inputs = []catalog.Input{{Name: "port", Type: "integer", Default: int64(18765)}}
	svc.Options.Runtime = &fakeRuntime{}
	ref := profileRefForFixture(svc, svc.Source.ID, "demo", "default")
	_, e := svc.RunProfileMCP(context.Background(), "start", ProfileRequest{Ref: ref}, "demo")
	if e != nil {
		t.Fatal(e)
	}
}
func TestUninstallMCPPreservesOtherHome(t *testing.T) {
	svc, a, s := fixture(t)
	svc.Source.Catalog[0].Skill = nil
	svc.Source.Catalog[0].MCP = &catalog.MCP{Name: "demo", Transport: "streamable-http"}
	a.Kind = "opencode"
	a.ID = "opencode"
	a.ConfigPath = filepath.Join(a.Home, "manual.jsonc")
	b, err := agents.ResolveEnvironment("claude", "claude", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ref := profileRefForFixture(svc, svc.Source.ID, "demo", "default")
	svc.Options.DiscoveryProbe = &agents.DiscoveryProbe{
		GOOS: "linux", Home: t.TempDir(), Getenv: func(string) string { return "" },
		LookPath: func(name string) (string, error) {
			if name == "opencode" || name == "claude" {
				return filepath.Join(t.TempDir(), "bin", name), nil
			}
			return "", os.ErrNotExist
		},
	}
	svc.Options.AgentScopes = map[string]agents.Scope{
		"opencode": {ID: "opencode", Home: a.Home, ConfigPathOverride: a.ConfigPath, ExplicitHome: true},
		"claude":   {ID: "claude", Home: b.Home, ConfigPathOverride: b.ConfigPath, ExplicitHome: true},
	}
	request := ProfileRequest{Ref: ref, DestinationIDs: []string{"opencode", "claude"}, ExternalURLs: map[string]string{"demo": "https://fixture.invalid/mcp"}}
	if _, err := svc.ApplyProfile(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	request.DestinationIDs = []string{"claude"}
	if _, err := svc.ApplyProfile(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	rows, err := s.Installations()
	if err != nil || len(rows) != 1 || rows[0].Destination != b.ConfigPath {
		t.Fatalf("removed one destination's MCP state: rows=%+v err=%v", rows, err)
	}
}
func TestLegacyRegistrationNameIsReadable(t *testing.T) {
	a := state.Key{Source: "same", Package: "foo", Environment: "dev-west", Target: "prod"}
	if got := registrationName(a); got != "foo-dev-west-prod" {
		t.Fatalf("legacy registration name = %q", got)
	}
}

func TestDeclaredRegistrationNameUsesInputVerbatim(t *testing.T) {
	definition := catalog.MCP{Name: "inspector", RegistrationNameInput: "registration"}
	got, err := declaredRegistrationName(definition, state.Key{Package: "ignored"}, map[string]any{"registration": "cluster-inspector-target-a"})
	if err != nil || got != "cluster-inspector-target-a" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := declaredRegistrationName(definition, state.Key{}, map[string]any{"registration": "-bad"}); err == nil {
		t.Fatal("invalid MCP CLI name accepted")
	}
}

func TestRegistrationRenameLedgerFailureReportsAchievedNewMCP(t *testing.T) {
	svc, _, store := fixture(t)
	home := filepath.Join(t.TempDir(), "opencode-home")
	env, err := agents.ResolveEnvironment("opencode", "opencode", home)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(env.ConfigPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(env.ConfigPath, []byte(`{"mcp":{"old-name":{"type":"remote","url":"https://old.invalid/mcp","enabled":true,"oauth":false,"timeout":30000}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	pkg := &svc.Source.Catalog[0]
	pkg.Skill = nil
	pkg.Inputs = []catalog.Input{{Name: "registration", Type: "string", Default: "new-name"}}
	pkg.MCP = &catalog.MCP{Name: "demo", Transport: "streamable-http", RegistrationNameInput: "registration"}
	ref := profileRefForFixture(svc, svc.Source.ID, pkg.ID, "default")
	key, err := store.ResolveProfileKey(ref.PackID, ref.CapabilityID, ref.Name)
	if err != nil {
		t.Fatal(err)
	}
	child := mcpProfileKey(key, *pkg, pkg.MCPDefinitions()[0])
	old := state.Installation{Key: child, AgentID: env.ID, AgentHome: env.Home, AgentKind: env.Kind, Component: "mcp", Destination: env.ConfigPath, RegistrationName: "old-name", URL: "https://old.invalid/mcp", Transport: "streamable-http", TimeoutMS: 30000}
	if err := store.Record(old); err != nil {
		t.Fatal(err)
	}
	svc.Options.RecordInstallation = func(state.Installation) error { return errors.New("ledger write failed") }
	svc.Options.AgentScopes = map[string]agents.Scope{"opencode": {ID: "opencode", Home: env.Home, ConfigPathOverride: env.ConfigPath, ExplicitHome: true}}
	result, err := svc.ApplyProfile(context.Background(), ProfileRequest{Ref: ref, Inputs: map[string]any{"registration": "new-name"}, DestinationIDs: []string{"opencode"}, ExternalURLs: map[string]string{"demo": "https://new.invalid/mcp"}})
	if err == nil || !strings.Contains(err.Error(), "ledger write failed") {
		t.Fatalf("expected truthful new-name failure, got result=%+v err=%v", result, err)
	}
	content, readErr := os.ReadFile(env.ConfigPath)
	if readErr != nil || strings.Contains(string(content), "old-name") || !strings.Contains(string(content), "new-name") {
		t.Fatalf("achieved rename must remain after ledger failure: path=%s content=%s read=%v result=%+v", env.ConfigPath, content, readErr, result)
	}
	rows, readErr := store.Installations()
	if readErr != nil {
		t.Fatal(readErr)
	}
	for _, row := range rows {
		if row.Component == "mcp" && row.RegistrationName == "old-name" {
			t.Fatalf("old ledger row survived confirmed removal: %+v", row)
		}
		if row.Component == "mcp" && row.RegistrationName == "new-name" {
			t.Fatalf("failed new registration ledger write was persisted: %+v", row)
		}
	}
	if len(result.Changes) != 1 || result.Changes[0].Component != "mcp" || result.Changes[0].RegistrationName != "new-name" {
		t.Fatalf("result should report the achieved new registration: %+v", result.Changes)
	}
}

func TestUIUninstallUsesPersistedCustomAgentHome(t *testing.T) {
	svc, env, s := fixture(t)
	env.ID = "generic:work"
	env.Kind = "opencode"
	env.ID = "opencode"
	env.ConfigPath = filepath.Join(env.Home, "manual.json")
	svc.Source.Catalog[0].MCP = &catalog.MCP{Name: "demo", Transport: "streamable-http"}
	_, e := svc.applyProfileFixture(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{env}, ExternalURL: "https://fixture.invalid/mcp"})
	if e != nil {
		t.Fatal(e)
	}
	ensureProfileForTest(svc, profileRefFromTestKey("fixture", "demo", "default"))
	_, e = svc.UIRun(context.Background(), "uninstall", "fixture", "demo", "default", "generic:work", "", "")
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
	for _, ids := range [][]string{{"all"}, {"generic"}, {"hermes"}, {"codex", "no-such-agent"}} {
		if err := svc.UISetDefaultAgents(context.Background(), ids); err == nil {
			t.Fatalf("accepted %#v", ids)
		}
		after, _ := os.ReadFile(path)
		if !bytes.Equal(before, after) {
			t.Fatalf("invalid %#v changed preferences", ids)
		}
	}
}

func TestUIAgentDefaultOptionsExcludesAllAndManualOrUnsupportedAdapters(t *testing.T) {
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
	for _, excluded := range []string{"all", "generic", "hermes", "intellij"} {
		if slices.Contains(ids, excluded) {
			t.Fatalf("manual or unsupported default option %q exposed: %#v", excluded, ids)
		}
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
	env.Kind = "opencode"
	env.ID = "opencode"
	env.ConfigPath = filepath.Join(t.TempDir(), "manual.json")
	prepare := &countingPrepareExecutor{}
	svc.Options.Runner = prepare
	runtime := &changedRuntime{}
	svc.Options.Runtime = runtime
	_, err := svc.applyProfileFixture(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{env}})
	if err != nil {
		t.Fatal(err)
	}
	if prepare.calls != 1 || runtime.starts != 2 || runtime.stops != 1 {
		t.Fatalf("Save repeated prepare or skipped replacement: prepare=%d starts=%d stops=%d", prepare.calls, runtime.starts, runtime.stops)
	}
}

func TestProfileInputsOverrideSavedCredentials(t *testing.T) {
	svc, _, store := fixture(t)
	svc.Source.Catalog[0].Inputs = []catalog.Input{
		{Name: "token", Type: "secret", ExclusiveGroup: "cluster_credentials"},
		{Name: "kubeconfig", Type: "file", ExclusiveGroup: "cluster_credentials"},
	}
	ref := writeProfileForTest(t, svc, "demo", "production", "[inputs]\nkubeconfig = './source.yaml'\n")
	key, err := store.ResolveProfileKey(ref.PackID, ref.CapabilityID, ref.Name)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveAnswers(key, map[string]any{"kubeconfig": "/saved/old.yaml", "token": "old-token"}); err != nil {
		t.Fatal(err)
	}
	preview, err := svc.PreviewProfile(context.Background(), ProfileRequest{Ref: ref, Inputs: map[string]any{"token": "new-token", "kubeconfig": ""}})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]any{}
	for _, input := range preview.Inputs {
		got[input.Definition.Name] = input.Value
	}
	if got["token"] != "new-token" || got["kubeconfig"] != "" {
		t.Fatalf("explicit profile inputs did not override saved answers: %#v", got)
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
	env.Kind = "opencode"
	env.ID = "opencode"
	env.ConfigPath = filepath.Join(env.Home, "manual.json")
	ref := profileRefForFixture(svc, svc.Source.ID, "demo", "prepare-choices")
	svc.Options.AgentScopes = map[string]agents.Scope{"opencode": {ID: "opencode", Home: env.Home, ConfigPathOverride: env.ConfigPath, ExplicitHome: true}}
	_, e := svc.ApplyProfile(context.Background(), ProfileRequest{Ref: ref, Interactive: true, DestinationIDs: []string{"opencode"}})
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
	env.Kind = "opencode"
	env.ID = "opencode"
	env.ConfigPath = filepath.Join(env.Home, "manual.json")
	ref := profileRefForFixture(svc, svc.Source.ID, "demo", "required-dynamic-choice")
	svc.Options.AgentScopes = map[string]agents.Scope{"opencode": {ID: "opencode", Home: env.Home, ConfigPathOverride: env.ConfigPath, ExplicitHome: true}}
	_, e := svc.ApplyProfile(context.Background(), ProfileRequest{Ref: ref, Interactive: true, DestinationIDs: []string{"opencode"}})
	if e != nil {
		t.Fatal(e)
	}
}
func TestProfileApplyDoesNotRelocatePackUntilExplicitLocate(t *testing.T) {
	svc, _, store := fixture(t)
	originalRoot := svc.Source.Root
	if err := os.WriteFile(svc.Source.ManifestPath, []byte("schema_version = 1\nsource_id = 'fixture'\ncatalog = [{ id = 'demo', source = './pkg' }]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := svc.rememberSource(); err != nil {
		t.Fatal(err)
	}
	nextRoot := t.TempDir()
	dir := filepath.Join(nextRoot, "pkg")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(svc.Source.Catalog[0].Dir, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), b, 0644); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(nextRoot, "aact.toml")
	if err := os.WriteFile(manifest, []byte("schema_version = 1\nsource_id = 'fixture'\ncatalog = [{ id = 'demo', source = './pkg' }]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	next, err := config.Discover(nextRoot, manifest, "", store.Root())
	if err != nil {
		t.Fatal(err)
	}
	copySvc := New(next, store, Options{})
	ref := profileRefForFixture(copySvc, next.ID, "demo", "copied")
	if _, err := copySvc.ApplyProfile(context.Background(), ProfileRequest{Ref: ref, ItemIDs: []string{}, DestinationIDs: []string{}}); err != nil {
		t.Fatal(err)
	}
	refs, err := copySvc.sourceRefs()
	if err != nil || len(refs) != 1 || refs[0].Root != originalRoot {
		t.Fatalf("profile apply relocated registered pack: %#v %v", refs, err)
	}
	if err := copySvc.UILocateSource(context.Background(), next.ID, nextRoot); err != nil {
		t.Fatal(err)
	}
	refs, err = copySvc.sourceRefs()
	if err != nil || len(refs) != 1 || refs[0].Root != nextRoot {
		t.Fatalf("explicit locate did not update pack path: %#v %v", refs, err)
	}
}
func TestRegistrationLedgerFailureRetainsAchievedAgentFileEffect(t *testing.T) {
	svc, env, _ := fixture(t)
	svc.Source.Catalog[0].Skill = nil
	svc.Source.Catalog[0].MCP = &catalog.MCP{Name: "demo", Transport: "streamable-http"}
	env.Kind = "opencode"
	env.ID = "opencode"
	env.ConfigPath = filepath.Join(env.Home, "manual.json")
	os.MkdirAll(env.Home, 0755)
	original := []byte("{\n // Preserve this comment\n \"servers\": {}, \"unrelated\":true\n}\n")
	os.WriteFile(env.ConfigPath, original, 0600)
	svc.Options.RecordInstallation = func(state.Installation) error { return errors.New("synthetic ledger failure") }
	_, e := svc.applyProfileFixture(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{env}, ExternalURL: "https://fixture.invalid/mcp"})
	if e == nil {
		t.Fatal("ledger failure ignored")
	}
	got, _ := os.ReadFile(env.ConfigPath)
	if bytes.Equal(got, original) || !bytes.Contains(got, []byte("fixture.invalid/mcp")) || !bytes.Contains(got, []byte("Preserve this comment")) || !bytes.Contains(got, []byte(`"unrelated":true`)) {
		t.Fatalf("achieved registration or unrelated config was lost after ledger failure: %s", got)
	}
}
func TestUnregisterLedgerFailureRestoresAgentFile(t *testing.T) {
	svc, env, _ := fixture(t)
	svc.Source.Catalog[0].Skill = nil
	svc.Source.Catalog[0].MCP = &catalog.MCP{Transport: "streamable-http"}
	env.Kind = "opencode"
	env.ID = "opencode"
	env.ConfigPath = filepath.Join(env.Home, "manual.json")
	_, e := svc.applyProfileFixture(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{env}, ExternalURL: "https://fixture.invalid/mcp"})
	if e != nil {
		t.Fatal(e)
	}
	original, _ := os.ReadFile(env.ConfigPath)
	svc.Options.RemoveInstallation = func(state.Installation) error { return errors.New("synthetic ledger failure") }
	_, e = svc.removeProfileFixture(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{env}})
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
	out, e := svc.applyProfileFixture(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{env}})
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
	_, e := svc.applyProfileFixture(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{env}})
	if e == nil || !strings.Contains(e.Error(), "--interactive") {
		t.Fatal(e)
	}
	if _, e = os.Stat(env.SkillsDir); !os.IsNotExist(e) {
		t.Fatal("wrote agent before input validation")
	}
}
func TestNoImplicitAllAgents(t *testing.T) {
	svc, _, _ := fixture(t)
	_, e := svc.applyProfileFixture(context.Background(), InstallRequest{Package: "demo"})
	if e == nil {
		t.Fatal("no agents accepted")
	}
}
func TestPartialAgentFailureRecorded(t *testing.T) {
	svc, env, s := fixture(t)
	blockedHome := t.TempDir()
	blockedSkills := filepath.Join(blockedHome, ".config", "opencode", "skills")
	if err := os.MkdirAll(filepath.Dir(blockedSkills), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blockedSkills, []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	probeHome := t.TempDir()
	svc.Options.DiscoveryProbe = &agents.DiscoveryProbe{GOOS: "linux", Home: probeHome, Getenv: func(string) string { return "" }, LookPath: func(name string) (string, error) {
		if name == "codex" || name == "opencode" {
			return filepath.Join(probeHome, "bin", name), nil
		}
		return "", os.ErrNotExist
	}}
	svc.Options.AgentScopes = map[string]agents.Scope{
		"codex":    {ID: "codex", Home: env.Home, ExplicitHome: true},
		"opencode": {ID: "opencode", Home: blockedHome, ExplicitHome: true},
	}
	ref := profileRefForFixture(svc, svc.Source.ID, "demo", "partial-agent")
	r, e := svc.ApplyProfile(context.Background(), ProfileRequest{Ref: ref, DestinationIDs: []string{"codex", "opencode"}})
	if e == nil || len(r.Errors) != 1 {
		t.Fatal(r, e)
	}
	rows, e := s.Installations()
	if e != nil || len(rows) != 1 || rows[0].AgentID != "codex" {
		t.Fatalf("successful earlier agent effect not preserved: rows=%+v err=%v result=%+v operationErr=%v", rows, e, r, e)
	}
}
func TestAnswersKeepLastValidEditAfterFailedApplyWithoutPersistingSecrets(t *testing.T) {
	svc, env, s := fixture(t)
	svc.Source.Catalog[0].Inputs = []catalog.Input{{Name: "label", Type: "string"}, {Name: "token", Type: "secret"}}
	probeHome := t.TempDir()
	svc.Options.DiscoveryProbe = &agents.DiscoveryProbe{GOOS: "linux", Home: probeHome, Getenv: func(string) string { return "" }, LookPath: func(name string) (string, error) {
		if name == "codex" || name == "opencode" {
			return filepath.Join(probeHome, "bin", name), nil
		}
		return "", os.ErrNotExist
	}}
	blockedHome := t.TempDir()
	blockedSkills := filepath.Join(blockedHome, ".config", "opencode", "skills")
	if err := os.MkdirAll(filepath.Dir(blockedSkills), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blockedSkills, []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	svc.Options.AgentScopes = map[string]agents.Scope{
		"codex":    {ID: "codex", Home: env.Home, ExplicitHome: true},
		"opencode": {ID: "opencode", Home: blockedHome, ExplicitHome: true},
	}
	ref := profileRefForFixture(svc, svc.Source.ID, "demo", "answer-retention")
	_, e := svc.ApplyProfile(context.Background(), ProfileRequest{Ref: ref, DestinationIDs: []string{"codex"}, Inputs: map[string]any{"label": "yes", "token": "never-persist"}})
	if e != nil {
		t.Fatal(e)
	}
	key, e := s.ResolveProfileKey(ref.PackID, ref.CapabilityID, ref.Name)
	a, e := s.Answers(key)
	if e != nil || a["label"] != "yes" || a["token"] != nil {
		t.Fatal(a, e)
	}
	_, e = svc.ApplyProfile(context.Background(), ProfileRequest{Ref: ref, DestinationIDs: []string{"opencode"}, Inputs: map[string]any{"label": "failed"}})
	if e == nil {
		t.Fatal("expected failure")
	}
	a, _ = s.Answers(key)
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
	_, e := svc.applyProfileFixture(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{env}, Inputs: map[string]any{"label": "world"}})
	if e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(filepath.Join(env.SkillsDir, "demo", "SKILL.md"))
	if e != nil || string(b) != "Hello world" {
		t.Fatal(string(b), e)
	}
	_, e = svc.removeProfileFixture(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{env}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = os.Lstat(filepath.Join(env.SkillsDir, "demo")); !os.IsNotExist(e) {
		t.Fatal(e)
	}
}

func TestFailedPackLocateRetainsRegisteredPath(t *testing.T) {
	svc, _, _ := fixture(t)
	if err := os.WriteFile(svc.Source.ManifestPath, []byte("schema_version = 1\nsource_id = 'fixture'\ncatalog = [{ id = 'demo', source = './pkg' }]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := svc.rememberSource(); err != nil {
		t.Fatal(err)
	}
	oldRoot := svc.Source.Root
	invalidRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(invalidRoot, "aact.toml"), []byte("schema_version = 1\nsource_id = 'fixture'\ncatalog = [{ id = 'demo', source = './missing' }]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := svc.UILocateSource(context.Background(), "fixture", invalidRoot); err == nil {
		t.Fatal("invalid pack location was accepted")
	}
	refs, err := svc.sourceRefs()
	if err != nil || len(refs) != 1 || refs[0].Root != oldRoot {
		t.Fatalf("failed locate replaced registered pack path: %#v %v", refs, err)
	}
}
func TestDomainReviewGenericHomesPreservedWithDefaultArtifact(t *testing.T) {
	svc, _, store := fixture(t)
	svc.Source.Catalog[0].Skill = nil
	svc.Source.Catalog[0].MCP = &catalog.MCP{Name: "demo", Transport: "streamable-http"}
	probeHome := t.TempDir()
	svc.Options.DiscoveryProbe = &agents.DiscoveryProbe{GOOS: "linux", Home: probeHome, Getenv: func(string) string { return "" }, LookPath: func(name string) (string, error) {
		if name == "opencode" {
			return filepath.Join(probeHome, "bin", name), nil
		}
		return "", os.ErrNotExist
	}}
	homeA, homeB := t.TempDir(), t.TempDir()
	idA, idB := "opencode:home-a", "opencode:home-b"
	svc.Options.AgentScopes = map[string]agents.Scope{
		idA: {ID: idA, Home: homeA, ConfigPathOverride: filepath.Join(homeA, "mcp.json"), ExplicitHome: true},
		idB: {ID: idB, Home: homeB, ConfigPathOverride: filepath.Join(homeB, "mcp.json"), ExplicitHome: true},
	}
	ref := profileRefForFixture(svc, svc.Source.ID, "demo", "multi-home")
	endpoint := map[string]string{"demo": "https://fixture.invalid/mcp"}
	if _, err := svc.ApplyProfile(context.Background(), ProfileRequest{Ref: ref, DestinationIDs: []string{idA, idB}, ExternalURLs: endpoint}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ApplyProfile(context.Background(), ProfileRequest{Ref: ref, DestinationIDs: []string{idB}, ExternalURLs: endpoint}); err != nil {
		t.Fatal(err)
	}
	rows, err := store.Installations()
	if err != nil || len(rows) != 1 || rows[0].AgentHome != homeB || rows[0].AgentID != idB {
		t.Fatalf("deselecting one profile home lost the other registration: %#v %v", rows, err)
	}
}
