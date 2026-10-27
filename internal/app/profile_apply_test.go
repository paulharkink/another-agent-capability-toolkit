package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/agents"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fixtureAdapters struct{ a *profileAdapter }

func (f fixtureAdapters) Adapter(string) (agents.Adapter, error) { return f.a, nil }
func (f fixtureAdapters) Adapters() []agents.Adapter             { return []agents.Adapter{f.a} }

type profileAdapter struct {
	calls          []string
	fail           string
	failRemove     bool
	failPlugin     bool
	pluginRequests []agents.PluginRequest
	inspect        func()
	home           string
	registerResult *agents.MCPRegistrationResult
	registerErr    error
	registerEffect func()
	registrations  []agents.Registration
	mcpRequests    []agents.MCPRequest
	skillRequests  []agents.SkillRequest
	observe        func(agents.ObservationRequest) agents.Observation
}

func (a *profileAdapter) ID() string   { return "test-agent" }
func (a *profileAdapter) Name() string { return "Test agent" }
func (a *profileAdapter) Features() agents.FeatureSet {
	return agents.FeatureSet{Skills: true, MCPs: true, PluginFormats: []string{"claude-code"}}
}
func (a *profileAdapter) Detect(context.Context, agents.Scope) (agents.Detection, error) {
	return agents.Detection{Installed: true, State: "installed"}, nil
}
func (a *profileAdapter) Observe(_ context.Context, _ agents.Scope, request agents.ObservationRequest) (agents.Observation, error) {
	if a.observe != nil {
		return a.observe(request), nil
	}
	return agents.Observation{}, nil
}
func (a *profileAdapter) effect(k state.Key, kind, name string) (state.Installation, error) {
	if a.inspect != nil {
		a.inspect()
	}
	a.calls = append(a.calls, kind+":"+name)
	if a.fail == kind {
		return state.Installation{}, errors.New("specific " + kind + " failure")
	}
	return state.Installation{Key: k, AgentID: a.ID(), AgentKind: a.ID(), Component: kind, Destination: filepath.Join(a.home, kind, name), RegistrationName: name}, nil
}
func (a *profileAdapter) InstallSkill(_ context.Context, _ agents.Scope, q agents.SkillRequest) (state.Installation, error) {
	a.skillRequests = append(a.skillRequests, q)
	return a.effect(q.Key, "skill", q.Skill.Name)
}
func (a *profileAdapter) Register(_ context.Context, _ agents.Scope, q agents.MCPRequest) (agents.MCPRegistrationResult, error) {
	a.registrations = append(a.registrations, q.Registration)
	a.mcpRequests = append(a.mcpRequests, q)
	if a.registerResult != nil {
		if a.registerEffect != nil {
			a.registerEffect()
		}
		return *a.registerResult, a.registerErr
	}
	row, err := a.effect(q.Key, "mcp", q.Registration.Name)
	return agents.MCPRegistrationResult{Installation: row}, err
}
func (a *profileAdapter) InstallPlugin(_ context.Context, _ agents.Scope, q agents.PluginRequest) (agents.PluginInstallResult, error) {
	a.pluginRequests = append(a.pluginRequests, q)
	market := state.Installation{Key: q.Key, AgentID: a.ID(), AgentKind: a.ID(), Component: "plugin-marketplace", ReleaseID: "aact-" + q.Key.ID()[:12] + "-" + q.Plugin.Name}
	if a.failPlugin {
		result := agents.PluginInstallResult{Effects: []state.Installation{market}}
		if q.Existing != nil {
			result.Removed = []state.Installation{*q.Existing}
		}
		return result, errors.New("specific plugin install failure")
	}
	row, err := a.effect(q.Key, "plugin", q.Plugin.Name)
	row.ReleaseID = q.Plugin.Name + "@aact-" + q.Key.ID()[:12] + "-" + q.Plugin.Name
	return agents.PluginInstallResult{Installation: row, Effects: []state.Installation{market}}, err
}
func (a *profileAdapter) RemoveSkill(context.Context, agents.Scope, state.Installation) error {
	if a.failRemove {
		return errors.New("specific removal failure")
	}
	return nil
}
func (a *profileAdapter) Unregister(context.Context, agents.Scope, state.Installation) error {
	return nil
}
func (a *profileAdapter) RemovePlugin(context.Context, agents.Scope, state.Installation) error {
	return nil
}
func profileApplyFixture(t *testing.T) (*Service, *profileAdapter, ProfileRequest) {
	t.Helper()
	s, _, _ := fixture(t)
	s.Source.ProfileRoot = filepath.Join(t.TempDir(), "profiles")
	dir := filepath.Join(s.Source.ProfileRoot, "demo")
	os.MkdirAll(dir, 0700)
	os.WriteFile(filepath.Join(dir, "ota.toml"), []byte("[inputs]\nlabel='company'\nenabled=false\n[aact.input_policy]\nlabel='fixed'\n"), 0600)
	p := &s.Source.Catalog[0]
	p.Skill = nil
	p.Skills = []catalog.Skill{{Name: "one", Source: p.Dir}, {Name: "two", Source: p.Dir}, {Name: "optional", Source: p.Dir}}
	p.MCPs = []catalog.MCP{{Name: "alpha", Image: "fixture", Transport: "streamable-http"}, {Name: "beta", Image: "fixture", Transport: "streamable-http"}}
	p.Sets = []catalog.ComponentSet{{Name: "core", Skills: []string{"one", "two"}, MCPs: []string{"alpha", "beta"}}}
	p.Inputs = []catalog.Input{{Name: "label", Type: "string", Regex: "^[a-z]+$"}, {Name: "enabled", Type: "boolean", Required: true}}
	a := &profileAdapter{home: t.TempDir()}
	s.Options.Adapters = fixtureAdapters{a}
	s.Options.Runtime = &fakeRuntime{}
	return s, a, ProfileRequest{Ref: config.ProfileRef{PackID: s.Source.ID, CapabilityID: "demo", Name: "ota"}, DestinationIDs: []string{a.ID()}, ItemIDs: []string{"set:core"}, Inputs: map[string]any{"label": "invalid123"}}
}
func TestApplyProfileReportsRemovedMCPAfterFailedReplacementAndRetries(t *testing.T) {
	s, a, q := profileApplyFixture(t)
	q.Inputs = nil
	s.Source.Catalog[0].Sets = []catalog.ComponentSet{{Name: "alpha-only", MCPs: []string{"alpha"}}}
	q.ItemIDs = []string{"set:alpha-only"}
	q.ExternalURLs = map[string]string{"alpha": "http://replacement.example/mcp"}
	key, err := s.Store.ResolveProfileKey(q.Ref.PackID, q.Ref.CapabilityID, q.Ref.Name)
	if err != nil {
		t.Fatal(err)
	}
	child := mcpDefinitionKey(key, s.Source.Catalog[0], s.Source.Catalog[0].MCPs[0])
	old := state.Installation{Key: child, AgentID: a.ID(), AgentKind: a.ID(), Component: "mcp", Destination: filepath.Join(a.home, "codex", "config.toml"), RegistrationName: registrationName(child), URL: "http://original.example/mcp", Mode: "registration"}
	if err := s.Store.Record(old); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(old.Destination), 0700); err != nil {
		t.Fatal(err)
	}
	registration := registrationName(child)
	unrelated := []byte("model = 'untouched'\n\n[mcp_servers." + registration + "]\nurl = 'http://original.example/mcp'\n\n[mcp_servers.unrelated]\nurl = 'http://unrelated.example/mcp'\n")
	if err := os.WriteFile(old.Destination, unrelated, 0600); err != nil {
		t.Fatal(err)
	}
	a.observe = func(agents.ObservationRequest) agents.Observation {
		contents, _ := os.ReadFile(old.Destination)
		status := "absent"
		if strings.Contains(string(contents), "[mcp_servers."+registration+"]") {
			status = "installed"
		}
		return agents.Observation{Components: []agents.ComponentObservation{{Kind: "mcp", Name: registration, RegistrationName: registration, Status: status}}}
	}
	a.registerEffect = func() {
		_ = os.WriteFile(old.Destination, []byte("model = 'untouched'\n\n[mcp_servers.unrelated]\nurl = 'http://unrelated.example/mcp'\n"), 0600)
	}
	a.registerResult = &agents.MCPRegistrationResult{Removed: []state.Installation{old}}
	a.registerErr = errors.New("fixture codex add failed: command diagnostic: fixture stderr")
	out, err := s.ApplyProfile(context.Background(), q)
	if err == nil || !strings.Contains(err.Error(), "fixture stderr") {
		t.Fatalf("exact CLI error missing: %+v %v", out, err)
	}
	if len(out.Changes) != 1 || out.Changes[0] != old {
		t.Fatalf("achieved removal not reported: %+v", out.Changes)
	}
	rows, err := s.Store.Installations()
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Component == "mcp" {
			t.Fatalf("stale MCP row remained after removal: %+v", rows)
		}
	}
	got, err := os.ReadFile(old.Destination)
	if err != nil || !strings.Contains(string(got), "untouched") || !strings.Contains(string(got), "unrelated") || strings.Contains(string(got), "[mcp_servers."+registration+"]") {
		t.Fatalf("unrelated config changed or removed MCP remains: %q err=%v", got, err)
	}
	preview, err := s.PreviewProfile(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	for _, destination := range preview.Destinations {
		if destination.ID == a.ID() && destination.Selected {
			t.Fatalf("removed MCP remained checked: %+v", destination)
		}
	}

	newRow := old
	newRow.URL = q.ExternalURLs["alpha"]
	a.registerEffect = func() {
		_ = os.WriteFile(old.Destination, []byte("model = 'untouched'\n\n[mcp_servers."+registration+"]\nurl = '"+newRow.URL+"'\n\n[mcp_servers.unrelated]\nurl = 'http://unrelated.example/mcp'\n"), 0600)
	}
	a.registerResult = &agents.MCPRegistrationResult{Installation: newRow}
	a.registerErr = nil
	out, err = s.ApplyProfile(context.Background(), q)
	if err != nil || len(out.Changes) != 1 || out.Changes[0].URL != newRow.URL || !out.Changes[0].ExternalRegistration {
		t.Fatalf("retry did not report replacement: %+v %v", out, err)
	}
	rows, err = s.Store.Installations()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].URL != newRow.URL {
		t.Fatalf("successful retry state = %+v", rows)
	}
	preview, err = s.PreviewProfile(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	selected := false
	for _, destination := range preview.Destinations {
		if destination.ID == a.ID() {
			selected = destination.Selected
		}
	}
	if !selected {
		t.Fatalf("successful replacement remained unchecked: %+v", preview.Destinations)
	}
	got, err = os.ReadFile(old.Destination)
	if err != nil || !strings.Contains(string(got), "unrelated") || !strings.Contains(string(got), "untouched") {
		t.Fatalf("retry damaged unrelated config: %q err=%v", got, err)
	}
}

