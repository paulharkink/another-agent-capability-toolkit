package app

import (
	"context"
	"errors"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/agents"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"os"
	"path/filepath"
	"testing"
)

type fixtureAdapters struct{ a *profileAdapter }

func (f fixtureAdapters) Adapter(string) (agents.Adapter, error) { return f.a, nil }
func (f fixtureAdapters) Adapters() []agents.Adapter             { return []agents.Adapter{f.a} }

type profileAdapter struct {
	calls   []string
	fail    string
	inspect func()
	home    string
}

func (a *profileAdapter) ID() string   { return "test-agent" }
func (a *profileAdapter) Name() string { return "Test agent" }
func (a *profileAdapter) Features() agents.FeatureSet {
	return agents.FeatureSet{Skills: true, MCPs: true, PluginFormats: []string{"claude-code"}}
}
func (a *profileAdapter) Detect(context.Context, agents.Scope) (agents.Detection, error) {
	return agents.Detection{Installed: true, State: "installed"}, nil
}
func (a *profileAdapter) Observe(context.Context, agents.Scope, agents.ObservationRequest) (agents.Observation, error) {
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
	return a.effect(q.Key, "skill", q.Skill.Name)
}
func (a *profileAdapter) Register(_ context.Context, _ agents.Scope, q agents.MCPRequest) (state.Installation, error) {
	return a.effect(q.Key, "mcp", q.Registration.Name)
}
func (a *profileAdapter) InstallPlugin(_ context.Context, _ agents.Scope, q agents.PluginRequest) (state.Installation, error) {
	return a.effect(q.Key, "plugin", q.Plugin.Name)
}
func (a *profileAdapter) RemoveSkill(context.Context, agents.Scope, state.Installation) error {
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
	if err != nil || len(out.Changes) != 1 || a.calls[0] != "plugin:guidance" {
		t.Fatalf("%+v %v %v", out, err, a.calls)
	}
	a.calls = nil
	q.ItemIDs = nil
	q.DestinationIDs = nil
	out, err = s.ApplyProfile(context.Background(), q)
	if err != nil || len(out.Changes) != 1 || len(a.calls) != 1 {
		t.Fatalf("saved selection: %+v %v", out, err)
	}
}
func TestCreateProfileDoesNotChangePackFiles(t *testing.T) {
	s, _, q := profileApplyFixture(t)
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
