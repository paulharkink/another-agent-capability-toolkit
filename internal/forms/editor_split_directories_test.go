package forms

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
)

func splitDirectoryForm(t *testing.T) (*FormModel, string, string, string) {
	t.Helper()
	root := t.TempDir()
	paths := []string{filepath.Join(root, "one"), filepath.Join(root, "two"), filepath.Join(root, "three")}
	for _, path := range paths {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	m := NewForm(context.Background(), []catalog.Input{{Name: "scan_roots", Label: "Directories to scan", Type: "directory", Multiple: true, Required: true}}, map[string]any{"scan_roots": []any{paths[0], paths[1]}})
	m.SetSections(FormSection{Title: "Inputs", Fields: []string{"scan_roots"}})
	m.Update(tea.WindowSizeMsg{Width: 110, Height: 24})
	m.Update(key(tea.KeyRight, ""))
	return m, paths[0], paths[1], paths[2]
}

func TestSplitDirectoryFormCanEditAndRemoveSavedRows(t *testing.T) {
	m, one, two, three := splitDirectoryForm(t)
	lines := strings.Split(ansi.Strip(m.View().Content), "\n")
	firstLine, secondLine := -1, -1
	for i, line := range lines {
		if strings.Contains(line, string(os.PathSeparator)+filepath.Base(one)) {
			firstLine = i
		}
		if strings.Contains(line, string(os.PathSeparator)+filepath.Base(two)) {
			secondLine = i
		}
	}
	if firstLine < 0 || secondLine < 0 || firstLine == secondLine {
		t.Fatalf("saved directories are not separate selectable rows:\n%s", ansi.Strip(m.View().Content))
	}
	m.Update(key(tea.KeyDown, ""))
	if m.selected != 0 || m.rowIndex["scan_roots"] != 1 {
		t.Fatalf("Down did not select the second directory: field %d, row %d", m.selected, m.rowIndex["scan_roots"])
	}
	m.Update(key('m', "m"))
	if !m.editing || m.buffer != two {
		t.Fatalf("manual edit did not open selected directory: editing=%v buffer=%q", m.editing, m.buffer)
	}
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "Edit: ") || !strings.Contains(view, filepath.Base(two)+"_") {
		t.Fatalf("existing directory is not visible while editing:\n%s", view)
	}
	m.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	m.Update(key('v', three))
	m.Update(key(tea.KeyEnter, ""))
	m.Update(key('r', "r"))
	m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	got, err := m.Result()
	if err != nil || !reflect.DeepEqual(got["scan_roots"], []string{one}) {
		t.Fatalf("saved directory edit/remove failed: %v, %v", got, err)
	}
}

func TestSplitDirectoryFormHasKeyboardAddRow(t *testing.T) {
	m, one, two, three := splitDirectoryForm(t)
	m.Update(key(tea.KeyDown, ""))
	m.Update(key(tea.KeyDown, ""))
	if m.rowIndex["scan_roots"] != 2 || !strings.Contains(ansi.Strip(m.View().Content), "Add directory") {
		t.Fatalf("Add row is not keyboard selectable:\n%s", ansi.Strip(m.View().Content))
	}
	m.Update(key('m', "m"))
	if !m.editing || m.editAction != "add" {
		t.Fatalf("manual path entry did not add from Add row: editing=%v action=%s", m.editing, m.editAction)
	}
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "Edit:") || !strings.Contains(view, "Enter finish") {
		t.Fatalf("manual path editor is invisible in the split form:\n%s", view)
	}
	m.Update(key('v', three))
	m.Update(key(tea.KeyEnter, ""))
	m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	got, err := m.Result()
	if err != nil || !reflect.DeepEqual(got["scan_roots"], []string{one, two, three}) {
		t.Fatalf("Add row did not append a directory: %v, %v", got, err)
	}
}

func TestSplitStringCollectionCanEditSavedHosts(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{{Name: "hosts", Label: "Git hosts", Type: "string", Multiple: true}}, map[string]any{"hosts": []string{"github.com", "git.example"}})
	m.SetSections(FormSection{Title: "Inputs", Fields: []string{"hosts"}})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	m.Update(key(tea.KeyRight, ""))
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "github.com") || !strings.Contains(view, "git.example") {
		t.Fatalf("short saved rows disappeared from the split form:\n%s", view)
	}
	m.Update(key(tea.KeyDown, ""))
	if m.rowIndex["hosts"] != 1 {
		t.Fatalf("second saved host is not keyboard selectable:\n%s", ansi.Strip(m.View().Content))
	}
	m.Update(key(tea.KeyEnter, ""))
	if !m.editing || m.editAction != "edit" || m.buffer != "git.example" {
		t.Fatalf("Enter did not edit selected host: editing=%v action=%s buffer=%q", m.editing, m.editAction, m.buffer)
	}
	m.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	m.Update(key('v', "git.company"))
	m.Update(key(tea.KeyEnter, ""))
	m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	got, err := m.Result()
	if err != nil || !reflect.DeepEqual(got["hosts"], []string{"github.com", "git.company"}) {
		t.Fatalf("saved hosts were not edited in place: %v, %v", got, err)
	}
}

