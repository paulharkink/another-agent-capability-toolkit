package tui

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

type typedAgentBackend struct {
	fixtureBackend
	rows    []viewmodel.AgentManagementRow
	content string
}

type typedEnvironmentBackend struct {
	fixtureBackend
	snapshot viewmodel.EnvironmentSnapshot
	contents map[string]string
}

type setupEnvironmentBackend struct {
	fixtureBackend
	snapshot       viewmodel.EnvironmentSnapshot
	previewRequest viewmodel.SetupRequest
}

func (b *setupEnvironmentBackend) UIEnvironmentSnapshot(context.Context) (viewmodel.EnvironmentSnapshot, error) {
	return b.snapshot, nil
}

func (b *setupEnvironmentBackend) UIEnvironmentTarget(context.Context, string) (string, error) {
	return "", nil
}

func (b *setupEnvironmentBackend) UISetupPreview(_ context.Context, request viewmodel.SetupRequest) (viewmodel.SetupPreview, error) {
	b.previewRequest = request
	return viewmodel.SetupPreview{Key: state.Key{Source: request.SourceID, Package: request.PackageID, Environment: request.Environment, Target: request.Target}, PackageName: request.PackageID}, nil
}

func (b *setupEnvironmentBackend) UIInstall(context.Context, viewmodel.SetupInstallRequest) (viewmodel.OperationResult, error) {
	return viewmodel.OperationResult{}, nil
}