func TestApplyProfileWholeLinkedCapability(t *testing.T) {
	s, a, q := profileApplyFixture(t)
	out, err := s.ApplyProfile(context.Background(), q)
	if err != nil || len(out.Changes) != 4 || len(a.calls) != 4 {
		t.Fatalf("%+v %v calls=%v", out, err, a.calls)
	}
	k, _ := s.Store.ResolveProfileKey(q.Ref.PackID, q.Ref.CapabilityID, q.Ref.Name)
	v, _ := s.Store.Answers(k)
	if v["label"] != "company" || v["enabled"] != false {
		t.Fatalf("policy: %v", v)
	}
}
func TestApplyProfileValidationBeforeAnyEffects(t *testing.T) {
	s, a, q := profileApplyFixture(t)
	q.Inputs = map[string]any{"unknown": "bad"}
	_, err := s.ApplyProfile(context.Background(), q)
	if err == nil || len(a.calls) != 0 {
		t.Fatalf("%v %v", err, a.calls)
	}
	q.Inputs = nil
	s.Source.Catalog[0].Inputs = append(s.Source.Catalog[0].Inputs, catalog.Input{Name: "address", Type: "string", Regex: "^ok$"})
	q.Inputs = map[string]any{"address": "bad"}
	_, err = s.ApplyProfile(context.Background(), q)
	if err == nil || len(a.calls) != 0 || s.Options.Runtime.(*fakeRuntime).starts != 0 {
		t.Fatalf("effects before regex rejection: %v", err)
	}
}
func TestApplyProfileFailureKeepsRealEffects(t *testing.T) {
	s, a, q := profileApplyFixture(t)
	a.fail = "mcp"
	out, err := s.ApplyProfile(context.Background(), q)
	rows, _ := s.Store.Installations()
	if err == nil || out.Step != "register" || len(rows) != 2 || len(out.Changes) != 2 || !out.Saved {
		t.Fatalf("out=%+v rows=%v err=%v", out, rows, err)
	}
}
func TestComponentSelectionEmptyAndUnknown(t *testing.T) {
	s, a, q := profileApplyFixture(t)
	q.ItemIDs = []string{}
	out, err := s.ApplyProfile(context.Background(), q)
	if err != nil || len(out.Changes) != 0 || len(a.calls) != 0 {
		t.Fatalf("empty selection %v %v", out, err)
	}
	q.ItemIDs = []string{"skill:one"}
	_, err = s.ApplyProfile(context.Background(), q)
	if err == nil {
		t.Fatal("linked member selectable independently")
	}
}
func TestApplyProfileExternalEndpointSkipsRuntime(t *testing.T) {
	s, _, q := profileApplyFixture(t)
	q.ExternalURLs = map[string]string{"alpha": "http://localhost:9001/mcp", "beta": "http://localhost:9002/mcp"}
	out, err := s.ApplyProfile(context.Background(), q)
	if err != nil || len(out.Changes) != 4 || s.Options.Runtime.(*fakeRuntime).starts != 0 {
		t.Fatalf("%+v %v", out, err)
	}
}
func TestApplyProfilePluginExplicitAndSavedSelection(t *testing.T) {
	s, a, q := profileApplyFixture(t)
	dir := filepath.Join(t.TempDir(), "plugin")
	os.MkdirAll(filepath.Join(dir, ".claude-plugin"), 0700)
	os.WriteFile(filepath.Join(dir, ".claude-plugin", "plugin.json"), []byte(`{"name":"guidance"}`), 0600)
	s.Source.Catalog[0].Plugins = []catalog.Plugin{{Name: "guidance", Format: "claude-code", Source: dir}}
	s.Source.Catalog[0].Dir = filepath.Dir(dir)
	q.ItemIDs = []string{"plugin:guidance"}
	out, err := s.ApplyProfile(context.Background(), q)
	if err != nil || len(out.Changes) != 2 || a.calls[0] != "plugin:guidance" {
		t.Fatalf("%+v %v %v", out, err, a.calls)
	}
	a.calls = nil
	q.ItemIDs = nil
	q.DestinationIDs = nil
	out, err = s.ApplyProfile(context.Background(), q)
	if err != nil || len(out.Changes) != 2 || len(a.calls) != 1 {
		t.Fatalf("saved selection: %+v %v", out, err)
	}
}

