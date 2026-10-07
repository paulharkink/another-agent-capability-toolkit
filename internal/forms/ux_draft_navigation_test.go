package forms

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/picker"
)

func TestUXPaneArrowIsolationAndReachableButtons(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{{Name: "a", Type: "string"}, {Name: "b", Type: "string"}}, nil)
	m.SetSections(FormSection{Title: "First", Fields: []string{"a"}}, FormSection{Title: "Second", Fields: []string{"b"}})
	if m.SectionTitle() != "First" {
		t.Fatalf("initial section title = %q", m.SectionTitle())
	}
	m.SelectSection("Second")
	if m.SectionTitle() != "Second" {
		t.Fatalf("selected section title = %q", m.SectionTitle())
	}
	m.FocusSection()
	if m.area != 0 {
		t.Fatalf("FocusSection left focus at area %d", m.area)
	}
	m.SelectSection("First")
	draft := m.Values()
	draft["a"] = "mutated copy"
	if m.Values()["a"] != nil {
		t.Fatalf("Values returned a mutable alias: %#v", m.Values())
	}
	m.Update(key(tea.KeyDown, ""))
	if m.sectionIndex != 1 || m.area != 0 {
		t.Fatalf("Down in sections changed details: section=%d area=%d", m.sectionIndex, m.area)
	}
	m.Update(key(tea.KeyRight, ""))
	m.Update(key(tea.KeyDown, ""))
	if m.sectionIndex != 1 || m.selected != 1 {
		t.Fatalf("Down in details changed section: section=%d selected=%d", m.sectionIndex, m.selected)
	}
	m.Update(key(tea.KeyTab, ""))
	if m.area != 2 {
		t.Fatalf("Tab did not focus visible button bar: area=%d", m.area)
	}
	if m.View().Content == "" {
		t.Fatal("focused actions have no visible rendering")
	}
}

func TestUXBottomActionsStayInTheirPanelAndWorkspaceCanExposeOnlyPrimaryAction(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{{Name: "name", Type: "string"}}, nil)
	m.SetSections(FormSection{Title: "Details", Fields: []string{"name"}})
	m.Update(key(tea.KeyTab, ""))
	m.Update(key(tea.KeyTab, ""))
	if m.area != 2 {
		t.Fatalf("Tab did not enter bottom actions: area=%d", m.area)
	}
	m.Update(key(tea.KeyDown, ""))
	if m.area != 2 || m.actionIndex != 1 {
		t.Fatalf("Down did not remain within the bottom action panel: area=%d action=%d", m.area, m.actionIndex)
	}
	m.Update(key(tea.KeyUp, ""))
	if m.area != 2 || m.actionIndex != 0 {
		t.Fatalf("Up did not return focus to Save: area=%d action=%d", m.area, m.actionIndex)
	}
	m.SetCancelHidden(true)
	m.Update(key(tea.KeyRight, ""))
	m.Update(key(tea.KeyDown, ""))
	if m.actionIndex != 0 || m.area != 2 {
		t.Fatalf("single-action footer moved focus: area=%d action=%d", m.area, m.actionIndex)
	}
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "[ Save ]") || strings.Contains(view, "[ Cancel ]") {
		t.Fatalf("single-action footer is incorrect:\n%s", view)
	}
	if !strings.Contains(m.View().Content, "\x1b[") {
		t.Fatal("focused bottom action did not receive an ANSI focus style")
	}
	_, cmd := m.Update(key(tea.KeyEnter, ""))
	if cmd == nil || !m.done {
		t.Fatal("Enter on the sole bottom action did not submit")
	}
}

func TestUXBackRetainsDraftUntilExplicitCancel(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{{Name: "name", Type: "string"}}, map[string]any{"name": "old"})
	m.SetSections(FormSection{Title: "Details", Fields: []string{"name"}})
	m.Update(key(tea.KeyRight, ""))
	m.Update(key(tea.KeyEnter, ""))
	m.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	m.Update(key('n', "n"))
	m.Update(key(tea.KeyEnter, ""))
	m.Update(key(tea.KeyEscape, ""))
	if got := m.editor.Values()["name"]; got != "n" || m.done {
		t.Fatalf("Back discarded draft or exited form: value=%#v done=%v", got, m.done)
	}
	_, backCmd := m.Update(key(tea.KeyEscape, ""))
	if backCmd == nil {
		t.Fatal("Escape at the section pane did not return a parent-navigation message")
	}
	back, ok := backCmd().(BackMsg)
	if !ok || back.Draft["name"] != "n" {
		t.Fatalf("BackMsg did not preserve the current draft: %#v", backCmd())
	}
	if _, err := m.Result(); err != ErrNotSubmitted {
		t.Fatalf("Back submitted or cancelled the form: %v", err)
	}
	m.Update(key(tea.KeyTab, ""))
	m.Update(key(tea.KeyTab, ""))
	m.Update(key(tea.KeyRight, ""))
	m.Update(key(tea.KeyEnter, ""))
	if _, err := m.Result(); err != picker.ErrCancelled {
		t.Fatalf("explicit Cancel did not report cancellation: %v", err)
	}
}

