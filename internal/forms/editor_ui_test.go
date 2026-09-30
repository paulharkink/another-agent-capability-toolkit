package forms

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/picker"
	"reflect"
	"strings"
	"testing"
)

func key(code rune, text string) tea.KeyPressMsg { return tea.KeyPressMsg{Code: code, Text: text} }
func TestKeyboardFormEditsPrefillAndSaves(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{{Name: "team", Type: "string", Required: true}}, map[string]any{"team": "old"})
	m.Update(key(tea.KeyEnter, ""))
	m.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	m.Update(key('n', "new"))
	m.Update(key(tea.KeyEnter, ""))
	m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	got, e := m.Result()
	if e != nil || got["team"] != "new" {
		t.Fatalf("%v %v", got, e)
	}
}
func TestKeyboardFormMasksSecretsAndCancels(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{{Name: "token", Type: "secret"}}, map[string]any{"token": "very-private-test-value"})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if strings.Contains(m.View().Content, "very-private-test-value") {
		t.Fatal("secret rendered")
	}
	m.Update(key(tea.KeyEscape, ""))
	if _, e := m.Result(); e != picker.ErrCancelled {
		t.Fatal(e)
	}
}
func TestFormRequiredValidationKeepsFormOpen(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{{Name: "required", Type: "string", Required: true}}, nil)
	_, cmd := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if cmd != nil {
		t.Fatal("quit despite validation failure")
	}
	if !strings.Contains(m.View().Content, "required") {
		t.Fatalf("%s", m.View().Content)
	}
}
func TestMultipleChoiceKeyboardRemainsArray(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{{Name: "teams", Type: "multichoice", Options: []catalog.Choice{{Value: "alpha"}, {Value: "beta"}}}}, nil)
	m.Update(key(tea.KeySpace, " "))
	m.Update(key(tea.KeyRight, ""))
	m.Update(key(tea.KeySpace, " "))
	m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	got, e := m.Result()
	if e != nil || len(got["teams"].([]string)) != 2 {
		t.Fatalf("%v %v", got, e)
	}
}

func TestTypedCollectionsKeyboardAddEditRemove(t *testing.T) {
	for _, tc := range []struct {
		kind, first, second, replacement string
		want                             any
	}{
		{"string", "alpha", "beta", "gamma", []string{"gamma"}},
		{"integer", "1", "2", "3", []int64{3}},
		{"number", "1.5", "2.5", "3.5", []float64{3.5}},
		{"boolean", "true", "false", "false", []bool{false}},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			m := NewForm(context.Background(), []catalog.Input{{Name: "items", Type: tc.kind, Multiple: true, Required: true}}, nil)
			for i, v := range []string{tc.first, tc.second} {
				if i == 0 {
					m.Update(key(tea.KeyEnter, ""))
				} else {
					m.Update(key('a', "a"))
				}
				m.Update(key('v', v))
				m.Update(key(tea.KeyEnter, ""))
			}
			if !strings.Contains(m.View().Content, "r Remove") {
				t.Fatalf("collection controls missing: %s", m.View().Content)
			}
			m.Update(key(']', "]"))
			m.Update(key('r', "r"))
			m.Update(key('e', "e"))
			m.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
			m.Update(key('v', tc.replacement))
			m.Update(key(tea.KeyEnter, ""))
			m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
			got, err := m.Result()
			if err != nil || !reflect.DeepEqual(got["items"], tc.want) {
				t.Fatalf("got %v, err %v, want %v", got, err, tc.want)
			}
		})
	}
}
func TestTypedCollectionInvalidEditPreservesRows(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{{Name: "ports", Type: "integer", Multiple: true}}, map[string]any{"ports": []int64{8080}})
	m.Update(key('e', "e"))
	m.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	m.Update(key('x', "bad"))
	m.Update(key(tea.KeyEnter, ""))
	if !m.editing || !reflect.DeepEqual(m.editor.Values()["ports"], []int64{8080}) {
		t.Fatalf("invalid edit changed rows: %v", m.editor.Values())
	}
	m.Update(key(tea.KeyEscape, ""))
	m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if _, err := m.Result(); err != nil {
		t.Fatal(err)
	}
}
