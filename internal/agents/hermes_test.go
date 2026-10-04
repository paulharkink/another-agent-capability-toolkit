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
	if _, err := For("hermes", nil); err == nil {
		t.Fatal("Hermes unexpectedly supports MCP registration")
	}
}
