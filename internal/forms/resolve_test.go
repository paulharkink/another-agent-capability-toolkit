package forms

import (
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"reflect"
	"testing"
)

func TestAnswerPrecedence(t *testing.T) {
	got, e := Resolve([]catalog.Input{{Name: "team", Type: "string"}}, map[string]any{"team": "default"}, map[string]any{"team": "saved"}, map[string]any{"team": "repo"}, map[string]any{"team": "cli"}, map[string]any{"team": "edited"})
	if e != nil || got["team"] != "edited" {
		t.Fatalf("%v %v", got, e)
	}
}
func TestBooleanAndArrayRemainTyped(t *testing.T) {
	defs := []catalog.Input{{Name: "enabled", Type: "boolean"}, {Name: "roots", Type: "directory", Multiple: true}}
	got, e := Resolve(defs, map[string]any{"enabled": true, "roots": []string{"/one", "/two"}, "secret": "unmapped"})
	if e != nil || got["enabled"] != true || !reflect.DeepEqual(got["roots"], []string{"/one", "/two"}) || got["secret"] != nil {
		t.Fatalf("%v %v", got, e)
	}
}
func TestConfigKeyAndInputsConflict(t *testing.T) {
	defs := []catalog.Input{{Name: "port", Type: "integer", ConfigKey: "mcp.local_port"}}
	_, e := Resolve(defs, map[string]any{"inputs": map[string]any{"port": int64(8888)}, "mcp": map[string]any{"local_port": int64(8765)}})
	if e == nil {
		t.Fatal("accepted conflicting definitions")
	}
	got, e := Resolve(defs, map[string]any{"mcp": map[string]any{"local_port": int64(8765)}, "cluster": map[string]any{"secret": "private"}})
	if e != nil || got["port"] != int64(8765) || len(got) != 1 {
		t.Fatalf("%v %v", got, e)
	}
}
func TestManifestDefaultsAndCLIScalars(t *testing.T) {
	got, e := Resolve([]catalog.Input{{Name: "port", Type: "integer", Default: int64(8765)}, {Name: "on", Type: "boolean"}}, map[string]any{"on": "false"})
	if e != nil || got["port"] != int64(8765) || got["on"] != false {
		t.Fatalf("%v %v", got, e)
	}
}