func TestApplyProfilePersistsPartialMarketplaceEffectAndRetries(t *testing.T) {
	s, a, q := profileApplyFixture(t)
	dir := filepath.Join(t.TempDir(), "plugin")
	if err := os.MkdirAll(filepath.Join(dir, ".claude-plugin"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".claude-plugin", "plugin.json"), []byte(`{"name":"guidance"}`), 0600); err != nil {
		t.Fatal(err)
	}
	s.Source.Catalog[0].Plugins = []catalog.Plugin{{Name: "guidance", Format: "claude-code", Source: dir}}
	s.Source.Catalog[0].Dir = filepath.Dir(dir)
	q.ItemIDs = []string{"plugin:guidance"}
	a.failPlugin = true
	out, err := s.ApplyProfile(context.Background(), q)
	if err == nil || len(out.Changes) != 1 || out.Changes[0].Component != "plugin-marketplace" {
		t.Fatalf("partial result: %+v %v", out, err)
	}
	rows, _ := s.Store.Installations()
	if len(rows) != 1 || rows[0].Component != "plugin-marketplace" {
		t.Fatalf("partial effect ledger: %+v", rows)
	}
	a.failPlugin = false
	a.calls = nil
	out, err = s.ApplyProfile(context.Background(), q)
	if err != nil || len(out.Changes) != 2 || len(a.pluginRequests) != 2 || !a.pluginRequests[1].MarketplaceConfigured {
		t.Fatalf("retry did not reconcile recorded marketplace: %+v %v requests=%+v", out, err, a.pluginRequests)
	}
	for _, row := range out.Changes {
		if row.Component == "plugin" && row.Destination == "" {
			t.Fatalf("plugin installation result missing destination: %+v", row)
		}
	}
	a.failPlugin = true
	out, err = s.ApplyProfile(context.Background(), q)
	if err == nil || len(out.Changes) != 2 || out.Changes[0].Component != "plugin" || out.Changes[1].Component != "plugin-marketplace" {
		t.Fatalf("failed repeat update did not report removal and marketplace: %+v %v", out, err)
	}
	rows, _ = s.Store.Installations()
	if len(rows) != 1 || rows[0].Component != "plugin-marketplace" {
		t.Fatalf("failed repeat update left stale installed plugin state: %+v", rows)
	}
}
func TestCreateProfileDoesNotChangePackFiles(t *testing.T) {
	s, _, q := profileApplyFixture(t)
	q.Inputs = map[string]any{"label": "valid", "enabled": false}
	q.Ref.Name = "extra"
	if err := s.CreateProfile(context.Background(), q.Ref); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.Source.ProfileRoot, "demo", "extra.toml")); !os.IsNotExist(err) {
		t.Fatal("wrote pack profile")
	}
	preview, err := s.PreviewProfile(context.Background(), q)
	if err != nil || preview.ProfileOrigin != "local" {
		t.Fatalf("%+v %v", preview, err)
	}
	if err := s.CreateProfile(context.Background(), q.Ref); err == nil {
		t.Fatal("duplicate accepted")
	}
}
func TestComponentSelectionDoesNotEnableOptionalMCPOrPlugin(t *testing.T) {
	p := catalog.Package{Skills: []catalog.Skill{{Name: "guide"}}, MCPs: []catalog.MCP{{Name: "optional", EnabledInput: "enabled"}}, Plugins: []catalog.Plugin{{Name: "native", Format: "claude-code"}}, Sets: []catalog.ComponentSet{{Name: "linked", Skills: []string{"guide"}, MCPs: []string{"optional"}}}}
	values := map[string]any{"enabled": false}
	items, _, err := componentSelection(p, values, nil, false)
	if err != nil || len(items) != 1 || len(items[0].MCPs) != 0 || len(items[0].Plugins) != 0 || values["enabled"] != false {
		t.Fatalf("%v %v", items, err)
	}
}
func TestApplyProfileRecordFailureReportsAchievedEffect(t *testing.T) {
	s, _, q := profileApplyFixture(t)
	s.Options.RecordInstallation = func(state.Installation) error { return errors.New("state disk full") }
	out, err := s.ApplyProfile(context.Background(), q)
	rows, _ := s.Store.Installations()
	if err == nil || out.Step != "record" || len(out.Changes) != 1 || len(rows) != 0 {
		t.Fatalf("%+v rows=%v err=%v", out, rows, err)
	}
}