func TestEnvironmentTargetCanStartSetupForItsPackage(t *testing.T) {
	b := &setupEnvironmentBackend{snapshot: viewmodel.EnvironmentSnapshot{SourceID: "team-source", Targets: []viewmodel.EnvironmentTarget{{SourceID: "team-source", Environment: "dev", PackageID: "plain", Name: "production", Path: "/environments/dev/plain/production.toml"}}}}
	m := New(b).(*Model)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.Update(m.Init()())
	m.navigate("Environments")
	press(m, tea.KeyDown, "")
	press(m, tea.KeyRight, "")
	press(m, tea.KeyEnter, "")
	if !strings.Contains(m.View().Content, "Configure / install selected target…") || strings.Contains(m.View().Content, "Configure / install selected target… — disabled") {
		t.Fatalf("actual target cannot start setup: %s", m.View().Content)
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil || !m.busy {
		t.Fatal("selected TOML target did not request setup preview")
	}
	m.Update(cmd())
	if b.previewRequest != (viewmodel.SetupRequest{SourceID: "team-source", PackageID: "plain", Environment: "dev", Target: "production"}) || m.form == nil {
		t.Fatalf("setup used wrong target: %+v, form=%v", b.previewRequest, m.form)
	}
}

func TestEnvironmentActionNamesItsImmediateSetupEffect(t *testing.T) {
	m := fixtureModel(t)
	m.navigate("Environments")
	press(m, tea.KeyEnter, "")
	view := m.View().Content
	if !strings.Contains(view, "Configure / install") || strings.Contains(view, "Use for new setups") {
		t.Fatalf("immediate setup action is mislabeled: %s", view)
	}
}

func TestManagementScreensAndMenuUseApprovedPalette(t *testing.T) {
	m := fixtureModel(t)
	for _, screen := range []string{"Agents", "Environments", "Settings"} {
		m.navigate(screen)
		view := m.View().Content
		if !strings.Contains(view, "48;2;9;38;111") || !strings.Contains(view, "48;2;233;242;251") {
			t.Fatalf("%s lost navy background or selected row highlight: %q", screen, view)
		}
		if got := defaultBackgroundGlyphs(view); got != 0 {
			t.Fatalf("%s has %d default-background glyphs", screen, got)
		}
	}
	press(m, tea.KeyF9, "")
	view := m.View().Content
	if !strings.Contains(view, "38;2;255;227;138") || !strings.Contains(view, "48;2;233;242;251") {
		t.Fatalf("management menu lost gold title or selected highlight: %q", view)
	}
	if got := defaultBackgroundGlyphs(view); got != 0 {
		t.Fatalf("management menu has %d default-background glyphs", got)
	}
}

func TestManagementScreensShareFullHeaderAndWiderDetailPane(t *testing.T) {
	m := fixtureModel(t)
	for _, screen := range []string{"Agents", "Environments", "Settings"} {
		m.navigate(screen)
		lines := strings.Split(ansi.Strip(m.View().Content), "\n")
		if !strings.Contains(lines[0], "AACT · Another Agent Capability Toolkit") || !strings.Contains(lines[1], "F9 Main menu") || !strings.Contains(lines[1], "F2 Open / Focus") || !strings.Contains(lines[2], "Capability Pack:") || !strings.Contains(lines[2], "Environment directory:") {
			t.Fatalf("%s lacks shared title, menu, or scope: %q", screen, lines[:3])
		}
		if screen != "Settings" {
			separator := strings.Index(lines[4], "╦")
			if separator < 0 || utf8.RuneCountInString(lines[4][:separator]) >= 45 {
				t.Fatalf("%s detail pane is not wider: %q", screen, lines[4])
			}
		}
	}
}

func TestEnvironmentInlineControlsUseExistingActions(t *testing.T) {
	m := fixtureModel(t)
	m.navigate("Environments")
	view := ansi.Strip(m.View().Content)
	for _, label := range []string{"Configure / install", "View target", "Environment directory", "Close"} {
		if !strings.Contains(view, label) {
			t.Fatalf("environment action %q is not visible: %s", label, view)
		}
	}
	for _, hit := range m.management.Hits {
		if hit.Control == "env-action" && hit.Index == 1 {
			m.Update(tea.MouseClickMsg{X: hit.X + 1, Y: hit.Y, Button: tea.MouseLeft})
			if m.form != nil || m.busy || !strings.Contains(m.output, "select an actual TOML target") {
				t.Fatalf("disabled View target became active: form=%v busy=%t output=%q", m.form, m.busy, m.output)
			}
			break
		}
	}
	m.View()
	for _, hit := range m.management.Hits {
		if hit.Control == "env-action" && hit.Index == 3 {
			m.Update(tea.MouseClickMsg{X: hit.X + 1, Y: hit.Y, Button: tea.MouseLeft})
			break
		}
	}
	if m.view != "Catalog" {
		t.Fatalf("Close did not return home: %q", m.view)
	}
}

func TestManagementUsesCanonicalEnvironmentRootSetting(t *testing.T) {
	m := fixtureModel(t)
	delete(m.settings, "environment_root")
	m.settings["environment-root"] = "/actual/environments"
	m.navigate("Environments")
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "Environment directory: /actual/environments") {
		t.Fatalf("Environments omitted canonical root: %s", view)
	}
	m.navigate("Settings")
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "Environment directory: /actual/environments") {
		t.Fatalf("Settings omitted canonical root: %s", view)
	}
	m.editEnvironmentRoot()
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "/actual/environments") {
		t.Fatalf("root editor lost canonical prefill: %s", view)
	}
}

func TestNoEnvironmentFileStartsSetupForSelectedCapability(t *testing.T) {
	b := &setupEnvironmentBackend{}
	m := New(b).(*Model)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.Update(m.Init()())
	m.navigate("Environments")
	press(m, tea.KeyEnter, "")
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("No environment file did not start selected capability setup")
	}
	m.Update(cmd())
	if b.previewRequest != (viewmodel.SetupRequest{SourceID: "team-source", PackageID: "plain"}) || m.form == nil {
		t.Fatalf("no-file setup lost selected capability: %+v, form=%v", b.previewRequest, m.form)
	}
}

