package integration_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/agents"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/app"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/install"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/state"
)

func TestMissingSharedSkillCanBeReinstalledWithDifferentContent(t *testing.T) {
	ctx := context.Background()
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	dest := install.SkillDestination{ID: "all", Home: home, Kind: "generic", SkillsDir: filepath.Join(home, ".agents", "skills")}
	skill := catalog.Skill{Name: "example"}
	pkg := catalog.Package{ID: "example", Dir: t.TempDir(), Skill: &skill}
	source := filepath.Join(pkg.Dir, "SKILL.md")
	if err := os.WriteFile(source, []byte("old content"), 0600); err != nil {
		t.Fatal(err)
	}
	installer := install.NewSkills(store)
	oldKey := state.Key{Source: "pack", Package: "example", Target: "old"}
	newKey := state.Key{Source: "pack", Package: "example", Target: "new"}
	if err := installer.Install(ctx, pkg, dest, oldKey, ""); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dest.SkillsDir, skill.Name)
	if err := os.WriteFile(source, []byte("new content"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := installer.Install(ctx, pkg, dest, newKey, ""); err == nil {
		t.Fatal("allowed different content while a shared destination exists")
	}
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}
	if err := installer.Install(ctx, pkg, dest, newKey, ""); err != nil {
		t.Fatalf("reinstall at missing destination: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(path, "SKILL.md"))
	if err != nil || string(b) != "new content" {
		t.Fatalf("installed content = %q, %v", b, err)
	}
	if err := installer.Uninstall(ctx, newKey, dest); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("uninstall left destination behind: %v", err)
	}
}

func TestMissingSkillRecoveryDefersRetiringOldContentUntilRecorded(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store, err := state.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	dest := install.SkillDestination{ID: "all", Home: home, Kind: "generic", SkillsDir: filepath.Join(home, ".agents", "skills")}
	skill := catalog.Skill{Name: "example"}
	pkg := catalog.Package{ID: "example", Dir: t.TempDir(), Skill: &skill}
	old := filepath.Join(root, "generated", "old", "output-1")
	newer := filepath.Join(root, "generated", "new", "output-2")
	for _, entry := range []struct{ dir, content string }{{old, "old"}, {newer, "new"}} {
		if err := os.MkdirAll(entry.dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(entry.dir, "SKILL.md"), []byte(entry.content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	oldKey := state.Key{Source: "pack", Package: "example", Target: "old"}
	newKey := state.Key{Source: "pack", Package: "example", Target: "new"}
	installer := install.NewSkills(store)
	if err := installer.Install(ctx, pkg, dest, oldKey, old); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dest.SkillsDir, skill.Name)
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}
	effect, err := installer.InstallOne(ctx, pkg, skill, dest, newKey, newer)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(old, "SKILL.md")); err != nil {
		t.Fatalf("old content removed before recording: %v", err)
	}
	if err := store.Record(effect); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("old generated content still exists: %v", err)
	}
	if err := installer.Uninstall(ctx, newKey, dest); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("uninstall left destination behind: %v", err)
	}
}

func TestMissingSkillRecoveryRollsBackWhenManagerCannotRecord(t *testing.T) {
	ctx := context.Background()
	sourceRoot, err := filepath.Abs("../../examples/capability-pack")
	if err != nil {
		t.Fatal(err)
	}
	root := copyCapabilityPackTree(t, sourceRoot)
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pack, err := config.DiscoverPack(root, filepath.Join(root, "aact.toml"), "", store.Root())
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	probe := agents.DiscoveryProbe{Home: home, GOOS: "linux", Getenv: func(string) string { return "" }, LookPath: func(string) (string, error) { return "fixture", nil }}
	svc := app.New(pack.LegacySource(), store, app.Options{DiscoveryProbe: &probe, AgentScopes: map[string]agents.Scope{"opencode": {ID: "opencode", Home: home, ExplicitHome: true}}})
	q := app.ProfileRequest{Ref: config.ProfileRef{PackID: pack.ID, CapabilityID: "company-guidance", Name: "ota"}, DestinationIDs: []string{"opencode"}, ItemIDs: []string{"skill:review"}}
	if _, err := svc.ApplyProfile(ctx, q); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(home, ".config", "opencode", "skills", "review")
	if err := os.RemoveAll(dest); err != nil {
		t.Fatal(err)
	}
	svc.Options.RecordInstallation = func(state.Installation) error { return fmt.Errorf("simulated record failure") }
	if _, err := svc.ApplyProfile(ctx, q); err == nil {
		t.Fatal("expected recording failure")
	}
	if _, err := os.Lstat(dest); !os.IsNotExist(err) {
		t.Fatalf("failed install left destination: %v", err)
	}
	svc.Options.RecordInstallation = nil
	if _, err := svc.ApplyProfile(ctx, q); err != nil {
		t.Fatalf("retry after recording failure: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "SKILL.md")); err != nil {
		t.Fatal(err)
	}
}
