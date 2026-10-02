package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func cliFixture(t *testing.T) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	pkg := filepath.Join(root, "demo")
	os.MkdirAll(pkg, 0755)
	os.WriteFile(filepath.Join(pkg, "SKILL.md.mustache"), []byte("Hello {{{inputs.label}}}"), 0644)
	os.WriteFile(filepath.Join(pkg, "package.toml"), []byte("schema_version = 1\nid = \"demo\"\nname = \"Demo\"\n[skill]\nname = \"demo\"\n[[inputs]]\nname = \"label\"\ntype = \"string\"\nrequired = true\n[[templates]]\nsource = \"SKILL.md.mustache\"\ndestination = \"SKILL.md\"\n"), 0644)
	cfg := filepath.Join(root, "aact.toml")
	os.WriteFile(cfg, []byte("schema_version = 1\nsource_id = \"fixture\"\n[[catalog]]\nid = \"demo\"\nsource = \"./demo\"\n"), 0644)
	return cfg, filepath.Join(root, "state"), filepath.Join(root, "home")
}
func TestCLIInstallRenderRemove(t *testing.T) {
	cfg, st, home := cliFixture(t)
	var out, errout bytes.Buffer
	args := []string{"install", "demo", "--config", cfg, "--state-dir", st, "--agent", "codex", "--agent-home", home, "--set", "label=world", "--json"}
	if code := Run(context.Background(), args, strings.NewReader(""), &out, &errout); code != 0 {
		t.Fatal(code, errout.String())
	}
	b, e := os.ReadFile(filepath.Join(home, ".agents", "skills", "demo", "SKILL.md"))
	if e != nil || string(b) != "Hello world" {
		t.Fatal(string(b), e)
	}
	out.Reset()
	errout.Reset()
	if code := Run(context.Background(), []string{"uninstall", "demo", "--config", cfg, "--state-dir", st, "--agent", "codex", "--agent-home", home}, nil, &out, &errout); code != 0 {
		t.Fatal(code, errout.String())
	}
	if _, e = os.Lstat(filepath.Join(home, ".agents", "skills", "demo")); !os.IsNotExist(e) {
		t.Fatal(e)
	}
}

func TestCLIInstallSkillToGlobalAllDestination(t *testing.T) {
	cfg, st, home := cliFixture(t)
	var out, errout bytes.Buffer
	args := []string{"install", "demo", "--config", cfg, "--state-dir", st, "--agent", "all", "--agent-home", home, "--set", "label=global"}
	if code := Run(context.Background(), args, nil, &out, &errout); code != 0 {
		t.Fatalf("global install failed (%d): %s", code, errout.String())
	}
	content, err := os.ReadFile(filepath.Join(home, ".agents", "skills", "demo", "SKILL.md"))
	if err != nil || string(content) != "Hello global" {
		t.Fatalf("global skill missing: %s, %v", content, err)
	}
}
func TestAgentEnvironmentsHonorNativeConfigOverridesWithoutCustomHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	codexRoot := filepath.Join(home, "codex-override")
	xdgRoot := filepath.Join(home, "xdg-override")
	t.Setenv("CODEX_HOME", codexRoot)
	t.Setenv("XDG_CONFIG_HOME", xdgRoot)
	envs, err := agentEnvironments(flags{agents: []string{"codex", "opencode"}})
	if err != nil || len(envs) != 2 || envs[0].ConfigPath != filepath.Join(codexRoot, "config.toml") || envs[1].ConfigPath != filepath.Join(xdgRoot, "opencode", "opencode.json") {
		t.Fatalf("native overrides ignored: %+v, %v", envs, err)
	}
	custom := filepath.Join(home, "custom-agent")
	envs, err = agentEnvironments(flags{agents: []string{"codex"}, homes: []string{"codex=" + custom}})
	if err != nil || len(envs) != 1 || envs[0].ConfigPath != filepath.Join(custom, ".codex", "config.toml") {
		t.Fatalf("explicit agent home overridden: %+v, %v", envs, err)
	}
}
func TestMissingInputExitTwo(t *testing.T) {
	cfg, st, home := cliFixture(t)
	var out, errout bytes.Buffer
	code := Run(context.Background(), []string{"install", "demo", "--config", cfg, "--state-dir", st, "--agent", "codex", "--agent-home", home}, nil, &out, &errout)
	if code != 2 || !strings.Contains(errout.String(), "--interactive") {
		t.Fatal(code, errout.String())
	}
}
func TestCatalogJSONOutput(t *testing.T) {
	cfg, st, _ := cliFixture(t)
	var out, errout bytes.Buffer
	code := Run(context.Background(), []string{"catalog", "--config", cfg, "--state-dir", st, "--json"}, nil, &out, &errout)
	if code != 0 {
		t.Fatal(code, errout.String())
	}
	var entries []map[string]any
	if e := json.Unmarshal(out.Bytes(), &entries); e != nil || len(entries) != 1 || entries[0]["id"] != "demo" {
		t.Fatal(out.String(), e)
	}
}
func TestFlagsRejectUnknownAndRepeatedScalar(t *testing.T) {
	cfg, st, home := cliFixture(t)
	for _, tail := range [][]string{{"--surprise"}, {"--set", "label=a", "--set", "label=b"}} {
		var out, errout bytes.Buffer
		a := []string{"install", "demo", "--config", cfg, "--state-dir", st, "--agent", "codex", "--agent-home", home}
		a = append(a, tail...)
		if code := Run(context.Background(), a, nil, &out, &errout); code != 2 {
			t.Fatal(code, errout.String())
		}
	}
}

func TestMigrationDryRunHasNoWrites(t *testing.T) {
	cfg, st, home := cliFixture(t)
	b, _ := os.ReadFile(cfg)
	b = []byte(strings.ReplaceAll(string(b), "source_id = \"fixture\"\n", ""))
	os.WriteFile(cfg, b, 0644)
	t.Setenv("AACT_LEGACY_SETTINGS", filepath.Join(home, "absent-settings"))
	t.Setenv("AACT_LEGACY_SKILLS_DIR", filepath.Join(home, "absent-skills"))
	var out, errout bytes.Buffer
	code := Run(context.Background(), []string{"migrate", "--dry-run", "--json", "--config", cfg, "--state-dir", st}, nil, &out, &errout)
	if code != 0 {
		t.Fatal(code, errout.String())
	}
	if _, e := os.Stat(st); !os.IsNotExist(e) {
		t.Fatal("dry run created state", e)
	}
	var result map[string]any
	if e := json.Unmarshal(out.Bytes(), &result); e != nil {
		t.Fatal(out.String(), e)
	}
}
func TestDryRunCannotSilentlyInstall(t *testing.T) {
	cfg, st, home := cliFixture(t)
	var out, errout bytes.Buffer
	code := Run(context.Background(), []string{"install", "demo", "--dry-run", "--config", cfg, "--state-dir", st, "--agent", "codex", "--agent-home", home, "--set", "label=no"}, nil, &out, &errout)
	if code != 2 {
		t.Fatal(code, errout.String())
	}
	if _, e := os.Stat(home); !os.IsNotExist(e) {
		t.Fatal("ignored --dry-run and installed")
	}
}