func TestSavedProfileDoesNotBecomeSetupTarget(t *testing.T) {
	b := &setupEnvironmentBackend{}
	m := New(b).(*Model)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.Update(m.Init()())
	m.profileSnapshot = &viewmodel.ProfileSnapshot{Profiles: []viewmodel.Profile{{Key: state.Key{Source: "team-source", Package: "plain", Environment: "dev", Target: "saved"}}}}
	m.navigate("Environments")
	press(m, tea.KeyDown, "")
	press(m, tea.KeyRight, "")
	press(m, tea.KeyEnter, "")
	if !strings.Contains(m.View().Content, "Configure / install selected target… — disabled") {
		t.Fatalf("saved profile offered as target TOML: %s", m.View().Content)
	}
}

func (b *typedEnvironmentBackend) UIEnvironmentSnapshot(context.Context) (viewmodel.EnvironmentSnapshot, error) {
	return b.snapshot, nil
}

func (b *typedEnvironmentBackend) UIEnvironmentTarget(_ context.Context, path string) (string, error) {
	content, ok := b.contents[path]
	if !ok {
		return "", fmt.Errorf("unknown target %s", path)
	}
	return content, nil
}

func TestEnvironmentBrowserShowsActualTOMLAndExactViewer(t *testing.T) {
	b := &typedEnvironmentBackend{snapshot: viewmodel.EnvironmentSnapshot{SourceID: "team", Root: "/environments", Targets: []viewmodel.EnvironmentTarget{
		{SourceID: "team", Environment: "dev", PackageID: "inspect", Name: "broken", Path: "/environments/dev/inspect/broken.toml", Error: "invalid TOML"},
		{SourceID: "team", Environment: "dev", PackageID: "inspect", Name: "production", Path: "/environments/dev/inspect/production.toml"},
	}}, contents: map[string]string{"/environments/dev/inspect/broken.toml": "[invalid\n", "/environments/dev/inspect/production.toml": "token = 'visible-secret'\n"}}
	m := New(b).(*Model)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.Update(m.Init()())
	m.navigate("Environments")
	view := m.View().Content
	if !strings.Contains(view, "No environment file") || !strings.Contains(view, "Pack: team · Environment: dev") {
		t.Fatalf("actual environment targets missing: %s", view)
	}
	press(m, tea.KeyDown, "")
	press(m, tea.KeyRight, "")
	view = m.View().Content
	if !strings.Contains(view, "broken") || !strings.Contains(view, "Invalid TOML") || !strings.Contains(view, "production") {
		t.Fatalf("selected target list missing: %s", view)
	}
	press(m, tea.KeyEnter, "")
	if !strings.Contains(m.View().Content, "View target") || strings.Contains(m.View().Content, "View target — disabled") {
		t.Fatal("exact target view unavailable")
	}
	press(m, tea.KeyDown, "")
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("view target did not request TOML")
	}
	m.Update(cmd())
	view = m.View().Content
	if !strings.Contains(view, "[invalid") || !strings.Contains(view, "broken.toml") {
		t.Fatalf("exact invalid TOML not shown: %s", view)
	}
	press(m, tea.KeyEscape, "")
	if m.view != "Environments" {
		t.Fatal("target viewer lost browser")
	}
}

func TestEnvironmentBrowserShowsEmptyDirectoryWithoutInventingTarget(t *testing.T) {
	b := &typedEnvironmentBackend{snapshot: viewmodel.EnvironmentSnapshot{SourceID: "team", Root: "/environments", Environments: []string{"empty"}}}
	m := New(b).(*Model)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.Update(m.Init()())
	m.navigate("Environments")
	press(m, tea.KeyDown, "")
	view := m.View().Content
	if !strings.Contains(view, "Pack: team · Environment: empty") || !strings.Contains(view, "No targets") {
		t.Fatalf("empty environment missing: %s", view)
	}
	press(m, tea.KeyEnter, "")
	if !strings.Contains(m.View().Content, "View target — disabled") {
		t.Fatal("empty environment offered exact target")
	}
}

func (b *typedAgentBackend) UIAgentManagement(context.Context) ([]viewmodel.AgentManagementRow, error) {
	return b.rows, nil
}

