package config

import (
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProfileDiscoveryIncludesInvalidRowsWithoutMutatingFiles(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "company-handoff")
	text := "[inputs]\nenabled = false\ncertificate = './cert.pem'\n[aact.input_policy]\nenabled = 'fixed'\n"
	packFile(t, filepath.Join(dir, "ota.toml"), text)
	packFile(t, filepath.Join(dir, "prod.toml"), "not valid TOML [")
	p := Pack{ID: "company", ProfileRoot: root, Catalog: []catalog.Package{{ID: "company-handoff", Inputs: []catalog.Input{{Name: "enabled", Type: "boolean"}, {Name: "certificate", Type: "file"}}}}}
	profiles, err := DiscoverProfiles(p, "company-handoff")
	if err != nil || len(profiles) != 2 {
		t.Fatalf("profiles: %#v, %v", profiles, err)
	}
	if profiles[0].Ref.Name != "ota" || profiles[1].Error == "" {
		t.Fatalf("valid/invalid profile rows: %#v", profiles)
	}
	loaded, err := LoadProfile(p, "company-handoff", "ota")
	if err != nil {
		t.Fatal(err)
	}
	inputs := loaded.Raw["inputs"].(map[string]any)
	if inputs["enabled"] != false || inputs["certificate"] != filepath.Join(dir, "cert.pem") || loaded.InputPolicy["enabled"] != "fixed" {
		t.Fatalf("values/origin/policy: %#v", loaded)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "ota.toml")); err != nil || string(data) != text {
		t.Fatal("profile TOML changed")
	}
	if _, err := LoadProfile(p, "company-handoff", "prod"); err == nil {
		t.Fatal("invalid profile loaded without error")
	}
}

func TestProfileDiscoveryEmptyUnknownAndEscapingPaths(t *testing.T) {
	root := t.TempDir()
	p := Pack{ID: "company", ProfileRoot: root, Catalog: []catalog.Package{{ID: "guidance"}}}
	profiles, err := DiscoverProfiles(p, "guidance")
	if err != nil || len(profiles) != 0 {
		t.Fatalf("empty profiles: %#v %v", profiles, err)
	}
	if _, err := LoadProfile(p, "guidance", "../escape"); err == nil {
		t.Fatal("unsafe profile name accepted")
	}
	if _, err := DiscoverProfiles(p, "../escape"); err == nil {
		t.Fatal("unsafe capability path accepted")
	}
	outside := filepath.Join(t.TempDir(), "outside.toml")
	packFile(t, outside, "[inputs]\nvalue = 'outside'\n")
	if err := os.MkdirAll(filepath.Join(root, "guidance"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "guidance", "escape.toml")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := LoadProfile(p, "guidance", "escape"); err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("profile symlink escaped: %v", err)
	}
}

func TestProfileRejectsUnknownPolicyAndUndeclaredPolicyInput(t *testing.T) {
	for _, policy := range []string{"[aact.input_policy]\nvalue='hidden'\n", "[aact.input_policy]\nunknown='fixed'\n"} {
		root := t.TempDir()
		packFile(t, filepath.Join(root, "guidance", "default.toml"), policy)
		p := Pack{ID: "company", ProfileRoot: root, Catalog: []catalog.Package{{ID: "guidance", Inputs: []catalog.Input{{Name: "value", Type: "string"}}}}}
		if _, err := LoadProfile(p, "guidance", "default"); err == nil {
			t.Fatalf("bad profile policy accepted: %s", policy)
		}
	}
}