func TestSplitDirectoryBrowseEditsSelectedRowAndAddOpensPicker(t *testing.T) {
	m, one, two, three := splitDirectoryForm(t)
	m.Update(key(tea.KeyRight, ""))
	m.Update(key(tea.KeyDown, ""))
	_, command := m.Update(key('b', "b"))
	if command == nil {
		t.Fatal("explicit Browse on an existing directory did not open the picker")
	}
	m.Update(pickedMsg{name: "scan_roots", action: "edit", index: 1, path: three})
	if got := m.editor.Values()["scan_roots"]; !reflect.DeepEqual(got, []string{one, three}) {
		t.Fatalf("picker did not replace selected row: %v", got)
	}
	m.Update(key(tea.KeyDown, ""))
	_, command = m.Update(key('a', "a"))
	if command == nil {
		t.Fatal("Add directory did not open the one-item picker")
	}
	m.Update(pickedMsg{name: "scan_roots", action: "add", path: two})
	if got := m.editor.Values()["scan_roots"]; !reflect.DeepEqual(got, []string{one, three, two}) {
		t.Fatalf("picker did not append exactly one directory: %v", got)
	}
}

func TestSplitDirectoryCanRemoveSavedRowByKeyboardOrMouse(t *testing.T) {
	m, one, two, _ := splitDirectoryForm(t)
	if view := ansi.Strip(m.View().Content); strings.Count(view, "[Remove]") != 2 {
		t.Fatalf("each saved directory needs a visible Remove action:\n%s", view)
	}
	m.Update(key(tea.KeyDown, ""))
	m.Update(key(tea.KeyDelete, ""))
	if got := m.editor.Values()["scan_roots"]; !reflect.DeepEqual(got, []string{one}) {
		t.Fatalf("Delete did not remove selected directory: %v", got)
	}
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "1 item") || strings.Contains(view, "1 items") {
		t.Fatalf("collection count is not singular after removing a directory:\n%s", view)
	}
	layout := m.layout()
	viewLines := strings.Split(ansi.Strip(m.View().Content), "\n")
	for y, line := range viewLines {
		if !strings.Contains(line, string(os.PathSeparator)+filepath.Base(one)) || !strings.Contains(line, "[Remove]") {
			continue
		}
		x := lipgloss.Width(line[:strings.Index(line, "[Remove]")])
		m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
		if got := m.editor.Values()["scan_roots"]; !reflect.DeepEqual(got, []string{}) {
			t.Fatalf("clicking Remove did not remove directory: %v", got)
		}
		return
	}
	t.Fatalf("saved row not visible in layout %+v; previous second row %s", layout, two)
}

func TestSplitDirectoryBackspaceRemovesFocusedRow(t *testing.T) {
	m, one, _, _ := splitDirectoryForm(t)
	m.Update(key(tea.KeyDown, ""))
	m.Update(key(tea.KeyBackspace, ""))
	if got := m.editor.Values()["scan_roots"]; !reflect.DeepEqual(got, []string{one}) {
		t.Fatalf("Backspace did not remove focused directory: %v", got)
	}
}

func TestSplitDirectoryBackspaceEditsTextWhileTyping(t *testing.T) {
	m, one, two, _ := splitDirectoryForm(t)
	m.Update(key('m', "m"))
	m.Update(key(tea.KeyBackspace, ""))
	if got := m.editor.Values()["scan_roots"]; !reflect.DeepEqual(got, []string{one, two}) {
		t.Fatalf("Backspace removed a directory while editing text: %v", got)
	}
	if !m.editing || m.buffer != one[:len(one)-1] {
		t.Fatalf("Backspace did not edit the path text: %q", m.buffer)
	}
}

func TestSplitLongPathEditKeepsCursorVisible(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, strings.Repeat("long-segment-", 10), "leaf")
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	m := NewForm(context.Background(), []catalog.Input{{Name: "scan_roots", Type: "directory", Multiple: true}}, map[string]any{"scan_roots": []string{path}})
	m.SetSections(FormSection{Title: "Inputs", Fields: []string{"scan_roots"}})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	m.Update(key(tea.KeyRight, ""))
	m.Update(key('m', "m"))
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "leaf_") || !strings.Contains(view, "…") {
		t.Fatalf("long path edit clipped the cursor or filename:\n%s", view)
	}
	m.Update(key(tea.KeyHome, ""))
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "Edit: _"+string([]rune(path)[0])) || !strings.Contains(view, "…") {
		t.Fatalf("moving to the start clipped the edit cursor:\n%s", view)
	}
}
