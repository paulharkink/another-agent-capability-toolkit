package forms

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
)

func updateFormCommand(m *FormModel, msg tea.Msg) {
	_, cmd := m.Update(msg)
	if cmd != nil {
		m.Update(cmd())
	}
}

func TestUXBrowseOpensEmbeddedBrowserOverForm(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".kubeconfig")
	if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	m := NewForm(context.Background(), []catalog.Input{{Name: "source", Label: "Source", Type: "file"}}, map[string]any{"source": path})
	_, cmd := m.Update(key('b', "b"))
	if cmd != nil || !m.PickerActive() {
		t.Fatal("Browse did not open the embedded picker directly")
	}
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "Browse file") || !strings.Contains(view, ".kubeconfig") || !strings.Contains(view, "Edit package inputs") || strings.Contains(view, "Enter path:") {
		t.Fatalf("picker is not an embedded browser over the form:\n%s", view)
	}
}

func TestUXDirectoryCollectionAddUsesEmbeddedPickerAndAddsOneRow(t *testing.T) {
	dir := t.TempDir()
	current, added := filepath.Join(dir, "current"), filepath.Join(dir, "added")
	for _, path := range []string{current, added} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	m := NewForm(context.Background(), []catalog.Input{{Name: "roots", Type: "directory", Multiple: true}}, map[string]any{"roots": []string{current}})
	m.SetSections(FormSection{Title: "Paths", Fields: []string{"roots"}})
	m.Update(key(tea.KeyRight, ""))
	m.Update(key(tea.KeyDown, ""))
	_, cmd := m.Update(key('a', "a"))
	if cmd != nil || !m.PickerActive() {
		t.Fatal("Add did not open the embedded picker directly")
	}
	updateFormCommand(m, key(tea.KeyTab, ""))
	updateFormCommand(m, tea.PasteMsg{Content: added})
	updateFormCommand(m, key(tea.KeyEnter, ""))
	updateFormCommand(m, key(tea.KeyTab, ""))
	updateFormCommand(m, key(tea.KeyTab, ""))
	updateFormCommand(m, key(tea.KeyEnter, ""))
	if got := m.Values()["roots"]; !reflect.DeepEqual(got, []string{current, added}) {
		t.Fatalf("Add should append exactly one selected directory: got %#v", got)
	}
}

func TestUXPickerEscapeReturnsSameFieldAndDraft(t *testing.T) {
	dir := t.TempDir()
	current := filepath.Join(dir, "current")
	if err := os.Mkdir(current, 0700); err != nil {
		t.Fatal(err)
	}
	m := NewForm(context.Background(), []catalog.Input{{Name: "root", Type: "directory"}}, map[string]any{"root": current})
	m.SetSections(FormSection{Title: "Paths", Fields: []string{"root"}}, FormSection{Title: "Other"})
	m.Update(key(tea.KeyRight, ""))
	_, cmd := m.Update(key('b', "b"))
	if cmd != nil || !m.PickerActive() {
		t.Fatal("Browse did not open the embedded picker directly")
	}
	m.Update(key(tea.KeyEscape, ""))
	if m.PickerActive() || m.selected != 0 || m.sectionIndex != 0 || m.area != 1 || m.editing {
		t.Fatalf("cancel did not restore field focus and draft: browser=%v field=%d section=%d area=%d editing=%v", m.PickerActive(), m.selected, m.sectionIndex, m.area, m.editing)
	}
	if got := m.Values()["root"]; got != current {
		t.Fatalf("cancel changed saved value: got %#v want %q", got, current)
	}
}

func TestUXPickerSelectionUpdatesOneDirectoryRow(t *testing.T) {
	dir := t.TempDir()
	paths := []string{filepath.Join(dir, "first"), filepath.Join(dir, "second"), filepath.Join(dir, "chosen")}
	for _, path := range paths {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	m := NewForm(context.Background(), []catalog.Input{{Name: "roots", Type: "directory", Multiple: true}}, map[string]any{"roots": []string{paths[0], paths[1]}})
	m.SetSections(FormSection{Title: "Paths", Fields: []string{"roots"}})
	m.Update(key(tea.KeyRight, ""))
	m.Update(key(tea.KeyDown, ""))
	_, cmd := m.Update(key('b', "b"))
	if cmd != nil || !m.PickerActive() {
		t.Fatal("Browse did not open the embedded picker directly")
	}
	// Navigate to the parent, then into the chosen sibling directory.
	m.Update(key(tea.KeyEnter, ""))
	m.Update(key(tea.KeyEnter, ""))
	for range 3 {
		m.Update(key(tea.KeyTab, ""))
	}
	m.Update(key(tea.KeyEnter, ""))
	if m.PickerActive() {
		t.Fatalf("picker did not submit selected directory:\n%s", ansi.Strip(m.browser.View().Content))
	}
	if got := m.Values()["roots"]; !reflect.DeepEqual(got, []string{paths[0], paths[2]}) {
		t.Fatalf("picker should replace only selected row: got %#v message=%q", got, m.message)
	}
}

func TestUXPickerKeyboardDoesNotNavigateParentForm(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"one", "two"} {
		if err := os.Mkdir(filepath.Join(dir, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	m := NewForm(context.Background(), []catalog.Input{{Name: "root", Type: "directory"}, {Name: "other", Type: "string"}}, map[string]any{"root": dir})
	m.SetSections(FormSection{Title: "Paths", Fields: []string{"root"}}, FormSection{Title: "Other", Fields: []string{"other"}})
	m.Update(key(tea.KeyRight, ""))
	field, section := m.selected, m.sectionIndex
	_, cmd := m.Update(key('b', "b"))
	if cmd != nil || !m.PickerActive() {
		t.Fatal("Browse did not open the embedded picker directly")
	}
	m.Update(key(tea.KeyDown, ""))
	m.Update(key(tea.KeyTab, ""))
	m.Update(tea.PasteMsg{Content: filepath.Join(dir, "two")})
	if m.selected != field || m.sectionIndex != section || !m.PickerActive() {
		t.Fatalf("picker input escaped to parent form: selected %d→%d section %d→%d picker=%v", field, m.selected, section, m.sectionIndex, m.PickerActive())
	}
}

func TestUXExplicitBrowseDoesNotReplaceEnterEditing(t *testing.T) {
	dir := t.TempDir()
	current := filepath.Join(dir, "config")
	if err := os.WriteFile(current, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	m := NewForm(context.Background(), []catalog.Input{{Name: "source", Type: "file"}}, map[string]any{"source": current})
	_, cmd := m.Update(key(tea.KeyEnter, ""))
	if cmd != nil || !m.editing || m.PickerActive() || m.buffer != current {
		t.Fatalf("Enter should edit path in place: cmd=%v editing=%v picker=%v buffer=%q", cmd != nil, m.editing, m.PickerActive(), m.buffer)
	}
	_, cmd = m.Update(key(tea.KeyEscape, ""))
	if cmd != nil || m.editing {
		t.Fatal("Escape should finish edit without opening picker")
	}
	_, cmd = m.Update(key('b', "b"))
	if cmd != nil || !m.PickerActive() {
		t.Fatal("Browse did not open embedded picker")
	}
}
