package install

import (
	"context"
	"errors"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"os"
	"path/filepath"
	"testing"
)

func setup(t *testing.T) (*Skills, *state.Store, catalog.Package, SkillDestination, state.Key) {
	t.Helper()
	s, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := catalog.Package{ID: "sample", Dir: t.TempDir(), Skill: &catalog.Skill{Name: "sample"}}
	os.WriteFile(filepath.Join(p.Dir, "SKILL.md"), []byte("skill"), 0644)
	e := SkillDestination{ID: "agent", Kind: "codex", Home: t.TempDir(), SkillsDir: t.TempDir()}
	k := state.Key{Source: "source", Package: "sample", Environment: "env", Target: "one"}
	return NewSkills(s), s, p, e, k
}
func TestStaticSkillLinksToSource(t *testing.T) {
	i, s, p, e, k := setup(t)
	if err := i.Install(context.Background(), p, e, k, ""); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(e.SkillsDir, "sample")
	path, err := os.Readlink(dest)
	if err != nil || path != p.Dir {
		t.Fatalf("%s %v", path, err)
	}
	rows, _ := s.Installations()
	if len(rows) != 1 || rows[0].Mode != "symlink" {
		t.Fatalf("%#v", rows)
	}
}
func TestGeneratedSkillLinksToManagedOutput(t *testing.T) {
	i, s, p, e, k := setup(t)
	generated := s.GeneratedDir(k)
	os.MkdirAll(generated, 0755)
	os.WriteFile(filepath.Join(generated, "SKILL.md"), []byte("rendered"), 0644)
	if err := i.Install(context.Background(), p, e, k, generated); err != nil {
		t.Fatal(err)
	}
	path, _ := os.Readlink(filepath.Join(e.SkillsDir, "sample"))
	if path != generated {
		t.Fatalf("got %q", path)
	}
}
func TestUnownedDirectoryAndForeignLinkPreserved(t *testing.T) {
	for _, link := range []bool{false, true} {
		i, s, p, e, k := setup(t)
		dest := filepath.Join(e.SkillsDir, "sample")
		if link {
			os.Symlink(p.Dir, dest)
		} else {
			os.Mkdir(dest, 0755)
		}
		if err := i.Install(context.Background(), p, e, k, ""); err == nil {
			t.Fatal("foreign content replaced")
		}
		if _, err := os.Lstat(dest); err != nil {
			t.Fatal("foreign content lost")
		}
		rows, _ := s.Installations()
		if len(rows) != 0 {
			t.Fatal("foreign content recorded")
		}
	}
}
func TestSharedCompanionReference(t *testing.T) {
	i, s, p, e, k := setup(t)
	if err := i.Install(context.Background(), p, e, k, ""); err != nil {
		t.Fatal(err)
	}
	second := k
	second.Target = "two"
	if err := i.Install(context.Background(), p, e, second, ""); err != nil {
		t.Fatal(err)
	}
	if err := i.Uninstall(context.Background(), k, e); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.SkillsDir, "sample", "SKILL.md")); err != nil {
		t.Fatal("shared skill removed")
	}
	if err := i.Uninstall(context.Background(), second, e); err != nil {
		t.Fatal(err)
	}
	rows, _ := s.Installations()
	if len(rows) != 0 {
		t.Fatalf("%v", rows)
	}
}
func TestIdenticalRenderedCompanionsSharedAcrossTargets(t *testing.T) {
	i, s, p, e, k := setup(t)
	second := k
	second.Target = "two"
	a, b := s.GeneratedDir(k), s.GeneratedDir(second)
	for _, d := range []string{a, b} {
		os.MkdirAll(d, 0755)
		os.WriteFile(filepath.Join(d, "SKILL.md"), []byte("same"), 0644)
	}
	if err := i.Install(context.Background(), p, e, k, a); err != nil {
		t.Fatal(err)
	}
	if err := i.Install(context.Background(), p, e, second, b); err != nil {
		t.Fatal(err)
	}
	if err := i.Uninstall(context.Background(), k, e); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(a); err != nil {
		t.Fatal("shared source removed")
	}
	if err := i.Uninstall(context.Background(), second, e); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(a); !os.IsNotExist(err) {
		t.Fatal("last owned generated source retained")
	}
}
func TestDifferentRenderedContentAtSameNameRefused(t *testing.T) {
	i, s, p, e, k := setup(t)
	second := k
	second.Target = "two"
	a, b := s.GeneratedDir(k), s.GeneratedDir(second)
	os.MkdirAll(a, 0755)
	os.MkdirAll(b, 0755)
	os.WriteFile(filepath.Join(a, "SKILL.md"), []byte("one"), 0644)
	os.WriteFile(filepath.Join(b, "SKILL.md"), []byte("two"), 0644)
	if err := i.Install(context.Background(), p, e, k, a); err != nil {
		t.Fatal(err)
	}
	if err := i.Install(context.Background(), p, e, second, b); err == nil {
		t.Fatal("different target overwritten")
	}
	got, _ := os.ReadFile(filepath.Join(e.SkillsDir, "sample", "SKILL.md"))
	if string(got) != "one" {
		t.Fatal("old output changed")
	}
}
func TestWindowsJunctionOrCopyFallback(t *testing.T) {
	i, s, p, e, k := setup(t)
	i.Link = func(string, string) error { return errors.New("privilege missing") }
	if err := i.Install(context.Background(), p, e, k, ""); err != nil {
		t.Fatal(err)
	}
	rows, _ := s.Installations()
	if len(rows) != 1 || rows[0].Mode != "copy" {
		t.Fatalf("%#v", rows)
	}
	got, _ := os.ReadFile(filepath.Join(e.SkillsDir, "sample", "SKILL.md"))
	if string(got) != "skill" {
		t.Fatal("copy missing")
	}
}
func TestCopyUpdateAndUninstall(t *testing.T) {
	i, _, p, e, k := setup(t)
	i.Link = func(string, string) error { return errors.New("no symlink") }
	if err := i.Install(context.Background(), p, e, k, ""); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(p.Dir, "SKILL.md"), []byte("new"), 0644)
	if err := i.Install(context.Background(), p, e, k, ""); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(e.SkillsDir, "sample", "SKILL.md"))
	if string(got) != "new" {
		t.Fatal("copy not updated")
	}
	if err := i.Uninstall(context.Background(), k, e); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(e.SkillsDir, "sample")); !os.IsNotExist(err) {
		t.Fatal("copy not removed")
	}
}
func TestEditedManagedCopyPreserved(t *testing.T) {
	i, s, p, e, k := setup(t)
	i.Link = func(string, string) error { return errors.New("no symlink") }
	if err := i.Install(context.Background(), p, e, k, ""); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(e.SkillsDir, "sample", "SKILL.md")
	os.WriteFile(dest, []byte("user edit"), 0644)
	if err := i.Uninstall(context.Background(), k, e); err == nil {
		t.Fatal("edited copy removed")
	}
	got, _ := os.ReadFile(dest)
	if string(got) != "user edit" {
		t.Fatal("edit lost")
	}
	rows, _ := s.Installations()
	if len(rows) != 1 {
		t.Fatal("ownership lost")
	}
}
func TestChangedSourceLocationRequiresUpdate(t *testing.T) {
	i, s, p, e, k := setup(t)
	if err := i.Install(context.Background(), p, e, k, ""); err != nil {
		t.Fatal(err)
	}
	original := p.Dir
	p.Dir = t.TempDir()
	os.WriteFile(filepath.Join(p.Dir, "SKILL.md"), []byte("moved"), 0644)
	if err := i.Install(context.Background(), p, e, k, ""); err == nil {
		t.Fatal("changed source accepted without explicit update")
	}
	got, _ := os.Readlink(filepath.Join(e.SkillsDir, "sample"))
	if got != original {
		t.Fatal("prior source changed")
	}
	rows, _ := s.Installations()
	if len(rows) != 1 || rows[0].SourcePath != original {
		t.Fatal("ledger changed")
	}
}