func TestUXUnsavedChangesTracksSemanticValuesAndCleanBaseline(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{{Name: "name", Type: "string"}}, map[string]any{"name": "old"})
	if m.HasUnsavedChanges() {
		t.Fatal("initial form is dirty")
	}
	m.editor.Apply("name", "new")
	if !m.HasUnsavedChanges() {
		t.Fatal("semantic value change was not detected")
	}
	m.editor.Apply("name", "old")
	if m.HasUnsavedChanges() {
		t.Fatal("reverting to the baseline remained dirty")
	}
	m.editor.Apply("name", "new")
	m.MarkClean()
	if m.HasUnsavedChanges() {
		t.Fatal("MarkClean did not reset the baseline")
	}
}

func TestUXUnsavedChangesIncludesActiveBuffer(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{{Name: "name", Type: "string"}}, map[string]any{"name": "old"})
	m.beginEdit("apply", "old")
	m.buffer = "uncommitted"
	if !m.HasUnsavedChanges() {
		t.Fatal("uncommitted active editor buffer was not detected")
	}
}

func TestUXGuardedBackAndCancelPreserveDirtyForm(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{{Name: "name", Type: "string"}}, map[string]any{"name": "old"})
	m.SetSections(FormSection{Title: "Details", Fields: []string{"name"}})
	m.SetUnsavedExitGuard(true)
	m.editor.Apply("name", "new")
	_, backCmd := m.Update(key(tea.KeyEscape, ""))
	if backCmd == nil {
		t.Fatal("dirty Back did not request exit confirmation")
	}
	back, ok := backCmd().(ExitRequestMsg)
	if !ok || back.Reason != "back" || back.Draft["name"] != "new" {
		t.Fatalf("dirty Back request did not preserve draft: %#v", backCmd())
	}
	if m.done || m.editor.Values()["name"] != "new" {
		t.Fatalf("dirty Back mutated/exited form: done=%v values=%#v", m.done, m.editor.Values())
	}
	_, cancelCmd := m.cancel()
	if cancelCmd == nil {
		t.Fatal("dirty Cancel did not request exit confirmation")
	}
	cancel, ok := cancelCmd().(ExitRequestMsg)
	if !ok || cancel.Reason != "cancel" || cancel.Draft["name"] != "new" {
		t.Fatalf("dirty Cancel request did not preserve draft: %#v", cancelCmd())
	}
	if m.done || m.editor.Values()["name"] != "new" {
		t.Fatalf("dirty Cancel mutated/exited form: done=%v values=%#v", m.done, m.editor.Values())
	}
}

func TestUXSubmitCommitsActiveBufferThroughValidation(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{{Name: "name", Type: "string", Required: true}}, nil)
	m.beginEdit("apply", "")
	m.buffer = "valid"
	_, cmd := m.Submit()
	if cmd == nil {
		t.Fatal("Submit did not use the regular save completion command")
	}
	got, err := m.Result()
	if err != nil || got["name"] != "valid" {
		t.Fatalf("Submit failed to commit active buffer: values=%#v err=%v", got, err)
	}
	if !m.HasUnsavedChanges() {
		t.Fatal("validated Submit marked the form clean before the parent operation succeeded")
	}
	m.MarkClean()
	if m.HasUnsavedChanges() {
		t.Fatal("MarkClean did not clear the submitted form")
	}

	invalid := NewForm(context.Background(), []catalog.Input{{Name: "name", Type: "string", Required: true}}, nil)
	invalid.beginEdit("apply", "")
	_, cmd = invalid.Submit()
	if cmd != nil || invalid.done {
		t.Fatalf("invalid Submit exited form: cmd=%v done=%v", cmd != nil, invalid.done)
	}
}

func TestUXPrepareRetryRestoresSubmittedFormWithoutMarkingClean(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{{Name: "name", Type: "string", Required: true}}, nil)
	m.editor.Apply("name", "submitted")
	m.Submit()
	if !m.done {
		t.Fatal("setup Submit did not reach submitted state")
	}

	m.PrepareRetry("backend apply failed")
	if m.done {
		t.Fatal("PrepareRetry left the form in submitted state")
	}
	if _, err := m.Result(); err != ErrNotSubmitted {
		t.Fatalf("PrepareRetry retained the submission result: %v", err)
	}
	if got := m.Values()["name"]; got != "submitted" {
		t.Fatalf("PrepareRetry lost submitted field values: %#v", m.Values())
	}
	if !m.HasUnsavedChanges() {
		t.Fatal("PrepareRetry marked the failed operation clean")
	}
	if !strings.Contains(m.View().Content, "backend apply failed") {
		t.Fatal("PrepareRetry did not show the backend error in the form")
	}
	m.Update(key(tea.KeyEnter, ""))
	if !m.editing {
		t.Fatal("restored form did not accept edits")
	}
}
