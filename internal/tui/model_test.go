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
func TestOperationReturnsToOriginView(t *testing.T) {
	m := fixtureModel(t)
	focusFixtureProfile(m)
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
	if !strings.Contains(text, "Team checkout") || !strings.Contains(text, "Plain") {
		t.Fatalf("%s", text)
	}
}
func TestMCPStatusAuthLogsActions(t *testing.T) {
	m := fixtureModel(t)
	focusFixtureProfile(m)
	m.focusPane(CapabilitiesPane)
	press(m, tea.KeyEnter, "")
	m.selectContext(m.home.Profiles.Index + 2)
	press(m, tea.KeyEnter, "")
	text := m.View().Content
	for _, part := range []string{"Restart", "Stop", "Authenticate", "View logs"} {
		if !strings.Contains(text, part) {
			t.Fatalf("missing %s: %s", part, text)
		}
	}
}
func TestCancelledFormDoesNotInstall(t *testing.T) {
	m := fixtureModel(t)
	press(m, tea.KeyEnter, "")
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
	m.navigate("Settings")
	press(m, tea.KeyEnter, "")
	if m.form == nil || !strings.Contains(m.View().Content, "Environment root") {
		t.Fatalf("%s", m.View().Content)
	}
}

func TestCatalogContextSupportsDefaultMultipleAgents(t *testing.T) {
	m := fixtureModel(t)
	m.catalog[0].MCP = &catalog.MCP{}
	m.settings["default_agents"] = "claude,codex"
	press(m, tea.KeyEnter, "")
	press(m, tea.KeyEnter, "")
	if !strings.Contains(m.View().Content, "[claude codex]") {
		t.Fatalf("multiple agent prefill missing: %s", m.View().Content)
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if cmd == nil || !m.busy || m.pending.agent != "claude,codex" {
		t.Fatalf("operation lacks agents: %#v", m.pending)
	}
}

func TestSkillOnlyContextDefaultsToAllDespiteNamedMCPDefaults(t *testing.T) {
	m := fixtureModel(t)
	m.settings["default_agents"] = "claude"
	press(m, tea.KeyEnter, "")
	press(m, tea.KeyEnter, "")
	if m.form == nil {
		t.Fatal("skill install form missing")
	}
	if !strings.Contains(m.View().Content, "[all]") || !strings.Contains(m.View().Content, "All — ~/.agents/skills") {
		t.Fatalf("skill-only default did not select All: %s", m.View().Content)
	}
}
func TestCatalogRequiresAtLeastOneAgent(t *testing.T) {
	m := fixtureModel(t)
	press(m, tea.KeyEnter, "")
	press(m, tea.KeyEnter, "")
	press(m, tea.KeySpace, " ")
	_, cmd := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if cmd != nil || m.busy || m.form == nil {
		t.Fatal("empty agent selection accepted")
	}
}
func TestProfileShortcutCannotOpenPackageUninstall(t *testing.T) {
	for _, status := range []string{"running", "external"} {
		m := fixtureModel(t)
		m.mcps[0].Status = status
		focusFixtureProfile(m)
		_, cmd := m.Update(tea.KeyPressMsg{Code: 'u', Text: "u"})
		if cmd != nil || m.form != nil || m.busy || m.pending.action == "uninstall" {
			t.Fatalf("%s profile shortcut dispatched package uninstall", status)
		}
	}
}
func TestExternalMCPRejectsDockerActions(t *testing.T) {
	for _, stroke := range []string{"s", "x", "a", "l"} {
		m := fixtureModel(t)
		m.mcps[0].Status = "external"
		focusFixtureProfile(m)
		_, cmd := m.Update(tea.KeyPressMsg{Code: rune(stroke[0]), Text: stroke})
		if cmd != nil || m.busy {
			t.Fatalf("external action %s dispatched", stroke)
		}
	}
}

func TestCatalogMCPAuthAndStartBeforeInventory(t *testing.T) {
	for _, action := range []struct{ key, action string }{{"a", "authenticate"}, {"s", "start"}} {
		t.Run(action.action, func(t *testing.T) {
			m := fixtureModel(t)
			m.catalog[0].MCP = &catalog.MCP{}
			m.mcps = nil
			press(m, rune(action.key[0]), action.key)
			if m.form == nil || m.busy || m.pending.action != action.action {
				t.Fatalf("catalog MCP action unavailable: %#v", m.pending)
			}
			if strings.Contains(m.View().Content, "Agents *") {
				t.Fatal("runtime action requests agents")
			}
			_, cmd := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
			if cmd == nil || !m.busy || m.pending.agent != "" {
				t.Fatalf("runtime action not dispatched: %#v", m.pending)
			}
		})
	}
}
func TestCatalogMCPActionsRejectSkillOnlyPackage(t *testing.T) {
	for _, key := range []string{"a", "s"} {
		m := fixtureModel(t)
		press(m, rune(key[0]), key)
		if m.form != nil || m.busy || m.output == "" {
			t.Fatal("skill-only package did not explain missing MCP action")
		}
	}
}

func focusFixtureProfile(m *Model) {
	m.catalog[0].ID = "inspect"
	m.catalog[0].MCP = &catalog.MCP{}
	m.mcps[0].Ownership = "local"
	m.reconcileHome()
	m.focusPane(ProfilesPane)
}
