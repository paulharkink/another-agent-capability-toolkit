package agents

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIntelliJRegistrationIsDisabledWithoutSupportedExternalConfig(t *testing.T) {
	home := t.TempDir()
	configRoot := filepath.Join(home, "Library", "Application Support", "JetBrains", "IntelliJIdea2026.2")
	xml := filepath.Join(configRoot, "options", "llm.mcpServers.xml")
	if err := os.MkdirAll(filepath.Dir(xml), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(xml, []byte("<application><component name=\"McpApplicationServerCommands\"><commands/><urls/></component></application>"), 0600); err != nil {
		t.Fatal(err)
	}
	probe := DiscoveryProbe{GOOS: "darwin", Home: home, AppRoots: []string{filepath.Join(home, "Applications")}, LookPath: func(string) (string, error) { return "", os.ErrNotExist }, Getenv: func(string) string { return "" }}
	if err := os.MkdirAll(filepath.Join(probe.AppRoots[0], "IntelliJ IDEA.app"), 0700); err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry(Dependencies{Probe: probe})
	adapter, err := registry.Adapter("intellij")
	if err != nil {
		t.Fatal(err)
	}
	features := adapter.Features()
	if !features.Skills || features.MCPs {
		t.Fatalf("IntelliJ capabilities = %+v, want skills enabled and MCP disabled", features)
	}
	if _, ok := adapter.(MCPManager); ok {
		t.Fatal("IntelliJ adapter exposes MCP registration despite no verified external configuration mechanism")
	}
	detection, err := adapter.Detect(context.Background(), Scope{Home: home, ExplicitHome: true})
	if err != nil {
		t.Fatal(err)
	}
	if detection.ConfigPath != "" || detection.CanCreateConfig || detection.MCPDisabledReason == "" || !strings.Contains(strings.ToLower(detection.MCPDisabledReason), "verified") {
		t.Fatalf("IntelliJ registration detection advertises an unverified write path: %+v", detection)
	}
	if len(detection.ConfigFiles) != 1 || detection.ConfigFiles[0].Path != xml || detection.ConfigFiles[0].Precedence != "metadata" {
		t.Fatalf("IntelliJ native settings evidence = %+v, want XML metadata only", detection.ConfigFiles)
	}
}
