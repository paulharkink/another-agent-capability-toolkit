package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNamedTargetRetainsRawTables(t *testing.T) {
	root := t.TempDir()
	put(t, filepath.Join(root, "dev", "inspect", "production.toml"), "[cluster]\napi_server='https://cluster.example'\n[dbms]\nport=5432\n")
	target, e := LoadTarget(Source{EnvironmentRoot: root}, "inspect", "dev", "production")
	if e != nil || target.Raw["cluster"].(map[string]any)["api_server"] != "https://cluster.example" || target.Raw["dbms"].(map[string]any)["port"] != int64(5432) {
		t.Fatalf("%#v %v", target, e)
	}
}
func TestTargetPathsCannotEscapeRoot(t *testing.T) {
	root := t.TempDir()
	for _, args := range [][3]string{{"../inspect", "dev", "target"}, {"inspect", "../dev", "target"}, {"inspect", "dev", "../target"}} {
		if _, e := LoadTarget(Source{EnvironmentRoot: root}, args[0], args[1], args[2]); e == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	external := t.TempDir()
	put(t, filepath.Join(external, "target.toml"), "[inputs]\nx=1\n")
	if e := os.MkdirAll(filepath.Join(root, "dev"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(external, filepath.Join(root, "dev", "inspect")); e != nil {
		t.Fatal(e)
	}
	if _, e := LoadTarget(Source{EnvironmentRoot: root}, "inspect", "dev", "target"); e == nil {
		t.Fatal("accepted escaped symlink")
	}
}
func TestMissingTargetDoesNotFallback(t *testing.T) {
	root := t.TempDir()
	put(t, filepath.Join(root, "dev", "inspect", "default.toml"), "[inputs]\nx=1")
	if _, e := LoadTarget(Source{EnvironmentRoot: root}, "inspect", "dev", "missing"); e == nil {
		t.Fatal("selected another target")
	}
}
