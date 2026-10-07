package forms

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
)

func TestUXEnterEditsValueAtField(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{{Name: "host", Label: "Host", Type: "string"}}, map[string]any{"host": "old"})
	m.Update(key(tea.KeyEnter, ""))
	view := m.View().Content
	if !m.editing || !strings.Contains(view, "Host: Edit: old_") || strings.Contains(view, "\nEdit: old_") {
		t.Fatalf("edit is not shown in its field row: editing=%v\n%s", m.editing, view)
	}
	m.Update(key(tea.KeyEnd, ""))
	m.Update(key('!', "!"))
	m.Update(key(tea.KeyEnter, ""))
	if got := m.editor.Values()["host"]; got != "old!" {
		t.Fatalf("edited value = %#v, want old!", got)
	}
}

func TestUXPasteInPlace(t *testing.T) {
	want := "token ☃  with spaces\nopaque"
	m := NewForm(context.Background(), []catalog.Input{{Name: "token", Type: "secret"}}, nil)
	m.Update(key(tea.KeyEnter, ""))
	m.Update(tea.PasteStartMsg{})
	m.Update(tea.PasteMsg{Content: want})
	m.Update(tea.PasteEndMsg{})
	m.Update(key(tea.KeyEnter, ""))
	m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	got, err := m.Result()
	if err != nil || got["token"] != want {
		t.Fatalf("pasted value = %#v, err=%v; want exact pasted text", got, err)
	}
}

func TestUXCtrlSFlushesOnceAndInvalidFieldStaysEditable(t *testing.T) {
	minimum := 1024.0
	m := NewForm(context.Background(), []catalog.Input{{Name: "port", Label: "Port", Type: "integer", Min: &minimum}}, map[string]any{"port": int64(8080)})
	m.Update(key(tea.KeyEnter, ""))
	m.Update(key(tea.KeyHome, ""))
	m.Update(key(tea.KeyDelete, ""))
	m.Update(key(tea.KeyDelete, ""))
	m.Update(key(tea.KeyDelete, ""))
	m.Update(key(tea.KeyDelete, ""))
	m.Update(key('1', "1"))
	m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if m.done || !m.editing || !strings.Contains(m.View().Content, "Port must be at least 1024.") {
		t.Fatalf("invalid port did not stay editable with inline validation: done=%v editing=%v\n%s", m.done, m.editing, m.View().Content)
	}
	m.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	m.Update(key('1', "1"))
	m.Update(key('8', "8"))
	m.Update(key('7', "7"))
	m.Update(key('7', "7"))
	m.Update(key('0', "0"))
	m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	got, err := m.Result()
	if err != nil || got["port"] != int64(18770) {
		t.Fatalf("corrected port = %#v, err=%v", got, err)
	}
	m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	after, afterErr := m.Result()
	if afterErr != nil || after["port"] != got["port"] {
		t.Fatalf("a repeated save changed the submitted result: %#v, %v", after, afterErr)
	}
}

func TestUXEscapeCancelsOnlyCurrentEdit(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{{Name: "name", Type: "string"}}, map[string]any{"name": "before"})
	m.Update(key(tea.KeyEnter, ""))
	m.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	m.Update(key('x', "x"))
	m.Update(key(tea.KeyEscape, ""))
	if got := m.editor.Values()["name"]; got != "before" || m.done || m.editing {
		t.Fatalf("Escape should restore only the current value: got=%#v done=%v editing=%v", got, m.done, m.editing)
	}
}

func TestUXDirectoryEnterEditAndBackspaceRoles(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "one")
	if err := os.Mkdir(first, 0o700); err != nil {
		t.Fatal(err)
	}
	m := NewForm(context.Background(), []catalog.Input{{Name: "directories", Label: "Directories", Type: "directory", Multiple: true}}, nil)
	m.beginEdit("add", first)
	m.commitBuffer()
	m.rowIndex["directories"] = 0
	m.Update(key(tea.KeyEnter, ""))
	if !m.editing {
		t.Fatal("Enter did not edit the selected directory row")
	}
	m.Update(key(tea.KeyEnd, ""))
	m.Update(key(tea.KeyBackspace, ""))
	if got := len(collectionRows(m.editor.Values()["directories"])); got != 1 {
		t.Fatalf("editing Backspace removed row: %d rows", got)
	}
	m.Update(key(tea.KeyEnter, ""))
	m.Update(key(tea.KeyBackspace, ""))
	if got := len(collectionRows(m.editor.Values()["directories"])); got != 0 {
		t.Fatalf("focused-row Backspace left %d rows", got)
	}
}

func TestUXExclusiveAlternativeSwitchIsVisible(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source kubeconfig")
	m := NewForm(context.Background(), []catalog.Input{
		{Name: "token", Label: "Token", Type: "secret", ExclusiveGroup: "auth"},
		{Name: "source", Label: "Source kubeconfig", Type: "file", ExclusiveGroup: "auth"},
	}, map[string]any{"token": "secret-value"})
	m.SetExclusiveFields("token", "source")
	m.Update(key(tea.KeyDown, ""))
	m.Update(key(tea.KeyEnter, ""))
	if !strings.Contains(m.View().Content, "Source kubeconfig") || !strings.Contains(m.View().Content, "secret-value") {
		t.Fatalf("editing inactive alternative hid existing credential or label:\n%s", m.View().Content)
	}
	m.Update(tea.PasteMsg{Content: source})
	m.Update(key(tea.KeyEnter, ""))
	values := m.editor.Values()
	if values["source"] != source || values["token"] != "" {
		t.Fatalf("switching alternative did not clear prior value: %#v", values)
	}
}

func TestUXEmptyChoicesExplainOptionalInput(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{{Name: "db", Label: "Database", Type: "choice", OptionsFrom: "dbms.*.tenants.*"}}, nil)
	m.SetHint("db", "target TOML: /etc/aact/target.toml")
	m.Update(key(tea.KeyEnter, ""))
	view := strings.ToLower(m.View().Content)
	if m.editing || !strings.Contains(view, "optional") || !strings.Contains(view, "target toml") {
		t.Fatalf("empty options lack optional target-TOML explanation:\n%s", view)
	}
}