func (b *typedAgentBackend) UIAgentConfig(_ context.Context, id, path string) (string, error) {
	expectedPath := "/home/test/config.json"
	if id == "claude" {
		expectedPath = "/tmp/claude.json"
	}
	if (id != "codex" && id != "claude") || path != expectedPath {
		return "", fmt.Errorf("unexpected config request %s %s", id, path)
	}
	return b.content, nil
}

func typedAgentModel(t *testing.T) (*Model, *typedAgentBackend) {
	t.Helper()
	b := &typedAgentBackend{rows: []viewmodel.AgentManagementRow{{ID: "codex", Name: "Codex", Detection: "installed", Evidence: "CLI at /usr/bin/codex", ConfigFiles: []viewmodel.AgentConfigFile{{Path: "/home/test/config.json", Scope: "user", Precedence: "primary", Exists: true}}, Registrations: []string{"team / inspect / dev / production"}}}, content: "{\"token\":\"visible-secret\"}\nsecond line\nthird line"}
	m := New(b).(*Model)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.Update(m.Init()())
	m.navigate("Agents")
	return m, b
}

// This catches a management screen claiming an agent is installed from its ID alone.
func TestAgentsDoesNotInventDetectionOrConfigEvidence(t *testing.T) {
	m := fixtureModel(t)
	m.navigate("Agents")
	view := m.View().Content
	if !strings.Contains(view, "codex") || !strings.Contains(view, "Detection unavailable") {
		t.Fatalf("agent evidence was invented or hidden: %s", view)
	}
	press(m, tea.KeyEnter, "")
	view = m.View().Content
	stripped := strings.ReplaceAll(ansi.Strip(view), "\n", " ")
	if !strings.Contains(stripped, "Configure location") || !strings.Contains(stripped, "unavailable") || strings.Contains(stripped, "View exact configuration") {
		t.Fatalf("unsupported configuration controls were offered: %s", view)
	}
	press(m, tea.KeyEscape, "")
	if m.view != "Agents" || m.selected != 0 || m.management.Focus != CapabilitiesPane {
		t.Fatal("Back from agent details lost the selected row")
	}
}

// This catches discarding verified detection evidence when the service provides it.
func TestAgentsShowsTypedDetectionSeparateFromConfigExistence(t *testing.T) {
	m, _ := typedAgentModel(t)
	view := m.View().Content
	for _, want := range []string{"Codex", "Detected", "CLI at /usr/bin/codex", "/home/test/config.json", "team / inspect / dev / production"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q from agent details: %s", want, view)
		}
	}
}

func TestAgentsDetailPaneUsesSelectedAgentSummary(t *testing.T) {
	m, _ := typedAgentModel(t)
	view := ansi.Strip(m.View().Content)
	for _, want := range []string{"╦ Codex", "Status: Detected", "Config candidates: 1", "/home/test/config.json", "Intended write config", "Observed registrations:"} {
		if !strings.Contains(view, want) {
			t.Fatalf("agent detail summary missing %q: %s", want, view)
		}
	}
	if strings.Contains(view, "Resolution note:") || !strings.Contains(view, "Observed registrations:") {
		t.Fatalf("agent detail shows an empty note or unlabeled registrations: %s", view)
	}
	m = fixtureModel(t)
	m.navigate("Agents")
	press(m, tea.KeyDown, "")
	view = ansi.Strip(m.View().Content)
	stripped := strings.ReplaceAll(ansi.Strip(view), "\n", " ")
	if !strings.Contains(stripped, "╦ claude") || !strings.Contains(stripped, "Status: Unverified") || !strings.Contains(stripped, "supported location editor") {
		t.Fatalf("selected fallback agent summary is misleading: %s", view)
	}
}

