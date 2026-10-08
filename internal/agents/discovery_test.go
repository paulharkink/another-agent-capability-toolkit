package agents

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func probeFixture(t *testing.T) (DiscoveryProbe, string) {
	t.Helper()
	home := t.TempDir()
	return DiscoveryProbe{
		GOOS:     "darwin",
		Home:     home,
		AppRoots: []string{filepath.Join(home, "Applications")},
		LookPath: func(string) (string, error) { return "", os.ErrNotExist },
		Getenv:   func(string) string { return "" },
	}, home
}

func TestDiscoveryDistinguishesExecutableFromConfigExistence(t *testing.T) {
	p, home := probeFixture(t)
	p.LookPath = func(name string) (string, error) {
		if name == "codex" {
			return "/opt/homebrew/bin/codex", nil
		}
		return "", os.ErrNotExist
	}
	got := DiscoverAgent(context.Background(), "codex", p)
	if got.Detection != "installed" || got.Evidence == "" || len(got.ConfigFiles) != 1 || got.ConfigFiles[0].Exists {
		t.Fatalf("executable and config were conflated: %+v", got)
	}
	path := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("model = 'demo'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	p.LookPath = func(string) (string, error) { return "", os.ErrNotExist }
	got = DiscoverAgent(context.Background(), "codex", p)
	if got.Detection != "not-detected" || !got.ConfigFiles[0].Exists || got.ConfigFiles[0].Path != path {
		t.Fatalf("config existence was treated as installation evidence: %+v", got)
	}
}

func TestDiscoveryReportsCodexDesktopWithoutInventingCLI(t *testing.T) {
	p, home := probeFixture(t)
	if err := os.MkdirAll(filepath.Join(home, "Applications", "Codex.app"), 0700); err != nil {
		t.Fatal(err)
	}
	got := DiscoverAgent(context.Background(), "codex", p)
	if got.Detection != "installed" || !strings.Contains(got.Evidence, "Application:") || strings.Contains(got.Evidence, "CLI executable:") || !strings.Contains(got.Note, "reload") {
		t.Fatalf("desktop-only Codex evidence misreported: %+v", got)
	}
	p.LookPath = func(name string) (string, error) {
		if name == "codex" {
			return "/opt/homebrew/bin/codex", nil
		}
		return "", os.ErrNotExist
	}
	got = DiscoverAgent(context.Background(), "codex", p)
	if !strings.Contains(got.Evidence, "Application:") || !strings.Contains(got.Evidence, "CLI executable:") {
		t.Fatalf("combined Codex evidence lost: %+v", got)
	}
}

func TestDiscoveryReportsOpenCodeJSONAndJSONCPrecedence(t *testing.T) {
	p, home := probeFixture(t)
	base := filepath.Join(home, ".config", "opencode")
	if err := os.MkdirAll(base, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"opencode.json", "opencode.jsonc"} {
		if err := os.WriteFile(filepath.Join(base, name), []byte("{}"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	got := DiscoverAgent(context.Background(), "opencode", p)
	if len(got.ConfigFiles) != 2 || !got.ConfigFiles[0].Exists || !got.ConfigFiles[1].Exists || got.ConfigFiles[0].Precedence != "lower" || got.ConfigFiles[1].Precedence != "higher" {
		t.Fatalf("effective JSONC precedence omitted: %+v", got.ConfigFiles)
	}
}

func TestDiscoverySeparatesDesktopAppFromCLIAndJetBrainsXML(t *testing.T) {
	p, home := probeFixture(t)
	apps := filepath.Join(home, "Applications")
	for _, app := range []string{"Claude.app", "IntelliJ IDEA 2026.2.0.1.app"} {
		if err := os.MkdirAll(filepath.Join(apps, app), 0700); err != nil {
			t.Fatal(err)
		}
	}
	claudeCode := DiscoverAgent(context.Background(), "claude", p)
	claudeDesktop := DiscoverAgent(context.Background(), "claude-desktop", p)
	if claudeCode.Detection != "not-detected" || claudeDesktop.Detection != "installed" {
		t.Fatalf("Claude Code and Desktop conflated: code=%+v desktop=%+v", claudeCode, claudeDesktop)
	}
	xml := filepath.Join(home, "Library", "Application Support", "JetBrains", "IntelliJIdea2026.2", "options", "llm.mcpServers.xml")
	if err := os.MkdirAll(filepath.Dir(xml), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(xml, []byte("<application/>"), 0600); err != nil {
		t.Fatal(err)
	}
	jetbrains := DiscoverAgent(context.Background(), "intellij", p)
	if jetbrains.Detection != "unverified" || jetbrains.Evidence == "" || len(jetbrains.ConfigFiles) != 1 {
		t.Fatalf("JetBrains IDE or XML settings were treated as plugin evidence: %+v", jetbrains)
	}
	if jetbrains.ConfigFiles[0].Path != xml || !jetbrains.ConfigFiles[0].Exists || jetbrains.ConfigFiles[0].Precedence != "metadata" {
		t.Fatalf("JetBrains IDE metadata path was not reported separately: %+v", jetbrains.ConfigFiles)
	}
	if !strings.Contains(strings.ToLower(jetbrains.Note), "no supported external config file path") {
		t.Fatalf("JetBrains discovery did not explain the unverified writer mechanism: %s", jetbrains.Note)
	}
}

func TestDiscoveryWindowsCLIUsesExecutableEvidenceAndUserConfigPaths(t *testing.T) {
	p, home := probeFixture(t)
	p.GOOS = "windows"
	p.LookPath = func(name string) (string, error) {
		if name == "codex" || name == "claude" || name == "opencode" {
			return filepath.Join(home, "bin", name+".exe"), nil
		}
		return "", os.ErrNotExist
	}
	for _, tc := range []struct {
		id, config string
	}{
		{"codex", filepath.Join(home, ".codex", "config.toml")},
		{"claude", filepath.Join(home, ".claude.json")},
		{"opencode", filepath.Join(home, ".config", "opencode", "opencode.json")},
	} {
		got := DiscoverAgent(context.Background(), tc.id, p)
		if got.Detection != "installed" || !strings.Contains(got.Evidence, "CLI executable:") || len(got.ConfigFiles) == 0 || got.ConfigFiles[0].Path != tc.config || got.ConfigFiles[0].Exists {
			t.Fatalf("%s Windows CLI discovery: %+v", tc.id, got)
		}
	}
}

func TestDiscoveryWindowsConfigFileAloneIsNotInstallEvidence(t *testing.T) {
	p, home := probeFixture(t)
	p.GOOS = "windows"
	config := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(config), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, []byte("model = 'demo'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	got := DiscoverAgent(context.Background(), "codex", p)
	if got.Detection != "not-detected" || len(got.ConfigFiles) != 1 || !got.ConfigFiles[0].Exists {
		t.Fatalf("Windows config was treated as installation: %+v", got)
	}
}

func TestDiscoveryWindowsCopilotCLIDetectsExecutableAndCOPILOTHomeConfig(t *testing.T) {
	p, home := probeFixture(t)
	p.GOOS = "windows"
	root := filepath.Join(home, "custom-copilot")
	p.Getenv = func(name string) string {
		if name == "COPILOT_HOME" {
			return root
		}
		return ""
	}
	p.LookPath = func(name string) (string, error) {
		if name == "copilot" {
			return filepath.Join(home, "bin", "copilot.exe"), nil
		}
		return "", os.ErrNotExist
	}
	got := DiscoverAgent(context.Background(), "copilot-cli", p)
	want := filepath.Join(root, "mcp-config.json")
	if got.Detection != "installed" || len(got.ConfigFiles) != 1 || got.ConfigFiles[0].Path != want {
		t.Fatalf("Windows Copilot CLI discovery failed: %+v", got)
	}
}

func TestDiscoveryWSLUsesLinuxCLIAndHomeOnly(t *testing.T) {
	p, home := probeFixture(t)
	p.GOOS = "linux"
	p.Getenv = func(name string) string {
		if name == "WSL_DISTRO_NAME" {
			return "Ubuntu"
		}
		return ""
	}
	p.LookPath = func(name string) (string, error) {
		if name == "opencode" {
			return "/usr/local/bin/opencode", nil
		}
		return "", os.ErrNotExist
	}
	got := DiscoverAgent(context.Background(), "opencode", p)
	if got.Detection != "installed" || got.ConfigFiles[0].Path != filepath.Join(home, ".config", "opencode", "opencode.json") || !strings.Contains(got.Note, "WSL") {
		t.Fatalf("WSL discovery escaped its Linux scope: %+v", got)
	}
}

func TestDiscoveryDoesNotCertifyUnverifiedWindowsClients(t *testing.T) {
	p, _ := probeFixture(t)
	p.GOOS = "windows"
	for _, id := range []string{"claude-desktop", "opencode-desktop", "intellij", "copilot-intellij"} {
		got := DiscoverAgent(context.Background(), id, p)
		if got.Detection != "unverified" || got.Note == "" {
			t.Fatalf("%s claimed Windows evidence: %+v", id, got)
		}
	}
}

func TestDiscoveryCopilotCLIUsesCOPILOTHomeAndExecutableEvidence(t *testing.T) {
	for _, goos := range []string{"darwin", "linux", "windows"} {
		t.Run(goos, func(t *testing.T) {
			p, home := probeFixture(t)
			p.GOOS = goos
			root := filepath.Join(home, "copilot-root")
			p.Getenv = func(name string) string {
				if name == "COPILOT_HOME" {
					return root
				}
				return ""
			}
			p.LookPath = func(name string) (string, error) {
				if name == "copilot" {
					return filepath.Join(home, "bin", "copilot"), nil
				}
				return "", os.ErrNotExist
			}
			got := DiscoverAgent(context.Background(), "copilot-cli", p)
			if got.Detection != "installed" || !strings.Contains(got.Evidence, "CLI executable:") || len(got.ConfigFiles) != 1 {
				t.Fatalf("Copilot CLI installation evidence incorrect: %+v", got)
			}
			if got.ConfigFiles[0].Path != filepath.Join(root, "mcp-config.json") {
				t.Fatalf("COPILOT_HOME not honored: %+v", got.ConfigFiles)
			}
		})
	}
}

func TestDiscoveryCopilotJetBrainsListsConfigWithoutClaimingPluginInstalled(t *testing.T) {
	p, home := probeFixture(t)
	path := filepath.Join(home, ".config", "github-copilot", "intellij", "mcp.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{\"servers\":{}}"), 0600); err != nil {
		t.Fatal(err)
	}
	got := DiscoverAgent(context.Background(), "copilot-intellij", p)
	if got.Detection != "unverified" || len(got.ConfigFiles) != 1 || !got.ConfigFiles[0].Exists || !strings.Contains(got.Note, "does not prove") {
		t.Fatalf("config presence was conflated with plugin installation: %+v", got)
	}
}

func TestDiscoveryWindowsJetBrainsFindsRoamingIDEStateAndCopilotConfig(t *testing.T) {
	p, home := probeFixture(t)
	p.GOOS = "windows"
	appData := filepath.Join(home, "roaming-data")
	p.Getenv = func(name string) string {
		if name == "APPDATA" {
			return appData
		}
		return ""
	}
	xml := filepath.Join(appData, "JetBrains", "IntelliJIdea2026.2", "options", "llm.mcpServers.xml")
	if err := os.MkdirAll(filepath.Dir(xml), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(xml, []byte("<application/>"), 0600); err != nil {
		t.Fatal(err)
	}
	got := DiscoverAgent(context.Background(), "intellij", p)
	if got.Detection != "unverified" || len(got.ConfigFiles) != 1 || got.ConfigFiles[0].Path != xml || !got.ConfigFiles[0].Exists {
		t.Fatalf("Windows JetBrains discovery failed: %+v", got)
	}
	copilot := DiscoverAgent(context.Background(), "copilot-intellij", p)
	want := filepath.Join(appData, "github-copilot", "intellij", "mcp.json")
	if copilot.Detection != "unverified" || len(copilot.ConfigFiles) != 1 || copilot.ConfigFiles[0].Path != want {
		t.Fatalf("Windows Copilot in JetBrains discovery failed: %+v", copilot)
	}
}

func TestDiscoveryDoesNotCallMissingIDEProofOfMissingAIAssistant(t *testing.T) {
	p, _ := probeFixture(t)
	got := DiscoverAgent(context.Background(), "intellij", p)
	if got.Detection != "unverified" {
		t.Fatalf("unverified plugin was reported absent: %+v", got)
	}
}
