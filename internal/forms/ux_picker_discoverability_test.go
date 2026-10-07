package forms

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
)

func TestUXBrowseControlOpensPickerWhileFieldIsBeingEdited(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{
		{Name: "token", Label: "Token", Type: "secret", ExclusiveGroup: "cluster_credentials"},
		{Name: "kubeconfig", Label: "Source kubeconfig", Type: "file", ExclusiveGroup: "cluster_credentials"},
	}, nil)
	m.SetSections(FormSection{Title: "Authentication", Fields: []string{"token", "kubeconfig"}})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m.Update(key(tea.KeyRight, ""))
	m.Update(key(tea.KeyDown, ""))
	m.Update(key(tea.KeyEnter, ""))
	if !m.editing {
		t.Fatal("Enter did not preserve the in-place field edit")
	}
	const draft = "typed/kube/config"
	m.Update(tea.PasteMsg{Content: draft})
	var browseCmd tea.Cmd
	for y, line := range strings.Split(m.View().Content, "\n") {
		plain := ansi.Strip(line)
		if index := strings.Index(plain, "[Browse · b]"); index >= 0 {
			layout := m.layout()
			bodyY := y - layout.bodyStart
			if bodyY < 0 || bodyY >= len(layout.splitFields) || layout.splitChoices[bodyY] != -2 {
				t.Fatalf("Browse row hit mapping missing: y=%d bodyY=%d fields=%v choices=%v start=%d", y, bodyY, layout.splitFields, layout.splitChoices, layout.bodyStart)
			}
			_, browseCmd = m.Update(tea.MouseClickMsg{X: index, Y: y, Button: tea.MouseLeft})
			break
		}
	}
	if !m.PickerActive() {
		t.Fatalf("clicking the visible Browse control did not open the embedded picker: cmd=%v\n%s", browseCmd != nil, ansi.Strip(m.View().Content))
	}
	if browseCmd != nil {
		m.Update(browseCmd())
	}
	if !m.editing {
		t.Fatal("opening Browse discarded the in-place edit before the picker returned")
	}
	m.Update(key(tea.KeyEscape, ""))
	if !m.editing || m.buffer != draft || m.PickerActive() {
		t.Fatalf("cancelled Browse did not restore the active edit: editing=%v buffer=%q picker=%v", m.editing, m.buffer, m.PickerActive())
	}
}

func TestUXSectionActionRowsAreKeyboardAndMouseSelectable(t *testing.T) {
	m := NewForm(context.Background(), nil, nil)
	m.SetSections(FormSection{Title: "Overview"})
	m.SetSectionContent("Overview", []string{"Read-only facts stay informational."})
	m.SetSectionActions("Overview", FormAction{ID: "start", Label: "Build and start MCP"})
	m.Update(tea.WindowSizeMsg{Width: 90, Height: 24})
	m.Update(key(tea.KeyRight, ""))
	_, cmd := m.Update(key(tea.KeyEnter, ""))
	if cmd == nil {
		t.Fatal("Enter did not activate the focused section action")
	}
	if msg, ok := cmd().(ActionMsg); !ok || msg != (ActionMsg{Section: "Overview", ID: "start"}) {
		t.Fatalf("keyboard activation returned %#v", msg)
	}

	m.area = 1
	for y, line := range strings.Split(m.View().Content, "\n") {
		plain := ansi.Strip(line)
		if index := strings.Index(plain, "[ Build and start MCP ]"); index >= 0 {
			_, cmd = m.Update(tea.MouseClickMsg{X: index, Y: y, Button: tea.MouseLeft})
			if cmd == nil {
				t.Fatal("mouse click did not activate section action")
			}
			if msg, ok := cmd().(ActionMsg); !ok || msg.ID != "start" {
				t.Fatalf("mouse activation returned %#v", msg)
			}
			return
		}
	}
	t.Fatalf("section action was not visible:\n%s", ansi.Strip(m.View().Content))
}

func TestReadOnlySectionCanActivateItsDiagnosticAction(t *testing.T) {
	m := NewForm(context.Background(), nil, nil)
	m.SetSections(FormSection{ID: "tool:endpoint", Title: "Endpoint"})
	m.SetSectionContentID("tool:endpoint", []string{"Observed endpoint facts"})
	m.SetSectionActionsID("tool:endpoint", FormAction{ID: "check", Label: "Check connection"})
	m.SetReadOnly(true)
	m.Update(tea.WindowSizeMsg{Width: 90, Height: 24})
	_, _ = m.Update(key(tea.KeyEnter, "")) // L3 → read-only details.
	_, cmd := m.Update(key(tea.KeyEnter, ""))
	if cmd == nil {
		t.Fatalf("read-only diagnostic action could not be activated:\n%s", ansi.Strip(m.View().Content))
	}
	if msg, ok := cmd().(ActionMsg); !ok || msg != (ActionMsg{Section: "Endpoint", SectionID: "tool:endpoint", ID: "check"}) {
		t.Fatalf("read-only endpoint action returned %#v", msg)
	}
}

