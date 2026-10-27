package tui

import (
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
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
	preview := viewmodel.SetupPreview{
		Key:            state.Key{Source: "team-source", Package: "cluster-inspector", Environment: "sample-env", Target: "target-a"},
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
	}
	m.workspace = &workspaceState{Key: preview.Key, Section: "Inputs", SectionID: sectionInputsID, Active: true, Preview: &preview}
	m.openSetupForm(preview)
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
	for _, want := range []string{"Save inputs", "Discard changes", "Keep editing"} {
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
	for _, want := range []string{"Inputs", "Agent access", "Runtime", "Logs", "Information", "Token", "Source kubeconfig", "[ Save ]", "[ Cancel ]"} {
		if !strings.Contains(view, want) {
			t.Errorf("setup split overlay missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "Save and apply configuration") {
		t.Fatalf("Inputs stage exposed a combined save-and-install action:\n%s", view)
	}
	if strings.Index(view, "Inputs") > strings.Index(view, "Token") {
		t.Fatalf("Inputs navigation should precede its right-side details:\n%s", view)
	}
}

func TestInteractionSetupKeepsSaveAndApplyFixedAt80By24(t *testing.T) {
	m, _ := openSetupInteraction(t)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if !strings.Contains(m.View().Content, "[ Save ]") || !strings.Contains(m.View().Content, "[ Cancel ]") {
		t.Fatalf("Inputs needs visible Save and Cancel controls at 80x24:\n%s", m.View().Content)
	}
}

func TestInteractionSetupKeyboardMovesBetweenSectionsAndDetails(t *testing.T) {
	m, _ := openSetupInteraction(t)
	setupKey(m, tea.KeyDown, "")
	view := m.View().Content
	if !strings.Contains(view, "Agent access") || !strings.Contains(view, "Destinations") {
		t.Fatalf("Down on the stage list did not open Agent access controls:\n%s", view)
	}
	setupKey(m, tea.KeyRight, "")
	setupKey(m, tea.KeyTab, "")
	setupKey(m, tea.KeyLeft, "")
	if !strings.Contains(m.View().Content, "Agent access") {
		t.Fatalf("left/right and Tab navigation lost the staged workspace:\n%s", m.View().Content)
	}
}

func TestInteractionSetupAuthFieldsStayVisibleAndSwitchOnTyping(t *testing.T) {
	m, _ := openSetupInteraction(t)
	setupKey(m, tea.KeyRight, "")
	view := m.View().Content
	for _, want := range []string{"Token", "Source kubeconfig", "old-token"} {
		if !strings.Contains(view, want) {
			t.Fatalf("authentication field %q missing before edit:\n%s", want, view)
		}
	}
	setupKey(m, tea.KeyDown, "")
	view = m.View().Content
	if got := m.form.Values()["token"]; got != "old-token" {
		t.Fatal("merely focusing Source kubeconfig cleared the active Token value")
	}
	if !strings.Contains(view, "Import source") {
		t.Fatalf("scrolling to Source kubeconfig did not reveal its field hint:\n%s", view)
	}
	setupKey(m, 'm', "m") // Manually edit a file path without opening the native picker.
	_, _ = m.form.Update(tea.KeyPressMsg{Code: 'x', Text: "/tmp/source-kubeconfig"})
	_, _ = m.form.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	view = m.View().Content
	if strings.Contains(view, "old-token") || !strings.Contains(view, "Active method") || !strings.Contains(view, "Source kubeconfig") {
		t.Fatalf("typing Source kubeconfig did not activate it and clear Token:\n%s", view)
	}
}

func TestInteractionSetupTabActionCancelAndEscBack(t *testing.T) {
	m, backend := openSetupInteraction(t)
	setupKey(m, tea.KeyTab, "") // Details.
	for range m.pendingSetup.Inputs {
		setupKey(m, tea.KeyTab, "") // Move through each Inputs field, then Actions.
	}
	if !strings.Contains(m.View().Content, "[Actions]") {
		t.Fatalf("Tab did not focus the fixed action bar:\n%s", m.View().Content)
	}
	_, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	if m.form.FocusArea() != 1 {
		t.Fatalf("Shift+Tab did not return from actions to the last Inputs field: area=%d section=%q/%q\n%s", m.form.FocusArea(), m.workspace.SectionID, m.form.SectionID(), m.View().Content)
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
	setupKey(m, tea.KeyRight, "") // Inputs.
	for i := 0; i < 3; i++ {      // Reach the fourth declared input without requiring a legacy subgroup.
		setupKey(m, tea.KeyDown, "")
	}
	view := m.View().Content
	for _, want := range []string{"Plane", "Grafana"} {
		if !strings.Contains(view, want) {
			t.Errorf("database detail missing %q:\n%s", want, view)
		}
	}
	setupKey(m, tea.KeyLeft, "")
	setupKey(m, tea.KeyDown, "") // Agent access.
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
	if m.form == nil || m.form.SectionID() != sectionInputsID {
		t.Fatalf("test did not begin in Inputs: section=%q form=%v", func() string {
			if m.form == nil {
				return ""
			}
			return m.form.SectionID()
		}(), m.form != nil)
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: 115, Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatalf("Ctrl-S did not submit the setup form:\n%s", m.View().Content)
	}
	m.Update(cmd())
	if backend.installRequest != nil {
		section := ""
		if m.form != nil {
			section = m.form.SectionID()
		}
		t.Fatalf("Ctrl-S saved Inputs but incorrectly installed the capability: saved=%+v installed=%+v section=%q output=%q", backend.savedInputRequest, backend.installRequest, section, m.output)
	}
	if backend.savedInputRequest == nil || m.form == nil || !strings.Contains(m.output, "Inputs saved") {
		t.Fatalf("Ctrl-S did not save Inputs and keep the staged workspace open: output=%q view=%s", m.output, m.View().Content)
	}
}

func TestInteractionSetupTabsThroughInputsToVisibleSave(t *testing.T) {
	m, backend := openSetupInteraction(t)
	if m.form == nil || m.form.SectionID() != sectionInputsID {
		t.Fatalf("test did not begin in Inputs: section=%q", m.form.SectionID())
	}
	setupKey(m, tea.KeyTab, "") // Sections → Details.
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "Tab Controls") || strings.Contains(view, "Tab Areas") {
		t.Fatalf("Inputs footer does not explain Tab field/action navigation:\n%s", view)
	}
	if m.form.FocusArea() != 1 || strings.Contains(ansi.Strip(m.form.View().Content), "> [ Save ]") {
		t.Fatalf("first Tab should enter Inputs details, not skip to actions: area=%d\n%s", m.form.FocusArea(), ansi.Strip(m.form.View().Content))
	}
	setupKey(m, tea.KeyDown, "")
	setupKey(m, tea.KeyDown, "")
	if !strings.Contains(ansi.Strip(m.form.View().Content), "> Endpoint") {
		t.Fatalf("test could not focus the Endpoint input:\n%s", ansi.Strip(m.form.View().Content))
	}
	m.workspace.SaveNotice = "Inputs saved successfully."
	setupKey(m, tea.KeyEnter, "")
	_, _ = m.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	_, _ = m.Update(tea.KeyPressMsg{Text: "edited-url"})
	if m.workspace.SaveNotice != "" {
		t.Fatalf("typing a changed value left a stale save-success notice visible: %q", m.workspace.SaveNotice)
	}
	setupKey(m, tea.KeyTab, "") // Commit Endpoint and move to Databases.
	if m.form.Values()["endpoint"] != "edited-url" {
		t.Fatalf("Tab did not commit the active Endpoint edit before moving: %#v", m.form.Values()["endpoint"])
	}
	if m.form.FocusArea() != 1 {
		t.Fatalf("Tab skipped from Endpoint to Actions instead of the next field: area=%d\n%s", m.form.FocusArea(), ansi.Strip(m.form.View().Content))
	}
	setupKey(m, tea.KeyTab, "") // Last field → Save.
	if m.form.FocusArea() != 2 || m.form.SectionID() != sectionInputsID {
		t.Fatalf("Tab did not reach Inputs Save after its fields: area=%d section=%q view=%s", m.form.FocusArea(), m.form.SectionID(), ansi.Strip(m.View().Content))
	}
	if !strings.Contains(ansi.Strip(m.View().Content), "> [ Save ]") {
		t.Fatalf("Inputs Save is not visibly focused at the end of the Tab sequence:\n%s", ansi.Strip(m.View().Content))
	}
	setupKey(m, tea.KeyTab, "") // Save → Cancel.
	if !strings.Contains(ansi.Strip(m.View().Content), "> [ Cancel ]") {
		t.Fatalf("Tab did not move from Save to Cancel:\n%s", ansi.Strip(m.View().Content))
	}
	setupKey(m, tea.KeyTab, "") // Cancel → first field.
	if m.form.FocusArea() != 1 {
		t.Fatalf("Tab did not wrap from Cancel to the first input: area=%d", m.form.FocusArea())
	}
	_, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}) // First field → Cancel.
	if m.form.FocusArea() != 2 || !strings.Contains(ansi.Strip(m.View().Content), "> [ Cancel ]") {
		t.Fatalf("Shift+Tab did not reverse from first input to Cancel:\n%s", ansi.Strip(m.View().Content))
	}
	setupKey(m, tea.KeyTab, "") // Cancel → first field.
	for range m.pendingSetup.Inputs {
		setupKey(m, tea.KeyTab, "") // First field → Save.
	}
	if !strings.Contains(ansi.Strip(m.View().Content), "> [ Save ]") {
		t.Fatalf("returning through Inputs did not focus Save again:\n%s", ansi.Strip(m.View().Content))
	}
	if cmd := setupKey(m, tea.KeyEnter, ""); cmd != nil {
		m.Update(cmd())
	}
	if backend.savedInputRequest == nil || backend.savedInputRequest.Inputs["endpoint"] != "edited-url" || backend.installRequest != nil {
		t.Fatalf("visible Save did not persist only the current Inputs: saved=%+v install=%+v", backend.savedInputRequest, backend.installRequest)
	}
	if m.form.HasUnsavedChanges() || m.workspace.SaveNotice != "Inputs saved successfully." {
		t.Fatalf("successful visible Save did not clear the draft and report success: dirty=%t notice=%q", m.form.HasUnsavedChanges(), m.workspace.SaveNotice)
	}
}
