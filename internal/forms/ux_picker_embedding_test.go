package forms

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/picker"
)

func runPickerCommand(t *testing.T, m *FormModel, name, kind, action, initial string, index int) {
	t.Helper()
	runner, complete := m.pickerCommand(name, kind, action, initial, index)
	err := runner.Run()
	m.Update(complete(err))
}

func TestUXUnavailableNativeEmbedsBrowserWithoutStdinPrompt(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".kubeconfig"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	m := NewForm(context.Background(), []catalog.Input{{Name: "source", Label: "Source", Type: "file"}}, map[string]any{"source": filepath.Join(dir, ".kubeconfig")})
	m.nativePicker = func(context.Context, string, string) (string, error) { return "", picker.ErrUnavailable }
	_, cmd := m.Update(key('b', "b"))
	if cmd == nil {
		t.Fatal("explicit Browse returned no command")
	}
	runPickerCommand(t, m, "source", "file", "apply", filepath.Join(dir, ".kubeconfig"), 0)
	if m.browser == nil {
		t.Fatal("native unavailability did not open the embedded browser")
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
	m.Update(key(tea.KeyDown, "")) // the Add row
	m.nativePicker = func(context.Context, string, string) (string, error) { return "", picker.ErrUnavailable }
	_, cmd := m.Update(key('a', "a"))
	if cmd == nil {
		t.Fatal("Add did not invoke the picker")
	}
	runPickerCommand(t, m, "roots", "directory", "add", "", 0)
	if m.browser == nil {
		t.Fatal("unavailable native Add did not open the embedded picker")
	}
	m.Update(key(tea.KeyTab, ""))
	m.Update(tea.PasteMsg{Content: added})
	m.Update(key(tea.KeyEnter, ""))
	m.Update(key(tea.KeyTab, ""))
	m.Update(key(tea.KeyTab, ""))
	m.Update(key(tea.KeyEnter, ""))
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
	m.SetSections(FormSection{Title: "Paths", Fields: []string{"root"}}, FormSection{Title: "Other", Fields: nil})
	m.Update(key(tea.KeyRight, ""))
	m.nativePicker = func(context.Context, string, string) (string, error) { return "", picker.ErrUnavailable }
	_, cmd := m.Update(key('b', "b"))
	if cmd == nil {
		t.Fatal("explicit Browse returned no command")
	}
	runPickerCommand(t, m, "root", "directory", "apply", current, 0)
	m.Update(key(tea.KeyEscape, ""))
	if m.browser != nil || m.selected != 0 || m.sectionIndex != 0 || m.area != 1 || m.editing {
		t.Fatalf("cancel did not restore field focus and draft: browser=%v field=%d section=%d area=%d editing=%v", m.browser != nil, m.selected, m.sectionIndex, m.area, m.editing)
	}
	if got := m.Values()["root"]; got != current {
		t.Fatalf("cancel changed the saved field value: got %#v want %q", got, current)
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
	m.nativePicker = func(context.Context, string, string) (string, error) { return "", picker.ErrUnavailable }
	_, cmd := m.Update(key('b', "b"))
	if cmd == nil {
		t.Fatal("explicit Browse returned no command")
	}
	runPickerCommand(t, m, "roots", "directory", "edit", paths[1], 1)
	if m.browser == nil {
		t.Fatal("native unavailability did not enter embedded picker")
	}
	// Open the parent and then the selected sibling directory.
	m.Update(key(tea.KeyEnter, ""))
	m.Update(key(tea.KeyEnter, ""))
	for range 3 {
		m.Update(key(tea.KeyTab, ""))
	}
	m.Update(key(tea.KeyEnter, ""))
	if m.browser != nil {
		t.Fatalf("picker did not submit selected directory:\n%s", ansi.Strip(m.browser.View().Content))
	}
	if got := m.Values()["roots"]; !reflect.DeepEqual(got, []string{paths[0], paths[2]}) {
		t.Fatalf("directory picker should replace only the selected row: got %#v message=%q", got, m.message)
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
	m.nativePicker = func(context.Context, string, string) (string, error) { return "", picker.ErrUnavailable }
	_, cmd := m.Update(key('b', "b"))
	if cmd == nil {
		t.Fatal("explicit Browse returned no command")
	}
	runPickerCommand(t, m, "root", "directory", "apply", dir, 0)
	m.Update(key(tea.KeyDown, ""))
	m.Update(key(tea.KeyTab, ""))
	m.Update(tea.PasteMsg{Content: filepath.Join(dir, "two")})
	if m.selected != field || m.sectionIndex != section || m.browser == nil {
		t.Fatalf("picker input escaped to parent form: selected %d→%d, section %d→%d, browser=%v", field, m.selected, section, m.sectionIndex, m.browser != nil)
	}
}

func TestUXExplicitBrowseDoesNotReplaceEnterEditing(t *testing.T) {
	dir := t.TempDir()
	current := filepath.Join(dir, "config")
	if err := os.WriteFile(current, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	m := NewForm(context.Background(), []catalog.Input{{Name: "source", Type: "file"}}, map[string]any{"source": current})
	m.nativePicker = func(context.Context, string, string) (string, error) { return "", picker.ErrUnavailable }
	_, cmd := m.Update(key(tea.KeyEnter, ""))
	if cmd != nil || !m.editing || m.browser != nil || m.buffer != current {
		t.Fatalf("Enter should edit the current path in place: cmd=%v editing=%v browser=%v buffer=%q", cmd != nil, m.editing, m.browser != nil, m.buffer)
	}
	_, cmd = m.Update(key(tea.KeyEscape, ""))
	if cmd != nil || m.editing {
		t.Fatal("Escape should finish the edit without opening a picker")
	}
	_, cmd = m.Update(key('b', "b"))
	if cmd == nil {
		t.Fatal("explicit Browse did not invoke the picker")
	}
}

func TestUXNativePickerOutcomesAreScopedToTheForm(t *testing.T) {
	dir := t.TempDir()
	selected := filepath.Join(dir, "selected")
	if err := os.WriteFile(selected, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		path string
		err  error
	}{
		{name: "success", path: selected},
		{name: "cancel", err: picker.ErrCancelled},
		{name: "unavailable", err: picker.ErrUnavailable},
		{name: "error", err: errors.New("native failure")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := NewForm(context.Background(), []catalog.Input{{Name: "source", Type: "file"}}, nil)
			m.nativePicker = func(context.Context, string, string) (string, error) { return tc.path, tc.err }
			_, cmd := m.Update(key('b', "b"))
			if cmd == nil {
				t.Fatal("explicit Browse returned no command")
			}
			runPickerCommand(t, m, "source", "file", "apply", "", 0)
			switch {
			case tc.name == "success":
				if m.browser != nil || m.Values()["source"] != selected {
					t.Fatalf("native selection did not apply: browser=%v value=%v", m.browser != nil, m.Values()["source"])
				}
			case tc.name == "unavailable":
				if m.browser == nil {
					t.Fatal("unavailable native picker did not embed fallback")
				}
			default:
				if m.browser != nil || m.Values()["source"] != nil {
					t.Fatalf("native %s altered the form: browser=%v value=%v", tc.name, m.browser != nil, m.Values()["source"])
				}
			}
		})
	}
}
