package tui

import (
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

func openSetupInteraction(t *testing.T) (*Model, *setupBackendFixture) {
	t.Helper()
	backend := &setupBackendFixture{}
	m := NewContext(t.Context(), backend)
	m.catalog = []catalog.Package{{ID: "cluster-inspector", MCP: &catalog.MCP{}}}
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 28})
	m.openSetupForm(viewmodel.SetupPreview{
		Key:            state.Key{Source: "team-source", Package: "cluster-inspector", Environment: "home", Target: "pms15"},
		PackageName:    "Cluster Inspector",
		MCP:            true,
		MCPDefinitions: []catalog.MCP{{Name: "cluster-inspector"}},
		HasManifestUI:  true,
		Sections: []catalog.Section{
			{ID: "authentication", Title: "Authentication", Fields: []string{"token", "kubeconfig", "endpoint"}},
			{ID: "databases", Title: "Databases", Fields: []string{"databases"}},
		},
		Inputs: []viewmodel.SetupInput{
			{Definition: catalog.Input{Name: "token", Label: "Token", Type: "secret", ExclusiveGroup: "auth"}, Value: "old-token", HasValue: true, Provenance: "saved", Editable: true},
			{Definition: catalog.Input{Name: "kubeconfig", Label: "Source kubeconfig", Hint: "Import source; not a live path · AACT uses managed credential material at runtime", Type: "file", ExclusiveGroup: "auth"}, Editable: true},
			{Definition: catalog.Input{Name: "endpoint", Label: "Endpoint", Type: "string"}, Value: "saved-url", HasValue: true, Provenance: "saved", InheritedValue: "environment-url", HasInheritedValue: true, InheritedOrigin: "environment", InheritedPath: "/env/home.toml", Editable: true},
			{Definition: catalog.Input{Name: "databases", Label: "Databases", Type: "multichoice", Options: []catalog.Choice{{Value: "plane", Label: "Plane"}, {Value: "grafana", Label: "Grafana"}}}, Value: []string{"plane"}, HasValue: true, Editable: true},
		},
		Destinations: []viewmodel.SetupDestination{{ID: "all", Path: "/home/test/.agents/skills"}, {ID: "codex", Path: "/home/test/.codex", Selected: true}},
	})
	return m, backend
}

