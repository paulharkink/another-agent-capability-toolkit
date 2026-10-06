package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

// This verifies remembered-source behavior while the process working directory
// points at an unrelated checkout-like directory. The package, target TOML,
// installed content, and ledger identity must all come from the remembered root.
func TestUXJourney13RememberedSourcePreviewAndInstallIgnoreProcessWorkingDirectory(t *testing.T) {
	svc, _, store := fixture(t)
	root, packageDir := rememberedSkillSource(t, svc)
	t.Setenv("HOME", t.TempDir())
	manifest := `schema_version = 1
id = "demo"
name = "Remembered Demo"
[skill]
name = "demo"
[[inputs]]
name = "repository"
label = "Repository"
type = "directory"
`
	if err := os.WriteFile(filepath.Join(packageDir, "package.toml"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	targetPath := filepath.Join(root, "env", "prod", "demo", "blue.toml")
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o700); err != nil {
		t.Fatal(err)
	}
	targetTOML := "[inputs]\nrepository = '../source'\n"
	if err := os.WriteFile(targetPath, []byte(targetTOML), 0o600); err != nil {
		t.Fatal(err)
	}
	unrelated := t.TempDir()
	if err := os.MkdirAll(filepath.Join(unrelated, "demo"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unrelated, "demo", "SKILL.md"), []byte("wrong checkout"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(unrelated)
	request := viewmodel.SetupRequest{SourceID: "remembered", PackageID: "demo", Environment: "prod", Target: "blue"}
	preview, err := svc.UISetupPreview(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Key.Source != "remembered" || preview.SourceRoot != root || preview.TargetPath != targetPath || preview.TargetTOML != targetTOML {
		t.Fatalf("preview mixed process cwd with remembered source: key=%+v root=%q path=%q TOML=%q", preview.Key, preview.SourceRoot, preview.TargetPath, preview.TargetTOML)
	}
	if len(preview.Inputs) != 1 || preview.Inputs[0].Value != filepath.Join(root, "env", "prod", "source") {
		t.Fatalf("resolved value did not use remembered target TOML location: %+v", preview.Inputs)
	}
	result, err := svc.UIInstall(context.Background(), viewmodel.SetupInstallRequest{
		SetupRequest: request, DestinationIDs: []string{"all"}, Inputs: map[string]any{"repository": preview.Inputs[0].Value},
	})
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
		if row.Key.Source != "remembered" || row.Key.Environment != "prod" || row.Key.Target != "blue" {
			t.Fatalf("installation ledger identity came from another checkout/target: %+v", row)
		}
	}
	installedSkill := filepath.Join(os.Getenv("HOME"), ".agents", "skills", "demo", "SKILL.md")
	content, err := os.ReadFile(installedSkill)
	if err != nil || string(content) != "---\nname: demo\ndescription: demo\n---\nRemembered" {
		t.Fatalf("installed content did not come from remembered source: content=%q err=%v", content, err)
	}
}