// This catches masking config values or replacing exact file contents with a summary.
func TestAgentConfigViewerShowsExactContent(t *testing.T) {
	m, _ := typedAgentModel(t)
	press(m, tea.KeyEnter, "")
	if m.management.Focus != ProfilesPane || !strings.Contains(m.View().Content, "View exact configuration candidate 1") {
		t.Fatal("verified config viewer unavailable")
	}
	press(m, tea.KeyEnter, "")
	if !strings.Contains(ansi.Strip(m.View().Content), "/home/test/config.json") {
		t.Fatal("agent detail omitted exact config path")
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("file viewer did not request file content")
	}
	m.Update(cmd())
	view := m.View().Content
	if !strings.Contains(view, "visible-secret") || !strings.Contains(view, "second line") {
		t.Fatalf("viewer changed exact contents: %s", view)
	}
	press(m, tea.KeyEscape, "")
	if m.view != "Agents" {
		t.Fatal("closing viewer lost Agents screen")
	}
}

// This catches treating a saved profile as proof that an environment TOML exists.
func TestEnvironmentsShowsNoFileAndSeparatesSavedTargets(t *testing.T) {
	m := fixtureModel(t)
	m.profileSnapshot = &viewmodel.ProfileSnapshot{Profiles: []viewmodel.Profile{{Key: state.Key{Source: "team", Package: "inspect", Environment: "company", Target: "production"}, Name: "production"}}}
	m.navigate("Environments")
	view := m.View().Content
	if !strings.Contains(view, "No environment file") || !strings.Contains(view, "Pack: team · Environment: company") || !strings.Contains(view, "Saved · Pack: team") {
		t.Fatalf("environment browser lost file/profile distinction: %s", view)
	}
	press(m, tea.KeyDown, "")
	press(m, tea.KeyTab, "")
	view = m.View().Content
	if !strings.Contains(view, "production") {
		t.Fatalf("saved target unavailable from right pane: %s", view)
	}
	press(m, tea.KeyEnter, "")
	if !strings.Contains(m.View().Content, "View target") || !strings.Contains(m.View().Content, "disabled") {
		t.Fatal("exact TOML viewer offered without source data")
	}
}

// This catches Help returning to Home when it was opened from a management screen.
func TestManagementHelpReturnsToOrigin(t *testing.T) {
	m := fixtureModel(t)
	m.navigate("Settings")
	press(m, tea.KeyDown, "")
	selected := m.selected
	press(m, tea.KeyF1, "")
	if m.view != "Help" || !strings.Contains(m.View().Content, "Open / focus") || strings.Contains(m.View().Content, "fixed:") {
		t.Fatal("context help not shown")
	}
	press(m, tea.KeyEscape, "")
	if m.view != "Settings" || m.selected != selected {
		t.Fatalf("Help did not restore Settings selection: view=%q selected=%d", m.view, m.selected)
	}
}

func TestMainMenuFromSettingsClosesBackToSettings(t *testing.T) {
	m, _ := homeFixture()
	m.navigate("Settings")
	m.selected = 1
	press(m, tea.KeyF9, "")
	if m.view != "Settings" || !strings.Contains(m.View().Content, "Main menu") {
		t.Fatalf("F9 replaced Settings instead of overlaying it: view=%q", m.view)
	}
	press(m, tea.KeyEscape, "")
	if m.view != "Settings" || m.selected != 1 {
		t.Fatalf("Esc did not restore Settings selection: view=%q selected=%d", m.view, m.selected)
	}
}

