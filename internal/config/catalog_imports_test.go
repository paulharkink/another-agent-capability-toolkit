package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func packFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}
func packSkill(t *testing.T, root, id string) {
	t.Helper()
	packFile(t, filepath.Join(root, "package.toml"), "schema_version = 1\nid = \""+id+"\"\nname = \"Skill\"\n[skill]\nname = \""+id+"\"\n")
}

func TestPackCombinesLocalBundledAndImportedCatalog(t *testing.T) {
	root := t.TempDir()
	vendor := filepath.Join(root, "vendor", "community")
	bundle := t.TempDir()
	packSkill(t, filepath.Join(vendor, "capabilities", "handoff"), "handoff-kit")
	packSkill(t, filepath.Join(vendor, "capabilities", "reference"), "reference-kit")
	packSkill(t, filepath.Join(root, "capabilities", "guidance"), "guidance")
	packSkill(t, filepath.Join(bundle, "repository-tools"), "repository-tools")
	packFile(t, filepath.Join(vendor, "aact.toml"), `schema_version = 1
pack_id = "external"
[environments]
root = "./external-profiles"
[packages.handoff-kit.inputs]
company = "external-default"
[[catalog]]
source = "./capabilities/handoff"
[[catalog]]
source = "./capabilities/reference"
`)
	packFile(t, filepath.Join(root, "aact.toml"), `schema_version = 1
pack_id = "company-pack"
[environments]
root = "./profiles"
[packages.company-handoff.inputs]
company = "local-default"
[[imports]]
catalog = "./vendor/community/aact.toml"
include = ["handoff-kit", "reference-kit"]
[imports.overrides.handoff-kit]
id = "company-handoff"
[[catalog]]
source = "bundled:repository-tools"
[[catalog]]
id = "local-guidance"
source = "./capabilities/guidance"
`)
	p, err := DiscoverPack(root, "", bundle, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != "company-pack" || p.ProfileRoot != filepath.Join(root, "profiles") || len(p.Catalog) != 4 {
		t.Fatalf("pack composition: %#v", p)
	}
	if p.Catalog[0].ID != "company-handoff" || p.Catalog[0].ManifestID != "handoff-kit" || p.Catalog[0].Dir != filepath.Join(vendor, "capabilities", "handoff") {
		t.Fatalf("import lost configured identity or definition root: %#v", p.Catalog[0])
	}
	if p.Catalog[1].ID != "reference-kit" || p.Catalog[2].ID != "repository-tools" || p.Catalog[3].ID != "local-guidance" {
		t.Fatalf("configured names/order: %#v", p.Catalog)
	}
	if p.PackageDefaults["company-handoff"]["company"] != "local-default" || p.PackageDefaults["handoff-kit"] != nil {
		t.Fatal("imported environment defaults escaped into company pack")
	}
}

func TestCatalogImportCyclesMissingPathsAndUnknownSelection(t *testing.T) {
	for _, tc := range []struct{ name, fragment, want string }{
		{"cycle", "[[imports]]\ncatalog = \"./aact.toml\"\n", "cycle"},
		{"missing", "[[imports]]\ncatalog = \"./not-initialized/aact.toml\"\n", "not-initialized"},
		{"unknown-include", "[[imports]]\ncatalog = \"./empty.toml\"\ninclude = [\"missing\"]\n", "missing"},
		{"unknown-override", "[[imports]]\ncatalog = \"./empty.toml\"\n[imports.overrides.missing]\nid = \"renamed\"\n", "missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			packFile(t, filepath.Join(root, "aact.toml"), "schema_version = 1\npack_id = \"pack\"\n"+tc.fragment)
			packFile(t, filepath.Join(root, "empty.toml"), "schema_version = 1\n")
			_, err := DiscoverPack(root, "", "", t.TempDir())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("wanted %s, got %v", tc.want, err)
			}
		})
	}
}

func TestConfiguredNameCollisionsAndPackIdentityAliases(t *testing.T) {
	root := t.TempDir()
	packSkill(t, filepath.Join(root, "one"), "shared")
	packSkill(t, filepath.Join(root, "two"), "shared")
	path := filepath.Join(root, "aact.toml")
	packFile(t, path, "schema_version = 1\npack_id = \"pack\"\nsource_id = \"other\"\n")
	if _, err := DiscoverPack(root, "", "", t.TempDir()); err == nil {
		t.Fatal("conflicting pack/source IDs accepted")
	}
	packFile(t, path, "schema_version = 1\nsource_id = \"pack\"\n[[catalog]]\nid = \"first\"\nsource = \"./one\"\n[[catalog]]\nid = \"second\"\nsource = \"./two\"\n")
	p, err := DiscoverPack(root, "", "", t.TempDir())
	if err != nil || len(p.Catalog) != 2 || p.ID != "pack" {
		t.Fatalf("author aliases not honored: %#v, %v", p, err)
	}
	packFile(t, filepath.Join(root, "vendor.toml"), "schema_version = 1\n[[catalog]]\nsource = \"./one\"\n")
	packFile(t, path, "schema_version = 1\npack_id = \"pack\"\n[[imports]]\ncatalog = \"./vendor.toml\"\n[[catalog]]\nsource = \"./two\"\n")
	_, err = DiscoverPack(root, "", "", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "vendor.toml") || !strings.Contains(err.Error(), "aact.toml") {
		t.Fatalf("duplicate error lacks both declarations: %v", err)
	}
}
