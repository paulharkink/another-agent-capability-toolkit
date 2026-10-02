package forms

import (
	"context"
	"errors"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/picker"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestNativeCancelDoesNotChangeValues(t *testing.T) {
	defs := []catalog.Input{{Name: "roots", Type: "directory", Multiple: true}}
	editor := NewEditor(defs, map[string]any{"roots": []string{"/initial"}})
	p := picker.Picker{Native: func(context.Context, string, string) (string, error) { return "", picker.ErrCancelled }}
	path, e := p.Select(context.Background(), "directory", t.TempDir())
	if !errors.Is(e, picker.ErrCancelled) {
		t.Fatal(e)
	}
	if e == nil {
		if e = editor.AddPath("roots", path); e != nil {
			t.Fatal(e)
		}
	}
	if !reflect.DeepEqual(editor.Values()["roots"], []string{"/initial"}) {
		t.Fatalf("%v", editor.Values())
	}
}
func TestOneSelectionPerAdd(t *testing.T) {
	count := 0
	path := t.TempDir()
	p := picker.Picker{Native: func(context.Context, string, string) (string, error) { count++; return path, nil }}
	editor := NewEditor([]catalog.Input{{Name: "roots", Type: "directory", Multiple: true}}, nil)
	selected, e := p.Select(context.Background(), "directory", path)
	if e != nil {
		t.Fatal(e)
	}
	if e = editor.AddPath("roots", selected); e != nil {
		t.Fatal(e)
	}
	if count != 1 || !reflect.DeepEqual(editor.Values()["roots"], []string{path}) {
		t.Fatalf("count %d values %v", count, editor.Values())
	}
}
func TestPrefilledCollectionEditRemove(t *testing.T) {
	one, two, three := t.TempDir(), t.TempDir(), t.TempDir()
	editor := NewEditor([]catalog.Input{{Name: "roots", Type: "directory", Multiple: true}}, map[string]any{"roots": []string{one, two}})
	if e := editor.EditPath("roots", 0, three); e != nil {
		t.Fatal(e)
	}
	if e := editor.RemovePath("roots", 1); e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(editor.Values()["roots"], []string{three}) {
		t.Fatalf("%v", editor.Values())
	}
}
func TestPrefilledValuesEditable(t *testing.T) {
	editor := NewEditor([]catalog.Input{{Name: "team", Type: "string", Required: true}, {Name: "port", Type: "integer"}}, map[string]any{"team": "initial", "port": int64(8765)})
	if e := editor.Apply("team", "edited"); e != nil {
		t.Fatal(e)
	}
	if e := editor.Apply("port", "9000"); e != nil {
		t.Fatal(e)
	}
	got, e := editor.Commit()
	if e != nil || got["team"] != "edited" || got["port"] != int64(9000) {
		t.Fatalf("%v %v", got, e)
	}
	editor.Cancel()
	if editor.Values()["team"] != "initial" {
		t.Fatalf("%v", editor.Values())
	}
	if _, e := editor.Commit(); !errors.Is(e, picker.ErrCancelled) {
		t.Fatal(e)
	}
}
func TestSelectingOtherCredentialClearsInactiveValue(t *testing.T) {
	defs := []catalog.Input{{Name: "token", Type: "secret", ExclusiveGroup: "cluster_credentials"}, {Name: "kubeconfig", Type: "file", ExclusiveGroup: "cluster_credentials"}}
	editor := NewEditor(defs, map[string]any{"kubeconfig": "/tmp/config"})
	if err := editor.Apply("token", "new-token"); err != nil {
		t.Fatal(err)
	}
	values, err := editor.Commit()
	if err != nil || values["token"] != "new-token" || values["kubeconfig"] != "" {
		t.Fatalf("inactive kubeconfig was not cleared for target override: %#v, %v", values, err)
	}
}
func TestPartialResolutionAllowsIncompletePrefill(t *testing.T) {
	defs := []catalog.Input{{Name: "required", Type: "string", Required: true}}
	if _, e := ResolvePartial(defs, nil); e != nil {
		t.Fatal(e)
	}
	editor := NewEditor(defs, nil)
	if _, e := editor.Commit(); e == nil {
		t.Fatal("committed missing required field")
	}
	if e := editor.Apply("undeclared", "private"); e == nil {
		t.Fatal("accepted undeclared field")
	}
}

func TestManualPathEntryNormalizesAgainstWorkingDirectory(t *testing.T) {
	cwd, e := os.Getwd()
	if e != nil {
		t.Fatal(e)
	}
	editor := NewEditor([]catalog.Input{{Name: "file", Type: "file"}, {Name: "roots", Type: "directory", Multiple: true}}, nil)
	if e = editor.Apply("file", "./relative.txt"); e != nil {
		t.Fatal(e)
	}
	if editor.Values()["file"] != filepath.Join(cwd, "relative.txt") {
		t.Fatalf("%v", editor.Values())
	}
	if e = editor.AddPath("roots", "./one"); e != nil {
		t.Fatal(e)
	}
	if e = editor.AddPath("roots", filepath.Join(cwd, "one")); e == nil {
		t.Fatal("accepted relative/absolute duplicate")
	}
}