// This catches management navigation being keyboard-only in a mouse-capable terminal.
func TestAgentManagementMouseSelectAndAction(t *testing.T) {
	m, backend := typedAgentModel(t)
	backend.rows = append(backend.rows, viewmodel.AgentManagementRow{ID: "claude", Name: "Claude", Detection: "detected", ConfigFiles: []viewmodel.AgentConfigFile{{Path: "/tmp/claude.json", Exists: true}}})
	m.agentManagement = backend.rows
	m.View()
	rowFound := false
	for _, hit := range m.management.Hits {
		if hit.Control == "row" && hit.Index == 1 {
			m.Update(tea.MouseClickMsg{X: hit.X + 1, Y: hit.Y, Button: tea.MouseLeft})
			rowFound = true
			break
		}
	}
	if !rowFound {
		t.Fatal("second agent row has no mouse hit region")
	}
	if m.selected != 1 {
		t.Fatalf("mouse selected row %d, want second agent", m.selected)
	}
	m.View()
	for _, hit := range m.management.Hits {
		if hit.Control == "actions" {
			m.Update(tea.MouseClickMsg{X: hit.X + 1, Y: hit.Y, Button: tea.MouseLeft})
			break
		}
	}
	if m.management.Focus != ProfilesPane || !strings.Contains(ansi.Strip(m.View().Content), "View exact configuration candidate 1") {
		t.Fatal("Open / Focus control did not reveal the selected agent action")
	}
	for _, hit := range m.management.Hits {
		if hit.Control == "agent-config" {
			_, cmd := m.Update(tea.MouseClickMsg{X: hit.X + 1, Y: hit.Y, Button: tea.MouseLeft})
			if cmd == nil {
				t.Fatal("mouse config action did not request exact viewer")
			}
			m.Update(cmd())
			if m.management.Modal != "viewer" {
				t.Fatal("mouse config action did not open viewer")
			}
			return
		}
	}
	t.Fatal("exact config action has no mouse hit region")
}

// This catches private and public Capability Packs being collapsed into one environment.
func TestEnvironmentBrowserKeepsCapabilityPackIdentity(t *testing.T) {
	m := fixtureModel(t)
	m.profileSnapshot = &viewmodel.ProfileSnapshot{Profiles: []viewmodel.Profile{
		{Key: state.Key{Source: "public", Package: "inspect", Environment: "dev", Target: "one"}},
		{Key: state.Key{Source: "company", Package: "inspect", Environment: "dev", Target: "two"}},
	}}
	m.navigate("Environments")
	view := m.View().Content
	if !strings.Contains(view, "Pack: public · Environment: dev") || !strings.Contains(view, "Pack: company · Environment: dev") {
		t.Fatalf("source identity lost: %s", view)
	}
	press(m, tea.KeyDown, "")
	press(m, tea.KeyRight, "")
	if !strings.Contains(m.View().Content, "two") || strings.Contains(m.View().Content, "one · Saved profile") {
		t.Fatal("targets from two sources mixed")
	}
}

// This catches unavailable Settings writes being implied by a selectable row.
func TestSettingsActionsExplainMissingPreferenceService(t *testing.T) {
	m := fixtureModel(t)
	m.navigate("Settings")
	m.selected = 2
	press(m, tea.KeyEnter, "")
	view := strings.ReplaceAll(ansi.Strip(m.View().Content), "\n", " ")
	if !strings.Contains(view, "Runtime backend") || !strings.Contains(view, "unavailable") || !strings.Contains(view, "service") || strings.Contains(view, "disabled:") || strings.Contains(view, "[ Select backend") {
		t.Fatalf("settings did not explain the unavailable runtime service: %s", view)
	}
}

type editableSettingsBackend struct {
	fixtureBackend
	saved []string
}

func (*editableSettingsBackend) UIAgentDefaultOptions(context.Context) ([]string, error) {
	return []string{"codex", "claude"}, nil
}
func (b *editableSettingsBackend) UISetDefaultAgents(_ context.Context, ids []string) error {
	b.saved = append([]string(nil), ids...)
	return nil
}
func (*editableSettingsBackend) UISettings(context.Context) (map[string]string, error) {
	return map[string]string{"environment_root": "/environments", "default_agents": "claude"}, nil
}

// The Settings action must save a future-install preference without running an install.
func TestSettingsDefaultNamedAgentsFormSavesSelection(t *testing.T) {
	backend := &editableSettingsBackend{}
	m := New(backend).(*Model)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.Update(m.Init()())
	m.navigate("Settings")
	press(m, tea.KeyDown, "")
	press(m, tea.KeyF2, "")
	press(m, tea.KeyEnd, "")
	press(m, tea.KeyEnter, "")
	if m.form == nil || !strings.Contains(m.View().Content, "Default named agents") || !strings.Contains(m.View().Content, "[claude]") {
		t.Fatalf("default-agent form missing or not prefilled: %s", m.View().Content)
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("saving default agents did not dispatch")
	}
	m.Update(cmd())
	if !reflect.DeepEqual(backend.saved, []string{"claude"}) {
		t.Fatalf("saved agents = %#v", backend.saved)
	}
}

