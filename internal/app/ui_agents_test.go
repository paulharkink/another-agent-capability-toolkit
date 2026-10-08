package app

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
)

func TestUIAgentManagementShowsConfigExistenceSeparatelyFromDetection(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS Codex path fixture")
	}
	home := t.TempDir()
	isolateUXUserHome(t, home)
	t.Setenv("CODEX_HOME", "")
	svc, _, _ := fixture(t)
	path := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("model = 'test'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	rows, err := svc.UIAgentManagement(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var codexFound bool
	for _, row := range rows {
		if row.ID != "codex" {
			continue
		}
		codexFound = true
		if len(row.ConfigFiles) == 0 || row.ConfigFiles[0].Path != path || !row.ConfigFiles[0].Exists || row.Detection == "" {
			t.Fatalf("Codex config candidate missing: %+v", row)
		}
	}
	if !codexFound {
		t.Fatal("Codex row missing")
	}
}

func TestUIDefaultAgentConfigPathsMatchNativeOverrides(t *testing.T) {
	home := t.TempDir()
	isolateUXUserHome(t, home)
	t.Setenv("USERPROFILE", home)
	codexRoot := filepath.Join(home, "alternate-codex")
	xdgRoot := filepath.Join(home, "alternate-xdg")
	t.Setenv("CODEX_HOME", codexRoot)
	t.Setenv("XDG_CONFIG_HOME", xdgRoot)
	svc, _, _ := fixture(t)
	for _, tc := range []struct{ id, want string }{
		{"codex", filepath.Join(codexRoot, "config.toml")},
		{"opencode", filepath.Join(xdgRoot, "opencode", "opencode.jsonc")},
	} {
		env, err := svc.uiEnvironment(tc.id, state.Key{Source: "fixture", Package: "demo", Target: "default"})
		if err != nil || env.ConfigPath != tc.want {
			t.Fatalf("%s writes %q rather than active config %q: %v", tc.id, env.ConfigPath, tc.want, err)
		}
		if tc.id == "opencode" && env.SkillsDir != filepath.Join(xdgRoot, "opencode", "skills") {
			t.Fatalf("OpenCode skills use inactive config root: %q", env.SkillsDir)
		}
	}
}

func TestUIEnvironmentRestoresCustomHomeFromNamedMCPChildren(t *testing.T) {
	home := t.TempDir()
	isolateUXUserHome(t, home)
	t.Setenv("CODEX_HOME", "")
	svc, _, store := fixture(t)
	key := state.Key{Source: "fixture", Package: "demo", Environment: "sample-env", Target: "target-a"}
	customHome := filepath.Join(home, "custom-agent-home")
	configPath := filepath.Join(customHome, ".codex", "config.toml")
	for _, name := range []string{"inspector", "metrics"} {
		childKey := key
		childKey.MCP = name
		if err := store.Record(state.Installation{Key: childKey, AgentID: "codex", AgentHome: customHome, AgentKind: "codex", Component: "mcp", Destination: configPath, RegistrationName: "demo-home-" + name, URL: "http://127.0.0.1/mcp"}); err != nil {
			t.Fatal(err)
		}
	}
	resolved, err := svc.uiEnvironment("codex", key)
	if err != nil || resolved.Home != customHome || resolved.ConfigPath != configPath {
		t.Fatalf("named child rows lost prior custom agent paths: %+v %v", resolved, err)
	}
}

func TestUIAgentConfigShowsExactContentsAndRejectsUndiscoveredPaths(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS Codex path fixture")
	}
	home := t.TempDir()
	isolateUXUserHome(t, home)
	t.Setenv("CODEX_HOME", "")
	svc, _, _ := fixture(t)
	path := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	contents := "# keep this comment\napi_key = 'visible-token'\n\n"
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := svc.UIAgentConfig(context.Background(), "codex", path)
	if err != nil || got != contents {
		t.Fatalf("configuration was changed or masked: %q, %v", got, err)
	}
	secret := filepath.Join(home, "unrelated.txt")
	if err := os.WriteFile(secret, []byte("not a config"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UIAgentConfig(context.Background(), "codex", secret); err == nil || !strings.Contains(err.Error(), "discovered") {
		t.Fatalf("undiscovered file accepted: %v", err)
	}
	if _, err := svc.UIAgentConfig(context.Background(), "codex", filepath.Join(home, ".codex", "missing.toml")); err == nil {
		t.Fatal("missing file accepted")
	}
}

func TestUIAgentManagementIncludesAACTRegistrationPathWithoutClaimingClientInstall(t *testing.T) {
	home := t.TempDir()
	isolateUXUserHome(t, home)
	svc, _, store := fixture(t)
	path := filepath.Join(home, "custom", "mcp.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{\"mcp\":{}}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := store.Record(state.Installation{AgentID: "custom-client", AgentKind: "generic", Component: "mcp", Destination: path, Key: state.Key{Source: "s", Package: "p", Target: "t"}}); err != nil {
		t.Fatal(err)
	}
	rows, err := svc.UIAgentManagement(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.ID != "custom-client" {
			continue
		}
		if row.Detection != "unverified" || len(row.ConfigFiles) != 1 || row.ConfigFiles[0].Path != path || !row.ConfigFiles[0].Exists || len(row.Registrations) != 1 {
			t.Fatalf("custom registration discovery conflated: %+v", row)
		}
		got, err := svc.UIAgentConfig(context.Background(), row.ID, path)
		if err != nil || got != "{\"mcp\":{}}\n" {
			t.Fatalf("owned config not viewable: %q %v", got, err)
		}
		return
	}
	t.Fatal("AACT-owned custom client omitted")
}

func TestUIAgentManagementDoesNotRepeatProfileForMultipleConfigFiles(t *testing.T) {
	home := t.TempDir()
	isolateUXUserHome(t, home)
	svc, _, store := fixture(t)
	key := state.Key{Source: "s", Package: "p", Target: "t"}
	for _, path := range []string{"first.json", "second.jsonc"} {
		if err := store.Record(state.Installation{AgentID: "opencode", AgentKind: "opencode", Component: "mcp", Destination: filepath.Join(home, path), Key: key}); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := svc.UIAgentManagement(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.ID == "opencode" {
			if len(row.Registrations) != 1 {
				t.Fatalf("profile repeated for multiple files: %+v", row.Registrations)
			}
			return
		}
	}
	t.Fatal("OpenCode row missing")
}
