package forms

import (
	"context"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"path/filepath"
	"strings"
	"testing"
)

func TestRegexProvidedValues(t *testing.T) {
	pattern := `^[a-z][a-z0-9-]*$`
	def := catalog.Input{Name: "name", Type: "string", Regex: pattern}
	if err := ValidateProvided([]catalog.Input{def}, map[string]any{"name": "review-kit"}); err != nil {
		t.Fatal(err)
	}
	err := ValidateProvided([]catalog.Input{def}, map[string]any{"name": "Review Kit"})
	for _, want := range []string{"name", "Review Kit", pattern} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("validation must explain %q: %v", want, err)
		}
	}
	def.Multiple = true
	err = ValidateProvided([]catalog.Input{def}, map[string]any{"name": []string{"good", "Bad Value"}})
	if err == nil || !strings.Contains(err.Error(), "item 2") || !strings.Contains(err.Error(), "Bad Value") {
		t.Fatalf("collection error: %v", err)
	}
}

func TestRegexNormalizedValuesAndOptionalEmpty(t *testing.T) {
	for _, tc := range []struct {
		kind, pattern string
		value         any
	}{
		{"integer", `^12$`, "012"}, {"boolean", `^false$`, "false"}, {"string", "match", "contains-match-inside"}, {"string", `^nonempty$`, ""},
	} {
		d := catalog.Input{Name: "value", Type: tc.kind, Regex: tc.pattern}
		if _, err := Resolve([]catalog.Input{d}, map[string]any{"value": tc.value}); err != nil {
			t.Fatalf("%s %v: %v", tc.kind, tc.value, err)
		}
	}
	d := catalog.Input{Name: "value", Type: "string", Regex: `^good$`, Default: "bad"}
	if _, err := ResolvePartial([]catalog.Input{d}); err == nil {
		t.Fatal("invalid default accepted")
	}
}

func TestRegexExplicitHiddenSubmissionAndEditorDraft(t *testing.T) {
	defs := []catalog.Input{{Name: "enabled", Type: "boolean"}, {Name: "name", Type: "string", Regex: `^good$`, VisibleWhen: map[string]any{"enabled": true}}}
	if err := Validate(defs, map[string]any{"enabled": false, "name": "bad"}); err == nil {
		t.Fatal("explicit invalid hidden input accepted")
	}
	d := defs[1]
	d.VisibleWhen = nil
	e := NewEditor([]catalog.Input{d}, map[string]any{"name": "bad"})
	if e.Values()["name"] != "bad" {
		t.Fatalf("invalid prefill disappeared: %#v", e.Values())
	}
	if _, err := e.Commit(); err == nil {
		t.Fatal("invalid draft committed")
	}
	if err := e.Apply("name", "good"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Commit(); err != nil {
		t.Fatal(err)
	}
	m := NewForm(context.Background(), []catalog.Input{d}, map[string]any{"name": "bad"})
	if !strings.Contains(m.View().Content, "bad") {
		t.Fatal("invalid editable value not displayed")
	}
}

func TestRegexPickerAndCollectionAcceptance(t *testing.T) {
	d := catalog.Input{Name: "file", Type: "file", Regex: `\.yaml$`}
	e := NewEditor([]catalog.Input{d}, nil)
	bad := filepath.Join(t.TempDir(), "wrong.txt")
	if err := e.Apply("file", bad); err == nil || !strings.Contains(err.Error(), "wrong.txt") {
		t.Fatalf("picked invalid path accepted: %v", err)
	}
	d.Name = "files"
	d.Multiple = true
	c := NewCollection(d)
	if err := c.Add(bad); err == nil {
		t.Fatal("collection accepted invalid path")
	}
	if len(c.Values()) != 0 {
		t.Fatal("rejected path changed collection")
	}
}