func TestManagementSettingsEditorIsCenteredOverlayAndKeepsOrigin(t *testing.T) {
	m := fixtureModel(t)
	m.navigate("Settings")
	press(m, tea.KeyEnter, "")
	press(m, tea.KeyEnd, "")
	press(m, tea.KeyEnter, "")
	if m.form == nil || !m.management.FormOverlay {
		t.Fatal("Environment directory editor did not open as a management overlay")
	}
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "F9 Main menu: Agents") || !strings.Contains(view, "Capability Pack · Choose environment directory") {
		t.Fatalf("editor replaced its management origin or lost its task title:\n%s", view)
	}
	if !strings.Contains(view, "Esc cancel") || strings.Contains(view, "Esc back") {
		t.Fatalf("Settings editor advertises the wrong Escape action:\n%s", view)
	}
	if got := len(strings.Split(view, "\n")); got != 30 {
		t.Fatalf("overlay changed terminal height: %d", got)
	}
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 35})
	if !strings.Contains(ansi.Strip(m.View().Content), "Capability Pack · Choose environment directory") {
		t.Fatal("resize discarded the management editor overlay")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.form != nil || m.management.FormOverlay || m.view != "Settings" {
		t.Fatalf("Cancel did not return to Settings: form=%v overlay=%t view=%q", m.form, m.management.FormOverlay, m.view)
	}
}

func TestManagementDefaultAgentEditorUsesNamedOverlayTitle(t *testing.T) {
	backend := &editableSettingsBackend{}
	m := New(backend).(*Model)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.Update(m.Init()())
	m.navigate("Settings")
	press(m, tea.KeyDown, "")
	press(m, tea.KeyEnter, "")
	press(m, tea.KeyEnd, "")
	press(m, tea.KeyEnter, "")
	view := ansi.Strip(m.View().Content)
	if m.form == nil || !m.management.FormOverlay || !strings.Contains(view, "Agent defaults · Edit future MCP destinations") || !strings.Contains(view, "F9 Main menu: Agents") {
		t.Fatalf("default agent editor overlay/title missing:\n%s", view)
	}
}

func TestSettingsNoLongerHasHiddenCheckboxSavePath(t *testing.T) {
	backend := &editableSettingsBackend{}
	m := New(backend).(*Model)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.Update(m.Init()())
	m.navigate("Settings")
	view := ansi.Strip(m.View().Content)
	for _, want := range []string{"Capability Pack", "Agent defaults", "Runtime backend", "Diagnostics"} {
		if !strings.Contains(view, want) {
			t.Fatalf("Settings missing category %q: %s", want, view)
		}
	}
	press(m, tea.KeySpace, " ")
	m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if backend.saved != nil || m.form != nil || m.view != "Settings" {
		t.Fatalf("Space/Ctrl+S mutated category state: view=%q form=%v saved=%v", m.view, m.form, backend.saved)
	}
}

func TestSettingsUnavailableBackendAndCheckoutControlsStayInert(t *testing.T) {
	m := fixtureModel(t)
	m.navigate("Settings")
	m.selected = 2
	press(m, tea.KeyEnter, "")
	view := strings.ReplaceAll(ansi.Strip(m.View().Content), "\n", " ")
	if !strings.Contains(view, "unavailable") || !strings.Contains(view, "service") || strings.Contains(view, "Select backend") || strings.Contains(view, "Check backend") || strings.Contains(view, "Known checkout") {
		t.Fatalf("unavailable services are shown as fake controls: %s", view)
	}
}