func TestSavedOverrideOffersKeyboardRestoreAndShowsInheritedProvenance(t *testing.T) {
	m, _ := openSetupInteraction(t)
	setupKey(m, tea.KeyRight, "")
	setupKey(m, tea.KeyDown, "")
	setupKey(m, tea.KeyDown, "")
	if !strings.Contains(m.View().Content, "Ctrl+R restore inherited value") {
		t.Fatalf("saved override does not explain how to restore inherited value:\n%s", m.View().Content)
	}
	_, _ = m.Update(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	if got := m.form.Values()["endpoint"]; got != "environment-url" {
		t.Fatalf("restore action did not load the lower-precedence value: %#v", got)
	}
	if got := m.form.ResetFields(); len(got) != 1 || got[0] != "endpoint" {
		t.Fatalf("restored field was not tracked for override removal: %#v", got)
	}
	for _, def := range m.form.Definitions() {
		if def.Name == "endpoint" && !strings.Contains(def.Label, "environment") {
			t.Fatalf("field label kept stale saved provenance after restore: %q", def.Label)
		}
	}
}

func TestSetupApplyOmitsRestoredFieldFromAnswersAndSendsResetIntent(t *testing.T) {
	m, backend := openSetupInteraction(t)
	setupKey(m, tea.KeyRight, "")
	setupKey(m, tea.KeyDown, "")
	setupKey(m, tea.KeyDown, "")
	_, _ = m.Update(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	cmd := m.applySetup(m.form.Values())
	if cmd == nil {
		t.Fatal("reset form did not submit")
	}
	m.Update(runTeaCmd(t, m, cmd))
	if backend.installRequest == nil {
		t.Fatal("setup backend did not receive reset form")
	}
	if _, ok := backend.installRequest.Inputs["endpoint"]; ok {
		t.Fatalf("inherited value was submitted as a new override: %+v", backend.installRequest.Inputs)
	}
	if !reflect.DeepEqual(backend.installRequest.ResetInputs, []string{"endpoint"}) {
		t.Fatalf("reset intent was not sent independently of field values: %+v", backend.installRequest)
	}
}

func TestResolvedWorkspaceBaselineIsCleanAfterSyntheticControlsAreSeeded(t *testing.T) {
	m, _ := openSetupInteraction(t)
	if m.form == nil || m.form.HasUnsavedChanges() {
		t.Fatalf("newly opened capability form was marked dirty by initial setup normalization: form=%v values=%v", m.form != nil, m.form.Values())
	}
}

func setupKey(m *Model, code rune, text string) tea.Cmd {
	_, cmd := m.Update(tea.KeyPressMsg{Code: code, Text: text})
	return cmd
}

func discardDirtySetupExit(t *testing.T, m *Model) {
	t.Helper()
	for _, want := range []string{"Apply changes", "Discard changes", "Keep editing"} {
		if !strings.Contains(m.View().Content, want) {
			t.Fatalf("dirty setup exit omitted %q:\n%s", want, m.View().Content)
		}
	}
	setupKey(m, tea.KeyUp, "") // Discard changes.
	setupKey(m, tea.KeyEnter, "")
}

func enableWorkspaceBackForTest(m *Model) {
	if m.pendingSetup == nil || m.form == nil {
		return
	}
	m.workspace = &workspaceState{Key: m.pendingSetup.Key, InvokingView: m.view, Active: true}
	m.form.SetBackNavigation(true)
}

func TestInteractionSetupUsesSectionListAndMatchingDetailsPane(t *testing.T) {
	m, _ := openSetupInteraction(t)
	view := m.View().Content
	for _, want := range []string{"Authentication", "Databases", "Agents", "Token", "Source kubeconfig", "Save and apply"} {
		if !strings.Contains(view, want) {
			t.Errorf("setup split overlay missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "[ Cancel ]") || strings.Contains(view, "Save and apply configuration") {
		t.Fatalf("workspace exposed a second exit/apply action:\n%s", view)
	}
	if strings.Index(view, "Authentication") > strings.Index(view, "Token") {
		t.Fatalf("section list should precede its right-side detail controls:\n%s", view)
	}
}

func TestInteractionSetupKeepsSaveAndApplyFixedAt80By24(t *testing.T) {
	m, _ := openSetupInteraction(t)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if !strings.Contains(m.View().Content, "[ Save and apply ]") || strings.Contains(m.View().Content, "[ Cancel ]") {
		t.Fatalf("one fixed Save and apply action should remain above the bottom border:\n%s", m.View().Content)
	}
}

func TestInteractionSetupKeyboardMovesBetweenSectionsAndDetails(t *testing.T) {
	m, _ := openSetupInteraction(t)
	setupKey(m, tea.KeyDown, "")
	view := m.View().Content
	if !strings.Contains(view, "Databases") || !strings.Contains(view, "Plane") {
		t.Fatalf("Down on section list did not keep the database controls in the right pane:\n%s", view)
	}
	setupKey(m, tea.KeyRight, "")
	setupKey(m, tea.KeyTab, "")
	setupKey(m, tea.KeyLeft, "")
	if !strings.Contains(m.View().Content, "Authentication") {
		t.Fatalf("left/right and Tab navigation lost the setup section list:\n%s", m.View().Content)
	}
}

func TestInteractionSetupAuthFieldsStayVisibleAndSwitchOnTyping(t *testing.T) {
	m, _ := openSetupInteraction(t)
	setupKey(m, tea.KeyRight, "")
	view := m.View().Content
	for _, want := range []string{"Token", "Source kubeconfig", "Import source", "old-token"} {
		if !strings.Contains(view, want) {
			t.Fatalf("authentication field %q missing before edit:\n%s", want, view)
		}
	}
	setupKey(m, tea.KeyDown, "")
	if !strings.Contains(m.View().Content, "old-token") {
		t.Fatal("merely focusing Source kubeconfig cleared the active Token value")
	}
	setupKey(m, 'm', "m") // Manually edit a file path without opening the native picker.
	_, _ = m.form.Update(tea.KeyPressMsg{Code: 'x', Text: "/tmp/source-kubeconfig"})
	_, _ = m.form.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	view = m.View().Content
	if strings.Contains(view, "old-token") || !strings.Contains(view, "Active method") || !strings.Contains(view, "Type a value to switch to Token") {
		t.Fatalf("typing Source kubeconfig did not activate it and clear Token:\n%s", view)
	}
}

func TestInteractionSetupTabActionCancelAndEscBack(t *testing.T) {
	m, backend := openSetupInteraction(t)
	setupKey(m, tea.KeyTab, "") // Details.
	setupKey(m, tea.KeyTab, "") // Actions.
	if !strings.Contains(m.View().Content, "[Actions]") {
		t.Fatalf("Tab did not focus the fixed action bar:\n%s", m.View().Content)
	}
	_, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	if !strings.Contains(m.View().Content, "[Details]") {
		t.Fatalf("Shift+Tab did not return from actions to details:\n%s", m.View().Content)
	}
	if err := m.form.ApplyValues(map[string]any{"token": "edited-token"}); err != nil {
		t.Fatal(err)
	}
	setupKey(m, tea.KeyEscape, "") // Back out of L4.
	if cmd := setupKey(m, tea.KeyEscape, ""); cmd != nil {
		m.Update(cmd())
	}
	if m.form == nil || m.unsavedExit == nil || backend.installRequest != nil {
		t.Fatal("Esc from the workspace did not show the dirty-exit popup")
	}
	discardDirtySetupExit(t, m)
	if m.form != nil || backend.installRequest != nil {
		t.Fatal("Discard changes did not close the form without applying")
	}
	m, backend = openSetupInteraction(t)
	enableWorkspaceBackForTest(m)
	if err := m.form.ApplyValues(map[string]any{"token": "edited-token"}); err != nil {
		t.Fatal(err)
	}
	setupKey(m, tea.KeyRight, "")
	if cmd := setupKey(m, tea.KeyEscape, ""); cmd != nil {
		m.Update(cmd())
	}
	if m.form == nil || !strings.Contains(m.View().Content, "[Sections]") {
		t.Fatalf("Esc from details should return to the section list:\n%s", m.View().Content)
	}
	if cmd := setupKey(m, tea.KeyEscape, ""); cmd != nil {
		m.Update(cmd())
	}
	if m.form == nil || m.unsavedExit == nil || backend.installRequest != nil {
		t.Fatal("Esc from the section list did not show the dirty-exit popup")
	}
	discardDirtySetupExit(t, m)
	if m.form != nil || backend.installRequest != nil || m.workspace == nil || m.workspace.Active {
		t.Fatal("Discard changes from Back did not return to the parent without applying")
	}
	if m.workspace.Draft != nil {
		t.Fatal("Discard changes from Back retained the target draft")
	}
}

func TestInteractionSetupShowsDatabaseAndNamedMCPDestinationsInRightPane(t *testing.T) {
	m, _ := openSetupInteraction(t)
	setupKey(m, tea.KeyDown, "") // Databases.
	setupKey(m, tea.KeyRight, "")
	view := m.View().Content
	for _, want := range []string{"Plane", "Grafana"} {
		if !strings.Contains(view, want) {
			t.Errorf("database detail missing %q:\n%s", want, view)
		}
	}
	setupKey(m, tea.KeyLeft, "")
	setupKey(m, tea.KeyDown, "") // Agents.
	setupKey(m, tea.KeyRight, "")
	view = m.View().Content
	if !strings.Contains(view, "Codex") || !strings.Contains(view, "/home/test/.codex") {
		t.Fatalf("named MCP destination detail missing:\n%s", view)
	}
	if strings.Contains(view, "All —") || strings.Contains(view, "All — /home/test/.agents/skills") {
		t.Fatalf("MCP setup exposed generic All destination:\n%s", view)
	}
}

func TestInteractionSetupEscapeShowsDirtyExitBeforeDiscard(t *testing.T) {
	m, backend := openSetupInteraction(t)
	enableWorkspaceBackForTest(m)
	if err := m.form.ApplyValues(map[string]any{"token": "edited-token"}); err != nil {
		t.Fatal(err)
	}
	if cmd := setupKey(m, tea.KeyEscape, ""); cmd != nil {
		m.Update(cmd())
	}
	if m.form == nil || m.unsavedExit == nil {
		t.Fatalf("Esc from the dirty section list did not show the exit popup: %s", m.View().Content)
	}
	if backend.installRequest != nil {
		t.Fatalf("Esc applied setup changes: %+v", backend.installRequest)
	}
	discardDirtySetupExit(t, m)
	if m.form != nil || m.workspace == nil || m.workspace.Active || backend.installRequest != nil {
		t.Fatal("Discard changes did not return to the parent without applying")
	}
	if m.workspace.Draft != nil {
		t.Fatal("Discard changes from Esc retained the target draft")
	}
}

func TestInteractionSetupCtrlSSavesFromKeyboard(t *testing.T) {
	m, backend := openSetupInteraction(t)
	_, cmd := m.Update(tea.KeyPressMsg{Code: 115, Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatalf("Ctrl-S did not submit the setup form:\n%s", m.View().Content)
	}
	m.Update(runTeaCmd(t, m, cmd))
	if backend.installRequest == nil {
		t.Fatal("Ctrl-S did not apply the saved setup")
	}
	if backend.installRequest.Inputs["token"] != "old-token" || !containsString(backend.installRequest.DestinationIDs, "codex") {
		t.Fatalf("Ctrl-S lost the setup draft or destination selection: %+v", backend.installRequest)
	}
}
