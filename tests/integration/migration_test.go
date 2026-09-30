package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/cli"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func migrationFile(t *testing.T, path, body string) {
	t.Helper()
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(path, []byte(body), 0600); e != nil {
		t.Fatal(e)
	}
}
func migrationTree(t *testing.T, root string) map[string]string {
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
			target, e := os.Readlink(path)
			if e != nil {
				return e
			}
			out[rel] = "symlink:" + target
		} else if entry.IsDir() {
			out[rel] = "directory"
		} else {
			data, e := os.ReadFile(path)
			if e != nil {
				return e
			}
			out[rel] = string(data)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	return out
}
func TestMigrationCLIReviewAndApplyPreservesLegacyResources(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	legacySkills := filepath.Join(root, "agent", "skills")
	stateRoot := filepath.Join(root, "state")
	settings := filepath.Join(root, "legacy-settings.json")
	t.Setenv("AACT_LEGACY_SKILLS_DIR", legacySkills)
	t.Setenv("AACT_LEGACY_SETTINGS", settings)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	cfg := filepath.Join(repo, "aact.toml")
	migrationFile(t, cfg, `schema_version=1
source_id="fixture-source"
[[catalog]]
id="plain"
source="./skills/plain"
[[catalog]]
id="inspect"
source="./packages/inspect"
`)
	skillSource := filepath.Join(repo, "skills", "plain")
	migrationFile(t, filepath.Join(skillSource, "SKILL.md"), "# Fixture skill\n")
	migrationFile(t, filepath.Join(repo, "packages", "inspect", "package.toml"), `schema_version=1
id="inspect"
[mcp]
name="inspect"
runtime="docker"
`)
	environments := filepath.Join(root, "environments")
	migrationFile(t, filepath.Join(environments, "demo", "inspect", "sample.toml"), "[mcp]\nlocal_port=8765\n")
	migrationFile(t, filepath.Join(stateRoot, "config-root"), environments+"\n")
	authPath := filepath.Join(stateRoot, "inspect", "demo", "sample")
	migrationFile(t, filepath.Join(authPath, "auth.json"), `{"token":"sanitized-fixture-token"}`)
	migrationFile(t, settings, `{"agents":["codex"]}`)
	if e := os.MkdirAll(legacySkills, 0700); e != nil {
		t.Fatal(e)
	}
	destination := filepath.Join(legacySkills, "plain")
	if e := os.Symlink(skillSource, destination); e != nil {
		t.Fatal(e)
	}
	before := migrationTree(t, root)
	var out, stderr bytes.Buffer
	code := cli.Run(context.Background(), []string{"migrate", "--dry-run", "--config", cfg, "--state-dir", stateRoot, "--json"}, nil, &out, &stderr)
	if code != 0 {
		t.Fatalf("code %d: %s", code, stderr.String())
	}
	if !reflect.DeepEqual(before, migrationTree(t, root)) {
		t.Fatal("CLI dry-run wrote files")
	}
	var plan state.Migration
	if e := json.Unmarshal(out.Bytes(), &plan); e != nil {
		t.Fatal(e)
	}
	if len(plan.Changes) != 1 || len(plan.Auth) != 1 || len(plan.Conflicts) != 0 || strings.Contains(out.String(), "sanitized-fixture-token") {
		t.Fatalf("%s", out.String())
	}
	out.Reset()
	stderr.Reset()
	code = cli.Run(context.Background(), []string{"migrate", "--apply", "--config", cfg, "--state-dir", stateRoot, "--json"}, nil, &out, &stderr)
	if code != 0 {
		t.Fatalf("code %d: %s", code, stderr.String())
	}
	store, e := state.OpenReadOnly(stateRoot)
	if e != nil {
		t.Fatal(e)
	}
	rows, e := store.Installations()
	if e != nil || len(rows) != 1 {
		t.Fatalf("%#v %v", rows, e)
	}
	key := state.Key{Source: "fixture-source", Package: "inspect", Environment: "demo", Target: "sample"}
	if store.AuthDir(key) != authPath {
		t.Fatalf("%s", store.AuthDir(key))
	}
	after := migrationTree(t, root)
	for path, body := range before {
		if after[path] != body {
			t.Fatalf("original resource changed %s", path)
		}
	}
}
func TestMigrationCLIDryRunWithoutStateDoesNotCreateIdentity(t *testing.T) {
	root := t.TempDir()
	cfg := filepath.Join(root, "repo", "aact.toml")
	migrationFile(t, cfg, "schema_version=1\n")
	t.Setenv("AACT_LEGACY_SKILLS_DIR", filepath.Join(root, "agent", "skills"))
	t.Setenv("AACT_LEGACY_SETTINGS", filepath.Join(root, "missing-settings.json"))
	stateRoot := filepath.Join(root, "absent-state")
	before := migrationTree(t, root)
	var out, stderr bytes.Buffer
	if code := cli.Run(context.Background(), []string{"migrate", "--dry-run", "--config", cfg, "--state-dir", stateRoot}, nil, &out, &stderr); code != 0 {
		t.Fatalf("code %d: %s", code, stderr.String())
	}
	if !reflect.DeepEqual(before, migrationTree(t, root)) {
		t.Fatal("dry-run created state or source identity")
	}
}
