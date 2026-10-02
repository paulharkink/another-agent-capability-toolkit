package forms

import (
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"strings"
	"testing"
)

func TestMissingRequiredIsActionable(t *testing.T) {
	e := Validate([]catalog.Input{{Name: "api_server", Type: "string", Required: true}}, nil)
	if e == nil || !strings.Contains(e.Error(), "api_server") || !strings.Contains(e.Error(), "required") {
		t.Fatalf("%v", e)
	}
}
func TestIntegerRejectsBoolean(t *testing.T) {
	if e := Validate([]catalog.Input{{Name: "port", Type: "integer"}}, map[string]any{"port": true}); e == nil {
		t.Fatal("accepted bool integer")
	}
}
func TestTypedBoundsAndChoices(t *testing.T) {
	min, max := float64(1024), float64(65535)
	one, two := 1, 2
	defs := []catalog.Input{{Name: "port", Type: "integer", Min: &min, Max: &max}, {Name: "roots", Type: "directory", Multiple: true, MinItems: &one, MaxItems: &two}, {Name: "kind", Type: "choice", Options: []catalog.Choice{{Value: "a"}, {Value: "b"}}}}
	for _, values := range []map[string]any{{"port": int64(1)}, {"port": 1.5}, {"roots": []string{}}, {"roots": []string{"one", "two", "three"}}, {"roots": "one,two"}, {"kind": "c"}} {
		if e := Validate(defs, values); e == nil {
			t.Errorf("accepted %#v", values)
		}
	}
	if e := Validate(defs, map[string]any{"port": int64(8765), "roots": []string{"one"}, "kind": "b"}); e != nil {
		t.Fatal(e)
	}
}
func TestOptionalEmptyChoiceAndUnsupportedTypes(t *testing.T) {
	if e := Validate([]catalog.Input{{Name: "optional", Type: "choice"}}, map[string]any{"optional": ""}); e != nil {
		t.Fatal(e)
	}
	if e := Validate([]catalog.Input{{Name: "x", Type: "imaginary"}}, map[string]any{"x": "ok"}); e == nil {
		t.Fatal("accepted unknown type")
	}
}

func TestSecretAndMultipleChoiceValues(t *testing.T) {
	for _, kind := range []string{"multichoice", "multiple-choice"} {
		defs := []catalog.Input{{Name: "token", Type: "secret", Required: true}, {Name: "teams", Type: kind, Required: true, Options: []catalog.Choice{{Value: "alpha"}, {Value: "beta"}}}}
		got, e := Resolve(defs, map[string]any{"token": "private-test-value", "teams": []any{"alpha", "beta"}})
		if e != nil {
			t.Fatal(e)
		}
		if got["token"] != "private-test-value" || len(got["teams"].([]string)) != 2 {
			t.Fatalf("%v", got)
		}
		if e = Validate(defs, map[string]any{"token": "private-test-value", "teams": []string{"wrong"}}); e == nil {
			t.Fatal("accepted invalid choice")
		}
		if e = Validate(defs, map[string]any{"token": "", "teams": []string{"alpha"}}); e == nil {
			t.Fatal("accepted missing secret")
		}
	}
}

func TestDirectoryCollectionRejectsEmptyItems(t *testing.T) {
	defs := []catalog.Input{{Name: "roots", Type: "directory", Multiple: true, Required: true}}
	if e := Validate(defs, map[string]any{"roots": []string{""}}); e == nil {
		t.Fatal("accepted empty directory item")
	}
	if _, e := Resolve(defs, map[string]any{"roots": []string{"  "}}); e == nil {
		t.Fatal("accepted blank directory item")
	}
}

func TestExclusiveGroupRejectsTwoCredentialMethods(t *testing.T) {
	defs := []catalog.Input{{Name: "token", Type: "secret", ExclusiveGroup: "cluster_credentials"}, {Name: "kubeconfig", Type: "file", ExclusiveGroup: "cluster_credentials"}}
	if err := Validate(defs, map[string]any{"token": "private", "kubeconfig": "/tmp/config"}); err == nil || !strings.Contains(err.Error(), "token") || !strings.Contains(err.Error(), "kubeconfig") {
		t.Fatalf("simultaneous credentials accepted or error unclear: %v", err)
	}
	for _, values := range []map[string]any{{"token": "private", "kubeconfig": ""}, {"token": "", "kubeconfig": "/tmp/config"}} {
		if err := Validate(defs, values); err != nil {
			t.Fatalf("single credential method rejected: %v", err)
		}
	}
}
