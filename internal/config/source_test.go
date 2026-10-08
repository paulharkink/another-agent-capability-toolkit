package config

import (
	"os"
	"path/filepath"
	"testing"
)

func put(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}
func checkout(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	return root
}
func TestDiscoverNearestCheckoutConfig(t *testing.T) {
	root := checkout(t)
	put(t, filepath.Join(root, "aact.toml"), "schema_version=1\nsource_id='root'\n")
	child := filepath.Join(root, "child")
	put(t, filepath.Join(child, "aact.toml"), "schema_version=1\nsource_id='child'\n")
	s, e := Discover(filepath.Join(child, "deep"), "", "", t.TempDir())
	if e != nil || s.ID != "child" || s.Root != child {
		t.Fatalf("%#v %v", s, e)
	}
}
func TestExplicitConfigWins(t *testing.T) {
	root := checkout(t)
	put(t, filepath.Join(root, "aact.toml"), "schema_version=1\nsource_id='root'\n")
	other := filepath.Join(t.TempDir(), "explicit.toml")
	put(t, other, "schema_version=1\nsource_id='explicit'\n")
	s, e := Discover(root, other, "", t.TempDir())
	if e != nil || s.ID != "explicit" {
		t.Fatalf("%#v %v", s, e)
	}
}

func TestCapabilityPackDefaultsEnvironmentDirectoryInsidePack(t *testing.T) {
	root := checkout(t)
	put(t, filepath.Join(root, "aact.toml"), "schema_version=1\nsource_id='pack'\n")
	s, err := Discover(root, "", "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "environments"); s.EnvironmentRoot != want {
		t.Fatalf("default environment directory = %q, want %q", s.EnvironmentRoot, want)
	}
}

func TestCapabilityPackCanUseEnvironmentDirectoryOutsidePack(t *testing.T) {
	root := checkout(t)
	external := filepath.Join(t.TempDir(), "shared-environments")
	put(t, filepath.Join(root, "aact.toml"), "schema_version=1\nsource_id='pack'\n[environments]\nroot='"+external+"'\n")
	s, err := Discover(root, "", "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if s.EnvironmentRoot != external {
		t.Fatalf("configured external environment directory = %q, want %q", s.EnvironmentRoot, external)
	}
}

func TestBundledCatalogWithoutConfig(t *testing.T) {
	bundle := t.TempDir()
	put(t, filepath.Join(bundle, "public", "SKILL.md"), "# Public")
	s, e := Discover(t.TempDir(), "", bundle, t.TempDir())
	if e != nil || len(s.Catalog) != 1 || s.Catalog[0].ID != "public" {
		t.Fatalf("%#v %v", s, e)
	}
}
func TestMixedPublicAndPrivateCatalog(t *testing.T) {
	root := checkout(t)
	bundle := t.TempDir()
	put(t, filepath.Join(bundle, "public", "SKILL.md"), "# Public")
	put(t, filepath.Join(root, "skills", "private", "SKILL.md"), "# Private")
	put(t, filepath.Join(root, "aact.toml"), `schema_version=1
source_id="mixed"
[[catalog]]
id="public"
source="bundled:public"
[[catalog]]
id="private"
source="./skills/private"
[environments]
root="./environments"
[packages.private.inputs]
team="Platform"
`)
	s, e := Discover(root, "", bundle, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if len(s.Catalog) != 2 || s.Catalog[0].Dir != filepath.Join(bundle, "public") || s.Catalog[1].Dir != filepath.Join(root, "skills", "private") || s.EnvironmentRoot != filepath.Join(root, "environments") || s.PackageDefaults["private"]["team"] != "Platform" {
		t.Fatalf("%#v", s)
	}
}
func TestDiscoveryDoesNotCrossCheckoutBoundary(t *testing.T) {
	parent := t.TempDir()
	put(t, filepath.Join(parent, "aact.toml"), "schema_version=1\nsource_id='parent'\n")
	root := filepath.Join(parent, "nested")
	if e := os.MkdirAll(filepath.Join(root, ".git"), 0700); e != nil {
		t.Fatal(e)
	}
	s, e := Discover(root, "", "", t.TempDir())
	if e != nil || s.ID == "parent" {
		t.Fatalf("%#v %v", s, e)
	}
}
func TestRejectConflictingCatalogIDs(t *testing.T) {
	root := checkout(t)
	put(t, filepath.Join(root, "plain", "SKILL.md"), "# Plain")
	for _, body := range []string{`schema_version=1
source_id="../bad"`, `schema_version=1
[[catalog]]
id="other"
source="./plain"`, `schema_version=1
[[catalog]]
id="plain"
source="./plain"
[[catalog]]
id="plain"
source="./plain"`} {
		put(t, filepath.Join(root, "aact.toml"), body)
		if _, e := Discover(root, "", "", t.TempDir()); e == nil {
			t.Errorf("accepted %s", body)
		}
	}
}

func TestDiscoverPreviewDoesNotCreateLocalIdentity(t *testing.T) {
	root := checkout(t)
	put(t, filepath.Join(root, "aact.toml"), "schema_version=1\n")
	state := filepath.Join(t.TempDir(), "absent-state")
	first, e := DiscoverPreview(root, "", "", state)
	if e != nil {
		t.Fatal(e)
	}
	second, e := DiscoverPreview(root, "", "", state)
	if e != nil || first.ID != second.ID {
		t.Fatalf("%#v %#v %v", first, second, e)
	}
	if _, e = os.Stat(state); !os.IsNotExist(e) {
		t.Fatalf("preview wrote state: %v", e)
	}
	saved, e := Discover(root, "", "", state)
	if e != nil {
		t.Fatal(e)
	}
	preview, e := DiscoverPreview(root, "", "", state)
	if e != nil || preview.ID != saved.ID {
		t.Fatalf("%#v %#v %v", saved, preview, e)
	}
}
