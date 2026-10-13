package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func cliFixture(t *testing.T) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	// Fixture executable evidence only; these skill installs do not invoke the agent CLI.
	bin := filepath.Join(root, "bin")
	os.MkdirAll(bin, 0700)
	for _, name := range []string{"codex", "hermes"} {
		body := []byte("#!/bin/sh\nexit 0\n")
		if runtime.GOOS == "windows" {
			name += ".cmd"
			body = []byte("@exit /b 0\r\n")
		}
		os.WriteFile(filepath.Join(bin, name), body, 0700)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
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

func TestParseNamedExternalURLs(t *testing.T) {
	f, err := parse([]string{"install", "demo", "--external-url", "search=https://search.example/mcp", "--external-url=docs=http://docs.example/mcp"})
	if err != nil {
		t.Fatal(err)
	}
	if f.externalURLs["search"] != "https://search.example/mcp" || f.externalURLs["docs"] != "http://docs.example/mcp" || f.url != "" {
		t.Fatalf("named endpoints parsed incorrectly: %+v", f)
	}
	if _, err := parse([]string{"install", "demo", "--external-url", "search=https://one", "--external-url", "search=https://two"}); err == nil {
		t.Fatal("duplicate named endpoint accepted")
	}
}

func TestCLIAuthenticatePrintsLoginURLWithoutWaitingForHealth(t *testing.T) {
	root := t.TempDir()
	pkgDir := filepath.Join(root, "demo")
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		t.Fatal(err)
	}
	actionPath := filepath.Join(root, "authenticate.sh")
	command := "[\"sh\", " + strconv.Quote(actionPath) + "]"
	actionBody := []byte("#!/bin/sh\nprintf 'x\\n' >> \"$(dirname \"$0\")/calls\"\nprintf '%s\\n' '{\"login_url\":\"https://login.example/device\"}'\n")
	if runtime.GOOS == "windows" {
		actionPath += ".cmd"
		command = "[\"cmd\", \"/c\", " + strconv.Quote(actionPath) + "]"
		actionBody = []byte("@echo x>>\"%~dp0calls\"\r\n@echo {\"login_url\":\"https://login.example/device\"}\r\n")
	}
	if err := os.WriteFile(actionPath, actionBody, 0700); err != nil {
		t.Fatal(err)
	}
	manifest := "schema_version = 1\nid = \"demo\"\nname = \"Demo\"\n[mcp]\nname = \"server\"\nhealth_path = \"/health\"\n[mcp.actions.authenticate]\ntarget = \"host\"\ncommand = " + command + "\n"
	if err := os.WriteFile(filepath.Join(pkgDir, "package.toml"), []byte(manifest), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(root, "aact.toml")
	if err := os.WriteFile(cfg, []byte("schema_version = 1\nsource_id = \"fixture\"\n[[catalog]]\nid = \"demo\"\nsource = \"./demo\"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(root, "state")
	var out, errOut bytes.Buffer
	profileArgs := []string{"profile", "create", "demo", "default", "--config", cfg, "--state-dir", stateDir}
	if code := Run(context.Background(), profileArgs, nil, &out, &errOut); code != 0 {
		t.Fatalf("profile creation failed (%d): %s", code, errOut.String())
	}
	out.Reset()
	errOut.Reset()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	type result struct {
		code        int
		output, err string
	}
	done := make(chan result, 1)
	args := []string{"mcp", "authenticate", "demo", "--profile", "default", "--config", cfg, "--state-dir", stateDir}
	go func() {
		var commandOut, commandErr bytes.Buffer
		code := Run(ctx, args, nil, &commandOut, &commandErr)
		done <- result{code: code, output: commandOut.String(), err: commandErr.String()}
	}()
	var got result
	select {
	case got = <-done:
	case <-ctx.Done():
		t.Fatal("direct authenticate did not return before its bounded context expired")
	}
	if got.code != 0 {
		t.Fatalf("authenticate failed (%d): %s", got.code, got.err)
	}
	if strings.TrimSpace(got.output) != "https://login.example/device" {
		t.Fatalf("authenticate output = %q, want returned login URL", got.output)
	}
	calls, err := os.ReadFile(filepath.Join(root, "calls"))
	if err != nil || string(calls) != "x\n" {
		t.Fatalf("authenticate action invocations = %q, %v; want exactly one", calls, err)
	}
}

func TestCLIUsageAdvertisesOnlySharedAuthenticateAction(t *testing.T) {
	if !strings.Contains(usage, "authenticate") {
		t.Fatal("MCP usage omits authenticate")
	}
	for _, action := range []string{"authentication_status", "cancel_authentication"} {
		if strings.Contains(usage, action) {
			t.Fatalf("obsolete MCP action %q remains in usage", action)
		}
	}
}

func TestParseEnvironmentDirectoryFlagAndLegacyAlias(t *testing.T) {
	for _, flag := range []string{"--environment-directory", "--environment-root"} {
		f, err := parse([]string{"install", "demo", flag, "/tmp/pack-envs"})
		if err != nil || f.envroot != "/tmp/pack-envs" {
			t.Fatalf("%s was not parsed as the environment directory: flags=%+v err=%v", flag, f, err)
		}
	}
}

func TestCLIConfigUsesEnvironmentDirectoryNameAndRetainsOldAlias(t *testing.T) {
	cfg, _, _ := cliFixture(t)
	for _, command := range []string{"set-environment-directory", "set-environment-root"} {
		t.Run(command, func(t *testing.T) {
			stateDir := filepath.Join(t.TempDir(), "state")
			environments := t.TempDir()
			var out, errout bytes.Buffer
			args := []string{"config", command, environments, "--config", cfg, "--state-dir", stateDir}
			if code := Run(context.Background(), args, nil, &out, &errout); code != 0 {
				t.Fatalf("config command failed (%d): %s", code, errout.String())
			}
			if !strings.Contains(out.String(), "Profile configuration directory: "+environments) {
				t.Fatalf("output did not use pack terminology: %s", out.String())
			}
			data, err := os.ReadFile(filepath.Join(stateDir, "config-root"))
			if err != nil || string(data) != environments+"\n" {
				t.Fatalf("environment directory was not saved: %q %v", data, err)
			}
		})
	}
}

func TestCLIConfigSetsMCPImageSource(t *testing.T) {
	cfg, _, _ := cliFixture(t)
	stateDir := filepath.Join(t.TempDir(), "state")
	var out, errout bytes.Buffer
	args := []string{"config", "set-image-source", "local", "--config", cfg, "--state-dir", stateDir}
	if code := Run(context.Background(), args, nil, &out, &errout); code != 0 {
		t.Fatalf("config failed (%d): %s", code, errout.String())
	}
	b, err := os.ReadFile(filepath.Join(stateDir, "manager", "settings.json"))
	if err != nil || !strings.Contains(string(b), `"image_source": "local"`) {
		t.Fatalf("image source not saved: %s %v", b, err)
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

func TestCLIInstallAcceptsHermesSkillsOnlyFlag(t *testing.T) {
	cfg, st, home := cliFixture(t)
	var out, errout bytes.Buffer
	args := []string{"install", "demo", "--config", cfg, "--state-dir", st, "--agent", "hermes", "--agent-home", home, "--set", "label=hermes", "--skills-only"}
	if code := Run(context.Background(), args, nil, &out, &errout); code != 0 {
		t.Fatalf("Hermes skills-only install failed (%d): %s", code, errout.String())
	}
	content, err := os.ReadFile(filepath.Join(home, ".hermes", "skills", "demo", "SKILL.md"))
	if err != nil || string(content) != "Hello hermes" {
		t.Fatalf("Hermes skill missing: %s, %v", content, err)
	}
}

func TestAgentScopesKeepNativeOverridesInsideAdapters(t *testing.T) {
	scopes, err := agentScopes(flags{agents: []string{"codex", "opencode"}})
	if err != nil || scopes["codex"].ExplicitHome || scopes["codex"].Home != "" {
		t.Fatalf("%v %v", scopes, err)
	}
	custom := t.TempDir()
	scopes, err = agentScopes(flags{agents: []string{"codex"}, homes: []string{"codex=" + custom}})
	if err != nil || !scopes["codex"].ExplicitHome || scopes["codex"].Home != custom || scopes["codex"].ConfigPathOverride != "" {
		t.Fatalf("%v %v", scopes, err)
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

func TestSubprocessStderrOwnershipDependsOnCLIInvocation(t *testing.T) {
	var tuiTerminal bytes.Buffer
	tuiOptions := appOptions(&tuiTerminal, "", true)
	if tuiOptions.OnStderr != nil {
		tuiOptions.OnStderr([]byte("BuildKit output\n"))
	}
	if got := tuiTerminal.String(); got != "" {
		t.Fatalf("TUI subprocess output escaped model ownership to terminal stderr: %q", got)
	}

	var cliDiagnostics bytes.Buffer
	cliOptions := appOptions(&cliDiagnostics, "", false)
	if cliOptions.OnStderr == nil {
		t.Fatal("noninteractive CLI lost its subprocess stderr diagnostics")
	}
	cliOptions.OnStderr([]byte("BuildKit output\n"))
	if got := cliDiagnostics.String(); got != "BuildKit output\n" {
		t.Fatalf("CLI stderr = %q, want preserved diagnostic", got)
	}
}