func TestApplyContinuesAfterFalseContainerHealthAndReportsLoginURL(t *testing.T) {
	s, a, q := profileApplyFixture(t)
	p := &s.Source.Catalog[0]
	p.Skills, p.Skill, p.MCP = nil, nil, nil
	p.MCPs = []catalog.MCP{{Name: "alpha", Image: "fixture", Transport: "streamable-http", HealthPath: "/health", Actions: map[string]catalog.Command{
		"authenticate": {Argv: []string{"auth", "authenticate"}, Target: "container"},
	}}}
	p.Sets = []catalog.ComponentSet{{Name: "alpha-only", MCPs: []string{"alpha"}}}
	q.ItemIDs = []string{"set:alpha-only"}
	healthChecks := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/health":
			healthChecks++
			if healthChecks == 1 {
				_, _ = fmt.Fprint(w, `{"authenticated":false}`)
				return
			}
			_, _ = fmt.Fprint(w, `{"authenticated":true}`)
		case r.Method == http.MethodPost && r.URL.Path == "/mcp":
			_, _ = fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05"}}`)
		default:
			t.Errorf("unexpected MCP health request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	runtime := &authenticationTestRuntime{providerTestRuntime: providerTestRuntime{url: server.URL + "/mcp"}, results: []string{`{"login_url":"https://login.example/device"}`}}
	s.Options.Runtime = runtime
	a.inspect = func() {
		if healthChecks != 1 {
			t.Fatalf("MCP registration started after %d health checks, want one false-health observation", healthChecks)
		}
	}
	var progress []viewmodel.OperationProgress
	ctx := viewmodel.WithOperationProgress(context.Background(), func(event viewmodel.OperationProgress) { progress = append(progress, event) })
	result, err := s.ApplyProfile(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Changes) == 0 {
		t.Fatalf("false health prevented registration changes: %#v", result)
	}
	if result.AuthenticationStatus != viewmodel.AuthenticationFailed {
		t.Fatalf("authentication status = %q, want AuthenticationFailed", result.AuthenticationStatus)
	}
	if len(runtime.keys) != 1 {
		t.Fatalf("runtime starts = %d", len(runtime.keys))
	}
	if len(runtime.actions) != 1 || runtime.actions[0] != "authenticate" {
		t.Fatalf("actions = %#v", runtime.actions)
	}
	if len(runtime.stateDirs) != 1 || runtime.stateDirs[0] != "/state" {
		t.Fatalf("container authentication state_dir = %#v", runtime.stateDirs)
	}
	if healthChecks != 1 {
		t.Fatalf("health checks = %d, want one false-health observation", healthChecks)
	}
	foundRegistration := false
	for _, call := range a.calls {
		if strings.HasPrefix(call, "mcp:") {
			foundRegistration = true
		}
	}
	if !foundRegistration {
		t.Fatalf("registration did not happen after confirmed auth: %#v", a.calls)
	}
	if len(progress) == 0 || !strings.Contains(fmt.Sprint(progress), "https://login.example/device") {
		t.Fatalf("login URL was not reported through progress: %#v", progress)
	}
}

func TestReconfiguredProfileAuthFailurePreservesSavedStateAndReportsAuthRequired(t *testing.T) {
	s, a, q := profileApplyFixture(t)
	p := &s.Source.Catalog[0]
	p.Skills, p.Skill, p.MCP = nil, nil, nil
	p.Inputs = append(p.Inputs, catalog.Input{Name: "auth_mode", Type: "string"})
	p.MCPs = []catalog.MCP{{Name: "alpha", Image: "fixture:v2", Transport: "streamable-http", HealthPath: "/health", Actions: map[string]catalog.Command{
		"authenticate": {Argv: []string{"auth", "authenticate"}, Target: "container"},
	}}}
	p.Sets = []catalog.ComponentSet{{Name: "alpha-only", MCPs: []string{"alpha"}}}
	q.ItemIDs = []string{"set:alpha-only"}
	q.Inputs = map[string]any{"enabled": false, "auth_mode": "api_token"}
	key, err := s.Store.ResolveProfileKey(q.Ref.PackID, q.Ref.CapabilityID, q.Ref.Name)
	if err != nil {
		t.Fatal(err)
	}
	child := mcpDefinitionKey(key, *p, p.MCPs[0])
	oldRegistration := state.Installation{Key: child, AgentID: a.ID(), AgentKind: a.ID(), Component: "mcp", Destination: filepath.Join(a.home, "mcp", "alpha"), RegistrationName: "alpha", URL: "http://old.example/mcp"}
	if err := s.Store.Record(oldRegistration); err != nil {
		t.Fatal(err)
	}
	stateDir, err := s.Store.EnsureAuthDir(child)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "saved-session"), []byte("prior-session"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.SaveAnswers(key, map[string]any{"enabled": true, "auth_mode": "sso"}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) }))
	defer server.Close()
	runtime := &authenticationTestRuntime{providerTestRuntime: providerTestRuntime{url: server.URL + "/mcp"}, results: []string{`{"login_url":"https://login.example/device"}`}}
	s.Options.Runtime = runtime
	result, err := s.ApplyProfile(context.Background(), q)
	if err == nil || !strings.Contains(err.Error(), "HTTP 401") {
		t.Fatalf("unexpected health result=%#v err=%v", result, err)
	}
	if len(runtime.specs) != 1 || runtime.specs[0].Image != "fixture:v2" {
		t.Fatalf("reconfigured runtime not retained: %#v", runtime.specs)
	}
	if len(runtime.requests) != 1 || runtime.requests[0].Inputs["auth_mode"] != "api_token" {
		t.Fatalf("authentication did not use the currently selected mode: %#v", runtime.requests)
	}
	if len(a.calls) != 0 {
		t.Fatalf("registration occurred after auth failure: %#v", a.calls)
	}
	answers, err := s.Store.Answers(key)
	if err != nil || answers["enabled"] != true || answers["auth_mode"] != "sso" {
		t.Fatalf("saved state changed: %#v %v", answers, err)
	}
	rows, err := s.Store.Installations()
	if err != nil {
		t.Fatal(err)
	}
	foundRegistration := false
	for _, row := range rows {
		if row.Key == child && row.Component == "mcp" && row.RegistrationName == "alpha" {
			foundRegistration = true
		}
	}
	if !foundRegistration {
		t.Fatalf("existing registration was lost: %#v", rows)
	}
	if data, err := os.ReadFile(filepath.Join(stateDir, "saved-session")); err != nil || string(data) != "prior-session" {
		t.Fatalf("saved auth state changed: %q %v", data, err)
	}
}