func TestUXDisabledSectionActionRemainsVisibleButCannotDispatch(t *testing.T) {
	m := NewForm(context.Background(), nil, nil)
	m.SetSections(FormSection{Title: "Overview"})
	m.SetSectionActions("Overview", FormAction{ID: "start", Label: "Build and start MCP", Disabled: "Docker is unavailable"})
	m.Update(tea.WindowSizeMsg{Width: 90, Height: 24})
	m.Update(key(tea.KeyRight, ""))
	_, cmd := m.Update(key(tea.KeyEnter, ""))
	if cmd != nil || !strings.Contains(ansi.Strip(m.View().Content), "Docker is unavailable") {
		t.Fatalf("disabled action should explain itself without dispatch: cmd=%v\n%s", cmd != nil, ansi.Strip(m.View().Content))
	}
}

func TestUXBrowseCueStaysVisibleBeforeLongPathAndLabel(t *testing.T) {
	path := "/very/long/configuration/path/" + strings.Repeat("cluster-config-", 8) + "config.yaml"
	label := "Tekton repository context hook [target]"
	for _, width := range []int{80, 100} {
		t.Run(fmt.Sprintf("width-%d", width), func(t *testing.T) {
			m := NewForm(context.Background(), []catalog.Input{
				{Name: "repository_context_hook", Label: label, Type: "file"},
			}, map[string]any{"repository_context_hook": path})
			m.SetSections(FormSection{Title: "Authentication", Fields: []string{"repository_context_hook"}})
			m.Update(tea.WindowSizeMsg{Width: width, Height: 16})
			m.Update(key(tea.KeyRight, ""))
			view := ansi.Strip(m.View().Content)
			if !strings.Contains(view, "[Browse · b]") {
				t.Fatalf("Browse control was clipped by a long field label/path:\n%s", view)
			}
			if got := m.Values()["repository_context_hook"]; got != path {
				t.Fatalf("rendering Browse changed the full editable path: %#v", got)
			}
		})
	}
}

func TestUXSectionActionNavigationDoesNotChangeSaveCancelFocus(t *testing.T) {
	m := NewForm(context.Background(), nil, nil)
	m.SetSections(FormSection{Title: "Overview"})
	m.SetSectionActions("Overview",
		FormAction{ID: "one", Label: "One"},
		FormAction{ID: "two", Label: "Two"},
		FormAction{ID: "three", Label: "Three"},
	)
	m.Update(tea.WindowSizeMsg{Width: 90, Height: 24})
	m.Update(key(tea.KeyRight, ""))
	m.Update(key(tea.KeyDown, ""))
	m.Update(key(tea.KeyDown, ""))
	m.Update(key(tea.KeyTab, ""))
	m.Update(key(tea.KeyEnter, ""))
	if !m.done || m.err != nil {
		t.Fatalf("navigating to the third section action changed footer Save focus: done=%t err=%v", m.done, m.err)
	}
}

func TestUXSaveValidationMovesFocusToDestinationFieldSection(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{
		{Name: "kubeconfig", Label: "Source kubeconfig", Type: "file"},
		{Name: "__aact_destinations", Label: "Destinations", Type: "multichoice", Required: true, Options: []catalog.Choice{{Value: "codex", Label: "Codex"}}},
	}, nil)
	m.SetSections(
		FormSection{Title: "Overview"},
		FormSection{Title: "Authentication", Fields: []string{"kubeconfig"}},
		FormSection{Title: "Agents", Fields: []string{"__aact_destinations"}},
	)
	m.SelectSection("Authentication")
	m.area = 1
	m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if m.done || m.SectionTitle() != "Agents" || m.area != 1 || m.selected != 1 {
		t.Fatalf("validation error did not move focus to the field's section: done=%t section=%q area=%d selected=%d", m.done, m.SectionTitle(), m.area, m.selected)
	}
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "Select at least one destination") || !strings.Contains(view, "Codex") {
		t.Fatalf("validation error or correction control is not visible after focus moves:\n%s", view)
	}
}

