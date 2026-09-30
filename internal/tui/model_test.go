package tui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"errors"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/mcp"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"strings"
	"testing"
)

type fixtureBackend struct{}

func (fixtureBackend) UICatalog(context.Context) ([]catalog.Package, error) {
	return []catalog.Package{{ID: "plain", Name: "Plain", Dir: "/catalog/plain", Skill: &catalog.Skill{Name: "plain"}}}, nil
}
func (fixtureBackend) UIInventory(context.Context) ([]state.Installation, error) {
	return []state.Installation{{Key: state.Key{Source: "team-source", Package: "plain", Environment: "dev", Target: "default"}, AgentID: "codex", Component: "skill", Destination: "/agent/plain"}}, nil
}
func (fixtureBackend) UIMCPs(context.Context) ([]mcp.Instance, error) {
	return []mcp.Instance{{Key: state.Key{Source: "team-source", Package: "inspect", Environment: "dev", Target: "production"}, Name: "inspect", Status: "running", URL: "http://127.0.0.1:8765/mcp"}}, nil
}
func (fixtureBackend) UIAgents(context.Context) ([]string, error) {
	return []string{"codex", "claude"}, nil
}
func (fixtureBackend) UISettings(context.Context) (map[string]string, error) {
	return map[string]string{"environment_root": "/environments"}, nil
}
func (fixtureBackend) UISourceLabels(context.Context) (map[string]string, error) {
	return map[string]string{"/catalog/plain": "team-source", "team-source": "Team checkout"}, nil
}
func (fixtureBackend) UIRun(context.Context, string, string, string, string, string, string) (string, error) {
	return "partial generator progress", errors.New("generator failed")
}
func fixtureModel(t *testing.T) *Model {
	t.Helper()
	m := New(fixtureBackend{}).(*Model)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.Update(m.Init()())
	return m
}
func press(m *Model, code rune, text string) { m.Update(tea.KeyPressMsg{Code: code, Text: text}) }
func TestMenuViewsIndependentlyReachable(t *testing.T) {
	m := fixtureModel(t)
	for _, entry := range []struct {
		key  rune
		view string
	}{{'2', "MCPs"}, {'3', "Agents"}, {'4', "Settings"}, {'1', "Catalog"}} {
		press(m, entry.key, string(entry.key))
		if m.view != entry.view || !strings.Contains(m.View().Content, entry.view) {
			t.Fatalf("view %s text %s", m.view, m.View().Content)
		}
	}
}
func TestOperationReturnsToOriginView(t *testing.T) {
	m := fixtureModel(t)
	press(m, '2', "2")
	m.busy = true
	m.Update(operationMsg{origin: "MCPs", output: "Stopped inspect"})
	if m.view != "MCPs" || m.busy || !strings.Contains(m.View().Content, "Stopped inspect") {
		t.Fatalf("%s", m.View().Content)
	}
}
func TestGeneratorFailureRetainsViewAndOutput(t *testing.T) {
	m := fixtureModel(t)
	m.busy = true
	m.Update(operationMsg{origin: "Catalog", output: "partial generator progress", err: errors.New("generator failed")})
	text := m.View().Content
	if m.view != "Catalog" || m.busy || !strings.Contains(text, "partial generator progress") || !strings.Contains(text, "generator failed") {
		t.Fatalf("%s", text)
	}
}
func TestGlobalInventoryShowsSourceLabels(t *testing.T) {
	m := fixtureModel(t)
	text := m.View().Content
	if !strings.Contains(text, "Team checkout") || !strings.Contains(text, "codex") || !strings.Contains(text, "Installed") {
		t.Fatalf("%s", text)
	}
}
func TestMCPStatusAuthLogsActions(t *testing.T) {
	m := fixtureModel(t)
	press(m, '2', "2")
	text := m.View().Content
	for _, part := range []string{"running", "127.0.0.1", "authenticate", "logs", "start", "stop"} {
		if !strings.Contains(text, part) {
			t.Fatalf("missing %s: %s", part, text)
		}
	}
}
func TestCancelledFormDoesNotInstall(t *testing.T) {
	m := fixtureModel(t)
	press(m, tea.KeyEnter, "")
	if m.form == nil {
		t.Fatal("operation context form not opened")
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if cmd != nil || m.busy || m.form != nil || m.view != "Catalog" {
		t.Fatalf("cancel changed model: %#v", m)
	}
}
func TestSettingsEnvironmentRootEditable(t *testing.T) {
	m := fixtureModel(t)
	press(m, '4', "4")
	press(m, tea.KeyEnter, "")
	if m.form == nil || !strings.Contains(m.View().Content, "Environment root") {
		t.Fatalf("%s", m.View().Content)
	}
}
