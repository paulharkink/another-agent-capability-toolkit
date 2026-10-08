package install

import (
	"context"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"os"
	"path/filepath"
	"testing"
)

func TestMultipleSkillEffectsAreRecordedByCoordinator(t *testing.T) {
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	engine := NewSkills(store)
	destination := SkillDestination{ID: "test", Home: t.TempDir(), SkillsDir: t.TempDir()}
	key := state.Key{Source: "pack", Package: "guidance", Target: "ota"}
	ctx := context.Background()
	records := []state.Installation{}
	for _, name := range []string{"review", "release"} {
		src := t.TempDir()
		if err := os.WriteFile(filepath.Join(src, "SKILL.md"), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
		row, err := engine.InstallOne(ctx, catalog.Package{Dir: src}, catalog.Skill{Name: name, Source: src}, destination, key, "")
		if err != nil {
			t.Fatal(err)
		}
		records = append(records, row)
		before, _ := store.Installations()
		if len(before) != len(records)-1 {
			t.Fatal("file primitive recorded an effect before coordinator acceptance")
		}
		if err := store.Record(row); err != nil {
			t.Fatal(err)
		}
		if _, err := engine.InstallOne(ctx, catalog.Package{Dir: src}, catalog.Skill{Name: name, Source: src}, destination, key, ""); err != nil {
			t.Fatal("repeated install not idempotent:", err)
		}
	}
	if err := engine.RemoveOne(ctx, records[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(records[0].Destination); !os.IsNotExist(err) {
		t.Fatal("selected skill not removed")
	}
	if _, err := os.Stat(filepath.Join(records[1].Destination, "SKILL.md")); err != nil {
		t.Fatal("removing one skill removed another:", err)
	}
}
