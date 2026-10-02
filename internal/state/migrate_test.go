package state

import (
	"context"
	"encoding/json"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func migrationFixture(t *testing.T) (config.Source, *Store, string) {
	t.Helper()
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	legacy := filepath.Join(root, "agent-skills")
	settings := filepath.Join(root, "legacy-settings.json")
	for _, dir := range []string{repo, legacy} {
		if e := os.MkdirAll(dir, 0700); e != nil {
			t.Fatal(e)
		}
	}
	t.Setenv("AACT_LEGACY_SKILLS_DIR", legacy)
	t.Setenv("AACT_LEGACY_SETTINGS", settings)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	s, e := Open(filepath.Join(root, "state"))
	if e != nil {
		t.Fatal(e)
	}
	sourceDir := filepath.Join(repo, "skills", "plain")
	migrationPut(t, filepath.Join(sourceDir, "SKILL.md"), "# Skill\n")
	src := config.Source{ID: "source", Root: repo, EnvironmentRoot: filepath.Join(root, "environments"), Catalog: []catalog.Package{{SchemaVersion: 1, ID: "plain", Dir: sourceDir, Skill: &catalog.Skill{Name: "plain"}}}}
	return src, s, legacy
}
func migrationPut(t *testing.T, path, body string) {
	t.Helper()
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(path, []byte(body), 0600); e != nil {
		t.Fatal(e)
	}
}
func TestLegacyConfigRootAndSettingsImported(t *testing.T) {
	src, s, _ := migrationFixture(t)
	src.EnvironmentRoot = ""
	root := filepath.Join(t.TempDir(), "environments")
	migrationPut(t, filepath.Join(s.Root(), "config-root"), root+"\n")
	legacySettings := os.Getenv("AACT_LEGACY_SETTINGS")
	migrationPut(t, legacySettings, `{"agents":["codex","opencode"]}`)
	plan, e := PlanMigration(context.Background(), src, s)
	if e != nil {
		t.Fatal(e)
	}
	if plan.EnvironmentRoot != root || !reflect.DeepEqual(plan.Settings["agents"], []string{"codex", "opencode"}) {
		t.Fatalf("%#v", plan)
	}
	if e = ApplyMigration(context.Background(), plan); e != nil {
		t.Fatal(e)
	}
	data, e := os.ReadFile(filepath.Join(s.Root(), "manager", "settings.json"))
	if e != nil {
		t.Fatal(e)
	}
	var imported map[string]any
	if e = json.Unmarshal(data, &imported); e != nil {
		t.Fatal(e)
	}
	if imported["environment_root"] != root || len(imported["agents"].([]any)) != 2 {
		t.Fatalf("%s", data)
	}
	after, e := os.ReadFile(legacySettings)
	if e != nil || string(after) != `{"agents":["codex","opencode"]}` {
		t.Fatalf("settings changed %s %v", after, e)
	}
}
func TestOwnedLegacySkillLinkAdopted(t *testing.T) {
	src, s, legacy := migrationFixture(t)
	dest := filepath.Join(legacy, "plain")
	if e := os.Symlink(src.Catalog[0].Dir, dest); e != nil {
		t.Fatal(e)
	}
	plan, e := PlanMigration(context.Background(), src, s)
	if e != nil || len(plan.Conflicts) != 0 || len(plan.Changes) != 1 {
		t.Fatalf("%#v %v", plan, e)
	}
	if plan.Changes[0].Mode != "symlink" || plan.Changes[0].SourcePath != src.Catalog[0].Dir {
		t.Fatalf("%#v", plan.Changes)
	}
	if e = ApplyMigration(context.Background(), plan); e != nil {
		t.Fatal(e)
	}
	installed, e := s.Installations()
	if e != nil || len(installed) != 1 {
		t.Fatalf("%#v %v", installed, e)
	}
	if link, e := os.Readlink(dest); e != nil || link != src.Catalog[0].Dir {
		t.Fatalf("%s %v", link, e)
	}
}
func TestLegacyGeneratedMarkerAdopted(t *testing.T) {
	src, s, legacy := migrationFixture(t)
	dest := filepath.Join(legacy, "plain")
	migrationPut(t, filepath.Join(dest, "SKILL.md"), "# Generated\n")
	migrationPut(t, filepath.Join(dest, ".agent-skills-generated-skill"), src.Catalog[0].Dir+"\n")
	plan, e := PlanMigration(context.Background(), src, s)
	if e != nil || len(plan.Changes) != 1 || plan.Changes[0].Mode != "copy" || plan.Changes[0].Digest == "" {
		t.Fatalf("%#v %v", plan, e)
	}
	if e = ApplyMigration(context.Background(), plan); e != nil {
		t.Fatal(e)
	}
	content, e := os.ReadFile(filepath.Join(dest, "SKILL.md"))
	if e != nil || string(content) != "# Generated\n" {
		t.Fatalf("%s %v", content, e)
	}
}
func authFixture(t *testing.T, src config.Source, s *Store) (config.Source, Key, string) {
	p := catalog.Package{SchemaVersion: 1, ID: "inspect", MCP: &catalog.MCP{Name: "inspect"}}
	src.Catalog = append(src.Catalog, p)
	migrationPut(t, filepath.Join(src.EnvironmentRoot, "dev", "inspect", "production.toml"), "[mcp]\nlocal_port=8765\n[cluster]\napi_server='https://cluster.example'\n")
	path := filepath.Join(s.Root(), "inspect", "dev", "production")
	migrationPut(t, filepath.Join(path, "auth.json"), `{"token":"sanitized-test-token"}`)
	return src, Key{Source: src.ID, Package: "inspect", Environment: "dev", Target: "production"}, path
}
func TestExistingAuthPreserved(t *testing.T) {
	src, s, _ := migrationFixture(t)
	src, k, path := authFixture(t, src, s)
	before, e := os.ReadFile(filepath.Join(path, "auth.json"))
	if e != nil {
		t.Fatal(e)
	}
	plan, e := PlanMigration(context.Background(), src, s)
	if e != nil || len(plan.Auth) != 1 {
		t.Fatalf("%#v %v", plan, e)
	}
	if e = ApplyMigration(context.Background(), plan); e != nil {
		t.Fatal(e)
	}
	if s.AuthDir(k) != path {
		t.Fatalf("%s", s.AuthDir(k))
	}
	after, e := os.ReadFile(filepath.Join(path, "auth.json"))
	if e != nil || !reflect.DeepEqual(after, before) {
		t.Fatal("auth changed", e)
	}
}
func TestUnownedLegacyResourceRefused(t *testing.T) {
	src, s, legacy := migrationFixture(t)
	migrationPut(t, filepath.Join(legacy, "plain", "SKILL.md"), "# Foreign\n")
	plan, e := PlanMigration(context.Background(), src, s)
	if e != nil || len(plan.Conflicts) != 1 || len(plan.Changes) != 0 {
		t.Fatalf("%#v %v", plan, e)
	}
	if e = ApplyMigration(context.Background(), plan); e == nil {
		t.Fatal("applied unowned resource")
	}
	rows, e := s.Installations()
	if e != nil || len(rows) != 0 {
		t.Fatalf("%#v %v", rows, e)
	}
}
func TestSourceCollisionUsesSeparateState(t *testing.T) {
	src, s, _ := migrationFixture(t)
	src, k, path := authFixture(t, src, s)
	other := k
	other.Source = "other-source"
	if e := s.AdoptAuth(other, path); e != nil {
		t.Fatal(e)
	}
	plan, e := PlanMigration(context.Background(), src, s)
	if e != nil || len(plan.Conflicts) == 0 || len(plan.Auth) != 0 {
		t.Fatalf("%#v %v", plan, e)
	}
	if s.AuthDir(k) == path || s.AuthDir(other) != path {
		t.Fatal("repointed existing auth")
	}
}
func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	if e := filepath.WalkDir(root, func(path string, entry os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		rel, e := filepath.Rel(root, path)
		if e != nil {
			return e
		}
		if entry.Type()&os.ModeSymlink != 0 {
			link, e := os.Readlink(path)
			if e != nil {
				return e
			}
			out[rel] = "link:" + link
		} else if entry.IsDir() {
			out[rel] = "dir"
		} else {
			b, e := os.ReadFile(path)
			if e != nil {
				return e
			}
			out[rel] = string(b)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	return out
}
func TestMigrationDryRunHasNoWrites(t *testing.T) {
	src, s, legacy := migrationFixture(t)
	if e := os.Symlink(src.Catalog[0].Dir, filepath.Join(legacy, "plain")); e != nil {
		t.Fatal(e)
	}
	src, _, _ = authFixture(t, src, s)
	migrationPut(t, os.Getenv("AACT_LEGACY_SETTINGS"), `{"agents":["codex"]}`)
	root := filepath.Dir(s.Root())
	before := snapshot(t, root)
	if _, e := PlanMigration(context.Background(), src, s); e != nil {
		t.Fatal(e)
	}
	after := snapshot(t, root)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("dry-run wrote files")
	}
}

func TestMigrationRefusesEscapingLegacyAuthSymlink(t *testing.T) {
	src, s, _ := migrationFixture(t)
	src.Catalog = append(src.Catalog, catalog.Package{ID: "inspect", MCP: &catalog.MCP{Name: "inspect"}})
	migrationPut(t, filepath.Join(src.EnvironmentRoot, "dev", "inspect", "production.toml"), "[mcp]\nlocal_port=8765\n")
	external := t.TempDir()
	migrationPut(t, filepath.Join(external, "dev", "production", "auth.json"), `{"token":"sanitized-outside-token"}`)
	if e := os.Symlink(external, filepath.Join(s.Root(), "inspect")); e != nil {
		t.Fatal(e)
	}
	plan, e := PlanMigration(context.Background(), src, s)
	if e != nil || len(plan.Conflicts) == 0 || len(plan.Auth) != 0 {
		t.Fatalf("%#v %v", plan, e)
	}
}
