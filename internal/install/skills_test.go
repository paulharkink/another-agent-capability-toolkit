package install

import (
	"context"
	"encoding/binary"
	"errors"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"os"
	"path/filepath"
	"strings"
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

func TestVerifiedForeignLXSkillLinkIsReplacedWithLocalGeneratedSkill(t *testing.T) {
	i, s, p, e, k := setup(t)
	parentID := k.ID()
	foreignTarget := "/home/synthetic/.local/state/agent-skills/generated/" + parentID + "/output-17"
	dest := filepath.Join(e.SkillsDir, p.Skill.Name)
	if err := os.Symlink(foreignTarget, dest); err != nil {
		t.Fatal(err)
	}
	i.readLXLink = func(string) (string, bool, error) { return foreignTarget, true, nil }
	generated := filepath.Join(s.Root(), "generated", k.ID(), "output-2")
	if err := os.MkdirAll(generated, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(generated, "SKILL.md"), []byte("rendered locally"), 0644); err != nil {
		t.Fatal(err)
	}
	proof := &ForeignSkillProof{ParentKeyID: parentID, RegistrationName: "synthetic-registration", URL: "http://127.0.0.1:4242/mcp"}
	row, err := i.InstallOneWithProof(context.Background(), p, *p.Skill, e, k, generated, proof)
	if err != nil {
		t.Fatalf("verified external link not replaced: %v", err)
	}
	got, err := os.Readlink(dest)
	if err != nil || got != generated {
		t.Fatalf("destination target = %q, err = %v; want local generated path %q", got, err, generated)
	}
	if row.SourcePath != generated || row.Mode != "symlink" {
		t.Fatalf("recorded installation = %#v", row)
	}
	if err := s.Record(row); err != nil {
		t.Fatal(err)
	}
	rows, err := s.Installations()
	if err != nil || len(rows) != 1 || rows[0].SourcePath != generated {
		t.Fatalf("local installation ledger = %#v, err = %v", rows, err)
	}
	if err := i.Uninstall(context.Background(), k, e); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(dest); !os.IsNotExist(err) {
		t.Fatalf("Windows destination remains after uninstall: %v", err)
	}
	if _, err := os.Lstat(generated); !os.IsNotExist(err) {
		t.Fatalf("local generated artifact remains after uninstall: %v", err)
	}
}

func TestForeignLXLinkRequiresExactGeneratedParentAndValidPayload(t *testing.T) {
	tests := []struct {
		name, target string
		proof        *ForeignSkillProof
		isLX         bool
		directory    bool
		outsideStore bool
	}{
		{name: "mismatched key", target: "/home/synthetic/.local/state/agent-skills/generated/" + strings.Repeat("b", 64) + "/output-17", proof: proofFor(strings.Repeat("a", 64)), isLX: true},
		{name: "wrong output name", target: "/home/synthetic/.local/state/agent-skills/generated/" + strings.Repeat("a", 64) + "/output-x", proof: proofFor(strings.Repeat("a", 64)), isLX: true},
		{name: "path traversal", target: "/home/synthetic/.local/state/agent-skills/generated/" + strings.Repeat("a", 64) + "/../output-17", proof: proofFor(strings.Repeat("a", 64)), isLX: true},
		{name: "missing binding proof", target: "/home/synthetic/.local/state/agent-skills/generated/" + strings.Repeat("a", 64) + "/output-17"},
		{name: "incomplete binding proof", target: "/home/synthetic/.local/state/agent-skills/generated/" + strings.Repeat("a", 64) + "/output-17", proof: &ForeignSkillProof{ParentKeyID: strings.Repeat("a", 64), RegistrationName: "synthetic-registration"}, isLX: true},
		{name: "other reparse type", target: "/home/synthetic/.local/state/agent-skills/generated/" + strings.Repeat("a", 64) + "/output-17", proof: proofFor(strings.Repeat("a", 64))},
		{name: "proof key does not match requested install key", target: "/home/synthetic/.local/state/agent-skills/generated/" + strings.Repeat("a", 64) + "/output-17", proof: proofFor(strings.Repeat("a", 64)), isLX: true},
		{name: "directory destination", target: "/home/synthetic/.local/state/agent-skills/generated/" + strings.Repeat("a", 64) + "/output-17", proof: proofFor(strings.Repeat("a", 64)), directory: true},
		{name: "staged outside local generated store", target: "/home/synthetic/.local/state/agent-skills/generated/" + strings.Repeat("a", 64) + "/output-17", proof: proofFor(strings.Repeat("a", 64)), isLX: true, outsideStore: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			i, _, p, e, k := setup(t)
			dest := filepath.Join(e.SkillsDir, p.Skill.Name)
			if tt.directory {
				if err := os.Mkdir(dest, 0755); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Symlink(tt.target, dest); err != nil {
				t.Fatal(err)
			}
			i.readLXLink = func(string) (string, bool, error) { return tt.target, tt.isLX, nil }
			generated := filepath.Join(i.Store.Root(), "generated", k.ID(), "output-3")
			if tt.outsideStore {
				generated = filepath.Join(t.TempDir(), "output-3")
			}
			if err := os.MkdirAll(generated, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(generated, "SKILL.md"), []byte("local"), 0644); err != nil {
				t.Fatal(err)
			}
			if _, err := i.InstallOneWithProof(context.Background(), p, *p.Skill, e, k, generated, tt.proof); err == nil {
				t.Fatal("unverified foreign link accepted")
			}
			if tt.directory {
				if _, err := os.Stat(dest); err != nil {
					t.Fatalf("foreign directory changed: %v", err)
				}
			} else {
				got, err := os.Readlink(dest)
				if err != nil || got != tt.target {
					t.Fatalf("foreign link changed: target=%q err=%v", got, err)
				}
			}
		})
	}
}

func proofFor(parentID string) *ForeignSkillProof {
	return &ForeignSkillProof{ParentKeyID: parentID, RegistrationName: "synthetic-registration", URL: "http://127.0.0.1:4242/mcp"}
}

func TestParseLXReparseTargetBoundsAndVersion(t *testing.T) {
	target := "/home/synthetic/.local/state/agent-skills/generated/" + strings.Repeat("c", 64) + "/output-9"
	payload := make([]byte, 4+len(target)+1)
	binary.LittleEndian.PutUint32(payload[:4], 2)
	copy(payload[4:], target)
	raw := make([]byte, 8+len(payload))
	binary.LittleEndian.PutUint32(raw[:4], lxSymlinkReparseTag)
	binary.LittleEndian.PutUint16(raw[4:6], uint16(len(payload)))
	copy(raw[8:], payload)
	got, isLX, err := parseLXReparseTarget(raw)
	if err != nil || !isLX || got != target {
		t.Fatalf("parsed target = %q, isLX=%v, err=%v", got, isLX, err)
	}
	for name, malformed := range map[string][]byte{
		"truncated payload":   raw[:len(raw)-1],
		"trailing payload":    append(append([]byte(nil), raw...), 0),
		"unsupported version": func() []byte { b := append([]byte(nil), raw...); binary.LittleEndian.PutUint32(b[8:12], 3); return b }(),
		"embedded terminator": func() []byte { b := append([]byte(nil), raw...); b[12] = 0; return b }(),
	} {
		t.Run(name, func(t *testing.T) {
			if _, ok, err := parseLXReparseTarget(malformed); !ok || err == nil {
				t.Fatalf("malformed payload accepted: isLX=%v err=%v", ok, err)
			}
		})
	}
}

func TestMatchesGeneratedLXTargetAllowsCustomAbsoluteStateRoot(t *testing.T) {
	parentID := strings.Repeat("d", 64)
	target := "/var/lib/synthetic/aact-state/agent-skills/generated/" + parentID + "/output-41"
	if !matchesGeneratedLXTarget(target, parentID) {
		t.Fatalf("custom absolute state root target rejected: %s", target)
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
