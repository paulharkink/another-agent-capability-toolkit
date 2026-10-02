package config

import (
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPathsResolveAgainstDeclaringFile(t *testing.T) {
	root := t.TempDir()
	declared := filepath.Join(root, "targets", "default.toml")
	path, e := ResolvePath("../repos", declared)
	if e != nil || path != filepath.Join(root, "repos") {
		t.Fatalf("%s %v", path, e)
	}
	home, e := os.UserHomeDir()
	if e != nil {
		t.Fatal(e)
	}
	path, e = ResolvePath("~/repos", declared)
	if e != nil || path != filepath.Join(home, "repos") {
		t.Fatalf("%s %v", path, e)
	}
	defs := []catalog.Input{{Name: "roots", Type: "directory", Multiple: true}, {Name: "team", Type: "string"}, {Name: "file", Type: "file", ConfigKey: "legacy.file"}}
	got, e := ResolveInputPaths(defs, map[string]any{"roots": []string{"../one", "../two"}, "team": "../unchanged", "legacy": map[string]any{"file": "relative.txt"}}, declared)
	if e != nil || !reflect.DeepEqual(got["roots"], []string{filepath.Join(root, "one"), filepath.Join(root, "two")}) || got["team"] != "../unchanged" || got["legacy"].(map[string]any)["file"] != filepath.Join(root, "targets", "relative.txt") {
		t.Fatalf("%v %v", got, e)
	}
}
func TestEnvironmentRootPrecedence(t *testing.T) {
	root := checkout(t)
	state := t.TempDir()
	legacy := filepath.Join(t.TempDir(), "legacy")
	put(t, filepath.Join(state, "config-root"), legacy+"\n")
	put(t, filepath.Join(root, "aact.toml"), "schema_version=1\nsource_id='root'\n")
	s, e := Discover(root, "", "", state)
	if e != nil || s.EnvironmentRoot != legacy {
		t.Fatalf("%#v %v", s, e)
	}
	put(t, filepath.Join(root, "aact.toml"), "schema_version=1\nsource_id='root'\n[environments]\nroot='./repo'\n")
	s, e = Discover(root, "", "", state)
	if e != nil || s.EnvironmentRoot != filepath.Join(root, "repo") {
		t.Fatalf("%#v %v", s, e)
	}
	s, e = WithEnvironmentRoot(s, "./cli")
	if e != nil || s.EnvironmentRoot != filepath.Join(root, "cli") {
		t.Fatalf("%#v %v", s, e)
	}
}