func TestUninstallSelectedHomePreservesOtherHome(t *testing.T) {
	i, s, p, a, k := setup(t)
	b := a
	b.SkillsDir = t.TempDir()
	if err := i.Install(context.Background(), p, a, k, ""); err != nil {
		t.Fatal(err)
	}
	if err := i.Install(context.Background(), p, b, k, ""); err != nil {
		t.Fatal(err)
	}
	if err := i.Uninstall(context.Background(), k, a); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(b.SkillsDir, "sample", "SKILL.md")); err != nil {
		t.Fatal("other home removed")
	}
	rows, _ := s.Installations()
	if len(rows) != 1 || rows[0].Destination != filepath.Join(b.SkillsDir, "sample") {
		t.Fatalf("wrong retained ledger: %v", rows)
	}
}

func TestExplicitSourceUpdateRepointsOwnedSkill(t *testing.T) {
	i, _, p, e, k := setup(t)
	if err := i.Install(context.Background(), p, e, k, ""); err != nil {
		t.Fatal(err)
	}
	p.Dir = t.TempDir()
	os.WriteFile(filepath.Join(p.Dir, "SKILL.md"), []byte("skill"), 0644)
	i.AllowSourceUpdate = true
	if err := i.Install(context.Background(), p, e, k, ""); err != nil {
		t.Fatal(err)
	}
	got, _ := os.Readlink(filepath.Join(e.SkillsDir, "sample"))
	if got != p.Dir {
		t.Fatal("explicit update did not repoint source")
	}
}