func TestUXBrowseOpensEmbeddedPickerWithoutCallingNativeDialog(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{{Name: "source", Label: "Source kubeconfig", Type: "file"}}, nil)
	_, cmd := m.Update(key('b', "b"))
	if cmd != nil {
		t.Fatal("opening the embedded picker should not execute an OS-picker command")
	}
	if !m.PickerActive() {
		t.Fatal("Browse did not open the embedded TUI picker")
	}
}

func TestUXBrowseSelectionEndsEditAndSaveKeepsChosenPath(t *testing.T) {
	dir := t.TempDir()
	oldPath, chosenPath := filepath.Join(dir, "old.yaml"), filepath.Join(dir, "chosen.yaml")
	for _, path := range []string{oldPath, chosenPath} {
		if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	m := NewForm(context.Background(), []catalog.Input{{Name: "source", Label: "Source kubeconfig", Type: "file"}}, map[string]any{"source": oldPath})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m.Update(key(tea.KeyEnter, ""))
	if !m.editing {
		t.Fatal("Enter did not begin in-place path editing")
	}
	clicked := false
	for y, line := range strings.Split(m.View().Content, "\n") {
		if x := strings.Index(ansi.Strip(line), "[Browse · b]"); x >= 0 {
			_, cmd := m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
			if cmd != nil || !m.PickerActive() {
				t.Fatal("Browse did not open the embedded picker directly")
			}
			clicked = true
			break
		}
	}
	if !clicked {
		t.Fatal("visible Browse control was not rendered")
	}
	m.Update(key(tea.KeyEnter, ""))
	if m.PickerActive() || m.editing || m.Values()["source"] != chosenPath {
		t.Fatalf("picker selection did not finish editing with the selected path: picker=%v editing=%v", m.PickerActive(), m.editing)
	}
	m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	values, err := m.Result()
	if err != nil || values["source"] != chosenPath {
		t.Fatalf("Save replaced the picker selection with a stale edit buffer: values=%#v err=%v", values, err)
	}
}

func TestUXBrowseMouseSelectsFileThroughCenteredPickerOverlay(t *testing.T) {
	dir := t.TempDir()
	oldPath := filepath.Join(dir, "old.yaml")
	chosenPath := filepath.Join(dir, "chosen.yaml")
	for _, path := range []string{oldPath, chosenPath} {
		if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	m := NewForm(context.Background(), []catalog.Input{{Name: "source", Label: "Source kubeconfig", Type: "file"}}, map[string]any{"source": oldPath})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	_, cmd := m.Update(key('b', "b"))
	if cmd != nil {
		m.Update(cmd())
	}
	if !m.PickerActive() {
		t.Fatal("Browse did not open picker")
	}
	if cmd := clickOverlayText(t, m, "chosen.yaml"); cmd != nil {
		m.Update(cmd())
	}
	if !m.PickerActive() {
		t.Fatalf("clicking the file row unexpectedly closed the picker: value=%v", m.Values()["source"])
	}
	if !strings.Contains(ansi.Strip(m.browser.View().Content), "› chosen.yaml") {
		t.Fatalf("mouse click did not select the file row:\n%s", ansi.Strip(m.browser.View().Content))
	}
	if cmd := clickOverlayText(t, m, "[ Select file ]"); cmd != nil {
		m.Update(cmd())
	}
	if m.PickerActive() || m.Values()["source"] != chosenPath {
		t.Fatalf("mouse Select file did not apply the chosen path: picker=%v value=%v", m.PickerActive(), m.Values()["source"])
	}
}

func TestUXPickerMouseWheelOutsidePopupDoesNotScrollList(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"file-00", "file-01"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	m := NewForm(context.Background(), []catalog.Input{{Name: "source", Type: "file"}}, map[string]any{"source": dir})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	_, _ = m.Update(key('b', "b"))
	before := ansi.Strip(m.browser.View().Content)
	m.Update(tea.MouseWheelMsg{X: 1, Y: 1, Button: tea.MouseWheelDown})
	after := ansi.Strip(m.browser.View().Content)
	if !strings.Contains(before, "› file-00") || !strings.Contains(after, "› file-00") {
		t.Fatalf("wheel outside popup moved list selection:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func clickOverlayText(t *testing.T, m *FormModel, text string) tea.Cmd {
	t.Helper()
	for y, line := range strings.Split(ansi.Strip(m.View().Content), "\n") {
		if byteOffset := strings.Index(line, text); byteOffset >= 0 {
			x := ansi.StringWidth(line[:byteOffset])
			_, cmd := m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
			return cmd
		}
	}
	t.Fatalf("picker overlay did not show %q:\n%s", text, ansi.Strip(m.View().Content))
	return nil
}
