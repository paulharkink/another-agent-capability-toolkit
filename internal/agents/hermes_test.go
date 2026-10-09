package agents

import (
	"path/filepath"
	"testing"
)

func TestResolveHermesEnvironmentUsesHermesSkillDirectory(t *testing.T) {
	home := t.TempDir()
	env, err := ResolveEnvironment("hermes", "hermes", home)
	if err != nil {
		t.Fatal(err)
	}
	if env.SkillsDir != filepath.Join(home, ".hermes", "skills") {
		t.Fatalf("Hermes skills directory = %q", env.SkillsDir)
	}
	if env.ConfigPath != "" {
		t.Fatalf("Hermes must not expose an MCP config path, got %q", env.ConfigPath)
	}
	adapter, err := NewRegistry(Dependencies{}).Adapter("hermes")
	if err != nil {
		t.Fatal(err)
	}
	if adapter.Features().MCPs {
		t.Fatal("Hermes unexpectedly supports MCP registration")
	}
	if _, ok := adapter.(MCPManager); ok {
		t.Fatal("Hermes unexpectedly exposed an MCP manager")
	}
}
