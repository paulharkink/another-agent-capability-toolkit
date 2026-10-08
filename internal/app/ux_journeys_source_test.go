package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// This verifies remembered-source behavior while the process working directory
// points at an unrelated checkout-like directory. The package, profile TOML,
// installed content, and ledger identity must all come from the remembered root.
func TestUXJourney13RememberedSourcePreviewAndInstallIgnoreProcessWorkingDirectory(t *testing.T) {
	svc, _, store := fixture(t)
	root, packageDir := rememberedSkillSource(t, svc)
	home := t.TempDir()
	isolateUXUserHome(t, home)
	manifest := `schema_version = 1
id = "demo"
name = "Remembered Demo"
[skill]
name = "demo"
source = "./"
[[inputs]]
name = "repository"
label = "Repository"
type = "directory"
`
	if err := os.WriteFile(filepath.Join(packageDir, "package.toml"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	remembered, err := svc.forSource("remembered")
	if err != nil {
		t.Fatal(err)
	}
	profileTOML := "[inputs]\nrepository = '../source'\n"
	ref := writeProfileForTest(t, remembered, "demo", "blue", profileTOML)
	unrelated := t.TempDir()
	if err := os.MkdirAll(filepath.Join(unrelated, "demo"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unrelated, "demo", "SKILL.md"), []byte("wrong checkout"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(unrelated)
	preview, err := remembered.PreviewProfile(context.Background(), ProfileRequest{Ref: ref})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Key.Source != "remembered" || preview.PackRoot != root || preview.ProfilePath != filepath.Join(root, "environments", "demo", "blue.toml") || preview.ProfileTOML != profileTOML {
		t.Fatalf("preview mixed process cwd with remembered source: key=%+v root=%q path=%q TOML=%q", preview.Key, preview.PackRoot, preview.ProfilePath, preview.ProfileTOML)
	}
	if len(preview.Inputs) != 1 || preview.Inputs[0].Value != filepath.Join(root, "environments", "source") {
		t.Fatalf("resolved value did not use remembered profile TOML location: %+v", preview.Inputs)
	}
	result, err := remembered.ApplyProfile(context.Background(), ProfileRequest{Ref: ref, DestinationIDs: []string{"all"}, Inputs: map[string]any{"repository": preview.Inputs[0].Value}})
	if err != nil || !result.Saved {
		t.Fatalf("remembered source install failed: result=%+v err=%v", result, err)
	}
	rows, err := store.Installations()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 {
		t.Fatal("remembered source install created no installation ledger row")
	}
	for _, row := range rows {
		if row.Key.Source != "remembered" || row.Key.Environment != "" || row.Key.Target != "blue" {
			t.Fatalf("installation ledger identity came from another checkout/profile: %+v", row)
		}
	}
	installedSkill := filepath.Join(home, ".agents", "skills", "demo", "SKILL.md")
	content, err := os.ReadFile(installedSkill)
	if err != nil || string(content) != "---\nname: demo\ndescription: demo\n---\nRemembered" {
		t.Fatalf("installed content did not come from remembered source: content=%q err=%v", content, err)
	}
}
