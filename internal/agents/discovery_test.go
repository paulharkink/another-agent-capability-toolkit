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
	if jetbrains.Detection != "unverified" || jetbrains.Evidence == "" || len(jetbrains.ConfigFiles) != 1 || jetbrains.ConfigFiles[0].Path != xml || !jetbrains.ConfigFiles[0].Exists {
		t.Fatalf("JetBrains IDE or XML settings were treated as plugin evidence: %+v", jetbrains)
	}
}

func TestDiscoveryDoesNotCertifyUnverifiedWindowsPaths(t *testing.T) {
	p, _ := probeFixture(t)
	p.GOOS = "windows"
	for _, id := range []string{"codex", "claude", "intellij", "copilot-intellij", "copilot-cli"} {
		got := DiscoverAgent(context.Background(), id, p)
		if got.Detection != "unverified" || got.Note == "" {
			t.Fatalf("%s claimed Windows evidence: %+v", id, got)
		}
	}
}

func TestDiscoveryDoesNotCallMissingIDEProofOfMissingAIAssistant(t *testing.T) {
	p, _ := probeFixture(t)
	got := DiscoverAgent(context.Background(), "intellij", p)
	if got.Detection != "unverified" {
		t.Fatalf("unverified plugin was reported absent: %+v", got)
	}
}