func TestResetInstanceToPresetClearsExactLocalStateWithoutStoppingUnknownRuntime(t *testing.T) {
	s, adapter, request := profileApplyFixture(t)
	ref := config.InstanceRef{PackID: request.Ref.PackID, CapabilityID: request.Ref.CapabilityID, Name: request.Ref.Name}
	preset, _, key, err := s.loadInstance(ref)
	_ = preset
	if err != nil {
		t.Fatal(err)
	}
	instance, err := config.LoadInstance(s.Source.CapabilityPack(), ref.CapabilityID, ref.Name)
	if err != nil {
		t.Fatal(err)
	}
	presetBytes, err := os.ReadFile(instance.Path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Store.RecordInstance(state.InstanceRecord{Key: key, Name: ref.Name, Local: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.SaveAnswers(key, map[string]any{"token": "selected-secret"}); err != nil {
		t.Fatal(err)
	}
	authDir, err := s.Store.EnsureAuthDir(key)
	if err != nil {
		t.Fatal(err)
	}
	authFile := filepath.Join(authDir, "token")
	if err := os.WriteFile(authFile, []byte("selected-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	child := mcpDefinitionKey(key, s.Source.Catalog[0], s.Source.Catalog[0].MCPs[0])
	rows := []state.Installation{
		{Key: key, AgentID: adapter.ID(), AgentKind: adapter.ID(), Component: "skill", Destination: filepath.Join(adapter.home, "skills", "one")},
		{Key: child, AgentID: adapter.ID(), AgentKind: adapter.ID(), Component: "mcp", Destination: filepath.Join(adapter.home, "config.toml"), RegistrationName: "managed"},
		{Key: child, AgentID: adapter.ID(), AgentKind: adapter.ID(), Component: "runtime", RegistrationName: "unknown-runtime", ExternalRegistration: true},
		{Key: child, AgentID: adapter.ID(), AgentKind: adapter.ID(), Component: "mcp", Destination: filepath.Join(adapter.home, "config.toml"), RegistrationName: "external", ExternalRegistration: true},
	}
	siblingKey, err := s.Store.ResolveInstanceKey(ref.PackID, ref.CapabilityID, "sibling")
	if err != nil {
		t.Fatal(err)
	}
	rows = append(rows, state.Installation{Key: siblingKey, AgentID: adapter.ID(), AgentKind: adapter.ID(), Component: "skill", Destination: filepath.Join(adapter.home, "skills", "sibling")})
	for _, row := range rows {
		if err := s.Store.Record(row); err != nil {
			t.Fatal(err)
		}
	}
	runtime := &changedRuntime{}
	s.Options.Runtime = runtime

	result, err := s.ResetInstanceToPreset(context.Background(), InstanceRequest{Ref: ref})
	if err != nil {
		t.Fatalf("reset to preset failed: %+v %v", result, err)
	}
	if !strings.Contains(result.Message, "runtime may remain") {
		t.Fatalf("reset did not explain unknown runtime recovery: %+v", result)
	}
	if runtime.stops != 0 || runtime.starts != 0 {
		t.Fatalf("reset touched an ambiguously owned runtime: starts=%d stops=%d", runtime.starts, runtime.stops)
	}
	afterRows, err := s.Store.Installations()
	if err != nil {
		t.Fatal(err)
	}
	if len(afterRows) != 2 || afterRows[0].RegistrationName != "external" || afterRows[1].Key != siblingKey {
		t.Fatalf("reset removed unrelated or externally owned rows, or retained managed/runtime rows: %+v", afterRows)
	}
	if _, err := os.Stat(authFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("exact instance credentials survived reset: %v", err)
	}
	hasAnswers, err := s.Store.HasAnswers(key)
	if err != nil || hasAnswers {
		t.Fatalf("exact instance answers survived reset: has=%t err=%v", hasAnswers, err)
	}
	records, err := s.Store.Instances()
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if record.Key == key {
			t.Fatalf("exact instance ledger survived reset: %+v", record)
		}
	}
	afterPreset, err := os.ReadFile(instance.Path)
	if err != nil || !bytes.Equal(afterPreset, presetBytes) {
		t.Fatalf("pack preset TOML changed during reset: err=%v", err)
	}
}

func TestRemoveInstanceDeletesOnlySelectedInstanceStateAfterCleanup(t *testing.T) {
	s, _, request := profileApplyFixture(t)
	_, selectedConfig, _, err := s.loadInstance(request.Ref)
	if err != nil {
		t.Fatal(err)
	}
	sibling := config.InstanceRef{PackID: request.Ref.PackID, CapabilityID: request.Ref.CapabilityID, Name: "sibling"}
	if err := s.CreateInstance(context.Background(), sibling); err != nil {
		t.Fatal(err)
	}
	siblingConfig := filepath.Join(s.Source.ProfileRoot, request.Ref.CapabilityID, "sibling.toml")
	if err := os.WriteFile(siblingConfig, []byte("[inputs]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	key, err := s.Store.ResolveInstanceKey(request.Ref.PackID, request.Ref.CapabilityID, request.Ref.Name)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Store.RecordInstance(state.InstanceRecord{Key: key, Name: request.Ref.Name, Local: true}); err != nil {
		t.Fatal(err)
	}
	siblingKey, err := s.Store.ResolveInstanceKey(sibling.PackID, sibling.CapabilityID, sibling.Name)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Store.SaveAnswers(key, map[string]any{"token": "selected-secret"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.SaveAnswers(siblingKey, map[string]any{"token": "sibling-secret"}); err != nil {
		t.Fatal(err)
	}
	authDir, err := s.Store.EnsureAuthDir(key)
	if err != nil {
		t.Fatal(err)
	}
	authFile := filepath.Join(authDir, "token")
	if err := os.WriteFile(authFile, []byte("selected-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RemoveInstance(context.Background(), InstanceRequest{Ref: request.Ref}); err != nil {
		t.Fatal(err)
	}
	records, err := s.Store.Instances()
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if record.Key == key {
			t.Fatalf("deleted instance record remains: %+v", records)
		}
	}
	foundSibling := false
	for _, record := range records {
		if record.Key == siblingKey {
			foundSibling = true
		}
	}
	if !foundSibling {
		t.Fatalf("sibling instance was deleted: %+v", records)
	}
	if has, err := s.Store.HasAnswers(key); err != nil || has {
		t.Fatalf("selected answers remain: has=%t err=%v", has, err)
	}
	if has, err := s.Store.HasAnswers(siblingKey); err != nil || !has {
		t.Fatalf("sibling answers changed: has=%t err=%v", has, err)
	}
	if _, err := os.Stat(authFile); !os.IsNotExist(err) {
		t.Fatalf("selected auth file remains: err=%v", err)
	}
	if _, err := os.Stat(selectedConfig.Path); !os.IsNotExist(err) {
		t.Fatalf("selected instance config remains: err=%v", err)
	}
	if _, err := os.Stat(siblingConfig); err != nil {
		t.Fatalf("sibling instance config was removed: %v", err)
	}
	if _, err := os.Stat(selectedConfig.Path); !os.IsNotExist(err) {
		t.Fatalf("selected instance config remains: err=%v", err)
	}
	if _, err := os.Stat(siblingConfig); err != nil {
		t.Fatalf("sibling instance config was removed: %v", err)
	}
}

func TestRemoveInstanceConfigurationRejectsSymlinkEscape(t *testing.T) {
	s, _, _ := profileApplyFixture(t)
	root := t.TempDir()
	instances := filepath.Join(root, "instances")
	pack := filepath.Join(root, "pack")
	if err := os.MkdirAll(instances, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(pack, 0700); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(pack, "ota.toml")
	if err := os.WriteFile(manifest, []byte("[inputs]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(pack, filepath.Join(instances, "escape")); err != nil {
		t.Fatal(err)
	}
	s.Source.InstanceRoot = instances
	err := s.removeInstanceConfiguration(config.InstanceRef{Name: "ota"}, filepath.Join(instances, "escape", "ota.toml"))
	if err == nil {
		t.Fatal("expected symlinked instance directory to be rejected")
	}
	if _, err := os.Stat(manifest); err != nil {
		t.Fatalf("outside pack file was removed: %v", err)
	}
}

func TestRemoveInstanceDeletesOnlyLocallyCreatedInstanceTOML(t *testing.T) {
	s, _, request := profileApplyFixture(t)
	root := t.TempDir()
	s.Source.InstanceRoot = root
	s.Source.ProfileRoot = root
	ref := config.InstanceRef{PackID: s.Source.ID, CapabilityID: request.Ref.CapabilityID, Name: "local"}
	instanceDir := filepath.Join(root, ref.CapabilityID)
	if err := os.MkdirAll(instanceDir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(instanceDir, ref.Name+".toml")
	if err := os.WriteFile(path, []byte("[inputs]\nlabel = 'company'\nenabled = true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	key, err := s.Store.ResolveInstanceKey(ref.PackID, ref.CapabilityID, ref.Name)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Store.RecordInstance(state.InstanceRecord{Key: key, Name: ref.Name, Local: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RemoveInstance(context.Background(), InstanceRequest{Ref: ref}); err != nil {
		t.Fatalf("remove local instance: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("locally created instance TOML still exists: %v", err)
	}
}

func TestRemovePackProvidedInstanceKeepsPresetTOML(t *testing.T) {
	s, _, request := profileApplyFixture(t)
	_, preset, _, err := s.loadInstance(request.Ref)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(preset.Path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RemoveInstance(context.Background(), InstanceRequest{Ref: request.Ref}); err != nil {
		t.Fatalf("remove pack-provided instance state: %v", err)
	}
	got, err := os.ReadFile(preset.Path)
	if err != nil {
		t.Fatalf("pack-provided preset was removed instead of restored: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("pack-provided preset changed during remove/reset:\nwant %q\ngot  %q", want, got)
	}
}

func TestRemoveInstanceFailurePreservesRecoverableState(t *testing.T) {
	s, adapter, request := profileApplyFixture(t)
	_, selectedConfig, _, err := s.loadInstance(request.Ref)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(selectedConfig.Path); err != nil {
		t.Fatalf("selected config missing before delete: %v", err)
	}
	siblingConfig := filepath.Join(s.Source.ProfileRoot, request.Ref.CapabilityID, "sibling.toml")
	if err := os.WriteFile(siblingConfig, []byte("[inputs]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	key, err := s.Store.ResolveInstanceKey(request.Ref.PackID, request.Ref.CapabilityID, request.Ref.Name)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Store.RecordInstance(state.InstanceRecord{Key: key, Name: request.Ref.Name, Local: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.SaveAnswers(key, map[string]any{"token": "preserve-me"}); err != nil {
		t.Fatal(err)
	}
	authDir, err := s.Store.EnsureAuthDir(key)
	if err != nil {
		t.Fatal(err)
	}
	authFile := filepath.Join(authDir, "token")
	if err := os.WriteFile(authFile, []byte("credential"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.Record(state.Installation{Key: key, AgentID: adapter.ID(), AgentKind: adapter.ID(), Component: "skill", Destination: "fixture"}); err != nil {
		t.Fatal(err)
	}
	adapter.failRemove = true
	if _, err := s.RemoveInstance(context.Background(), InstanceRequest{Ref: request.Ref}); err == nil {
		t.Fatal("expected binding cleanup failure")
	}
	records, err := s.Store.Instances()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, record := range records {
		if record.Key == key {
			found = true
		}
	}
	if !found {
		t.Fatal("instance record was deleted after cleanup failed")
	}
	answers, err := s.Store.Answers(key)
	if err != nil || answers["token"] != "preserve-me" {
		t.Fatalf("answers were not preserved: %#v %v", answers, err)
	}
	if _, err := os.Stat(authFile); err != nil {
		t.Fatalf("credentials were not preserved: %v", err)
	}
	if _, err := os.Stat(selectedConfig.Path); err != nil {
		t.Fatalf("instance config was not preserved: %v", err)
	}
}
