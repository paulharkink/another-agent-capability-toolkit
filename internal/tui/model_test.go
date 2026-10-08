package tui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"errors"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/mcp"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
	"strings"
	"testing"
)

type fixtureBackend struct{}

func TestEveryTopLevelViewOwnsAlternateScreenAndMouseMode(t *testing.T) {
	m, _ := homeFixture()
	view := m.View()
	if !view.AltScreen || view.MouseMode != tea.MouseModeCellMotion {
		t.Fatalf("home view did not request Bubble Tea managed terminal modes: alt=%t mouse=%v", view.AltScreen, view.MouseMode)
	}
	m.result = &resultState{Rows: []string{"done"}}
	view = m.View()
	if !view.AltScreen || view.MouseMode != tea.MouseModeCellMotion {
		t.Fatalf("result view did not request Bubble Tea managed terminal modes: alt=%t mouse=%v", view.AltScreen, view.MouseMode)
	}
}

func TestF10QuitsGlobalOverlaysAndPromptsForDirtySetup(t *testing.T) {
	t.Run("home", func(t *testing.T) {
		m := fixtureModel(t)
		_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyF10})
		if cmd == nil {
			t.Fatal("F10 did not quit from home")
		}
	})
	t.Run("result", func(t *testing.T) {
		m := fixtureModel(t)
		m.showOperationResult(operationMsg{origin: "Catalog", output: "complete"})
		_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyF10})
		if cmd == nil {
			t.Fatal("F10 did not quit from the result overlay")
		}
	})
	t.Run("dirty form", func(t *testing.T) {
		m, _ := openSetupInteraction(t)
		m.form.ApplyValues(map[string]any{"token": "changed-token"})
		_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyF10})
		if cmd != nil || m.unsavedExit == nil || m.unsavedExit.reason != "quit" {
			t.Fatalf("F10 bypassed the dirty-form guard: cmd=%v popup=%+v", cmd, m.unsavedExit)
		}
	})
}

func TestQQuitsWhenNoTextEditorOwnsTheKey(t *testing.T) {
	m := fixtureModel(t)
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	if cmd == nil {
		t.Fatal("q did not quit from a non-editing screen")
	}
}

func TestGlobalQuitDuringBusyOperationAndQDuringActiveEdit(t *testing.T) {
	t.Run("busy F10", func(t *testing.T) {
		m := fixtureModel(t)
		m.busy = true
		_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyF10})
		if cmd == nil {
			t.Fatal("F10 did not quit while an operation was active")
		}
	})
	t.Run("q stays in active form editor", func(t *testing.T) {
		m, _ := openSetupInteraction(t)
		m.form.SelectSection("Authentication")
		m.form.FocusSection()
		m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
		m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		if !m.form.IsEditingInput() {
			t.Fatal("fixture did not enter a text-edit state")
		}
		_, cmd := m.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
		if cmd != nil || !m.form.IsEditingInput() || m.unsavedExit != nil {
			t.Fatalf("q escaped the active text editor: cmd=%v editing=%t popup=%+v", cmd, m.form.IsEditingInput(), m.unsavedExit)
		}
	})
}

type modelWorkspaceBackend struct{ *setupBackendFixture }

func (b *modelWorkspaceBackend) UISetupPreview(ctx context.Context, request viewmodel.SetupRequest) (viewmodel.SetupPreview, error) {
	preview, err := b.setupBackendFixture.UISetupPreview(ctx, request)
	preview.Key = setupProfileKey(request)
	preview.PackageName = "Inspector"
	// This backend is used for observed MCP profile fixtures; keep the package
	// metadata and declared public UI sections consistent with the profile rows it returns.
	preview.MCP = request.Ref.CapabilityID == "inspect" || request.PackageID == "inspect"
	if (request.Ref.CapabilityID == "inspect" || request.PackageID == "inspect") && len(preview.Sections) > 0 {
		preview.Sections[0].Title = "Authentication"
	}
	return preview, err
}

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
	return map[string]string{"/catalog/plain": "team-source", "team-source": "Team Capability Pack"}, nil
}
func (fixtureBackend) UIRun(context.Context, string, string, string, string, string, string, string) (string, error) {
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
	m.settings["source"] = "Team"
	text := m.View().Content
	if !strings.Contains(text, "Capability Pack: Team") || !strings.Contains(text, "Plain") {
		t.Fatalf("%s", text)
	}
}

func TestHomeScrollableCapabilityPaneShowsThumbAndTrack(t *testing.T) {
	m := fixtureModel(t)
	for i := 0; i < 24; i++ {
		id := "package-" + strings.Repeat("x", i+1)
		m.catalog = append(m.catalog, catalog.Package{ID: id, Name: id, Dir: "/catalog"})
	}
	m.reconcileHome()
	view := m.View().Content
	if !strings.Contains(view, "█") || !strings.Contains(view, "│") {
		t.Fatalf("scrollable home pane has no visible scrollbar thumb/track:\n%s", view)
	}
}
func TestMCPStatusAuthLogsActions(t *testing.T) {
	m, msg := homeFixture()
	m.backend = &modelWorkspaceBackend{setupBackendFixture: &setupBackendFixture{}}
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	m.Update(msg)
	m.focusPane(ProfilesPane)
	profileIndex := -1
	for index, row := range m.contextRows() {
		if row.Kind == "profile" && row.Key.Target == "production" {
			profileIndex = index
			break
		}
	}
	if profileIndex < 0 {
		t.Fatal("profile fixture did not include the running profile")
	}
	m.selectContext(profileIndex)
	_, open := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if open == nil {
		t.Fatal("selected profile did not open its workspace")
	}
	m.Update(open())
	text := m.View().Content
	for _, part := range []string{"Endpoint: http://127.0.0.1:8765/mcp", "Authentication", "Logs", "Information"} {
		if !strings.Contains(text, part) {
			t.Fatalf("missing %s: %s", part, text)
		}
	}
	if m.workspace == nil || m.workspace.Key.Target != "production" || m.form == nil || m.form.SectionTitle() != "Overview" {
		t.Fatalf("MCP status workspace lost the selected profile: workspace=%+v form=%v", m.workspace, m.form != nil)
	}
	m.form.SelectSectionID(sectionRuntimeID)
	m.form.FocusSection()
	if !strings.Contains(m.View().Content, "status: running") {
		t.Fatalf("profile Runtime section did not preserve runtime status: %s", m.View().Content)
	}
}
func TestSettingsEnvironmentRootEditable(t *testing.T) {
	m := fixtureModel(t)
	m.navigate("Settings")
	press(m, tea.KeyEnter, "")
	press(m, tea.KeyEnd, "")
	press(m, tea.KeyEnter, "")
	if m.form == nil || !strings.Contains(m.View().Content, "Profile configuration directory") {
		t.Fatalf("%s", m.View().Content)
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

func focusFixtureProfile(m *Model) {
	m.catalog[0].ID = "inspect"
	m.catalog[0].MCP = &catalog.MCP{}
	m.mcps[0].Ownership = "local"
	m.reconcileHome()
	m.focusPane(ProfilesPane)
}