func TestSettingsSkillOnlyNoteWrapsWithoutHidingActions(t *testing.T) {
	backend := &editableSettingsBackend{}
	m := New(backend).(*Model)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.Update(m.Init()())
	m.navigate("Settings")
	m.selected = 1
	view := ansi.Strip(m.View().Content)
	for _, want := range []string{
		"Skill-only installations use All — ~/.agents/skills.",
		"Existing registrations are unchanged.",
		"Edit default named agents",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("Settings clipped %q: %s", want, view)
		}
	}
	for i, line := range strings.Split(view, "\n") {
		if width := ansi.StringWidth(line); width != 100 {
			t.Fatalf("Settings line %d has width %d, want 100", i, width)
		}
	}
}

func TestSettingsCategoryNavigationFitsSupportedTerminalSizes(t *testing.T) {
	for _, size := range []tea.WindowSizeMsg{{Width: 80, Height: 24}, {Width: 100, Height: 30}} {
		backend := &editableSettingsBackend{}
		m := New(backend).(*Model)
		m.Update(size)
		m.Update(m.Init()())
		m.navigate("Settings")
		view := ansi.Strip(m.View().Content)
		lines := strings.Split(view, "\n")
		if len(lines) != size.Height {
			t.Fatalf("%dx%d Settings height changed: %d", size.Width, size.Height, len(lines))
		}
		if strings.Contains(view, "[ Save ]") || strings.Contains(view, "[ Cancel ]") {
			t.Fatalf("%dx%d category screen exposes detached Save/Cancel controls: %s", size.Width, size.Height, view)
		}
		press(m, tea.KeyTab, "")
		if m.management.Focus != ProfilesPane {
			t.Fatalf("%dx%d Tab did not focus the category details pane", size.Width, size.Height)
		}
		_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
		if cmd != nil || backend.saved != nil || m.view != "Settings" {
			t.Fatal("Space in the details pane submitted settings")
		}
	}
}

// This catches the visible Help Back control being inert for mouse users.
func TestHelpVisibleBackControlIsClickable(t *testing.T) {
	m := fixtureModel(t)
	m.navigate("Help")
	m.View()
	for _, hit := range m.management.Hits {
		if hit.Control == "back" {
			m.Update(tea.MouseClickMsg{X: hit.X + 1, Y: hit.Y, Button: tea.MouseLeft})
			if m.view != "Catalog" {
				t.Fatal("click on Help Back did not return home")
			}
			return
		}
	}
	t.Fatal("Help Back has no click region")
}

// This catches Help reopening the environment browser at a different target.
func TestHelpRestoresEnvironmentPaneAndSelection(t *testing.T) {
	m := fixtureModel(t)
	m.profileSnapshot = &viewmodel.ProfileSnapshot{Profiles: []viewmodel.Profile{{Key: state.Key{Source: "team", Package: "inspect", Environment: "dev", Target: "production"}}}}
	m.navigate("Environments")
	press(m, tea.KeyDown, "")
	press(m, tea.KeyRight, "")
	press(m, tea.KeyF1, "")
	press(m, tea.KeyEscape, "")
	if m.view != "Environments" || m.management.EnvironmentIndex != 1 || m.management.Focus != ProfilesPane || !strings.Contains(m.View().Content, "production") {
		t.Fatalf("Help lost selected environment target: %s", m.View().Content)
	}
}

// This catches stale target-pane indexes after an environment disappears on refresh.
func TestEnvironmentRefreshClampsRemovedSelection(t *testing.T) {
	m := fixtureModel(t)
	m.profileSnapshot = &viewmodel.ProfileSnapshot{Profiles: []viewmodel.Profile{{Key: state.Key{Source: "team", Package: "inspect", Environment: "dev", Target: "production"}}}}
	m.navigate("Environments")
	press(m, tea.KeyDown, "")
	press(m, tea.KeyRight, "")
	m.profileSnapshot = &viewmodel.ProfileSnapshot{}
	press(m, tea.KeyDown, "")
	if m.management.EnvironmentIndex != 0 || !strings.Contains(m.View().Content, "No environment file") {
		t.Fatal("removed environment did not clamp selection")
	}
}
