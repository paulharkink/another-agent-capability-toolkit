package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/agents"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
)

func TestGlobalSkillDestinationInstallsAtSharedAgentSkillsPath(t *testing.T) {
	svc, _, store := fixture(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	global, err := agents.GlobalSkillsEnvironment(home)
	if err != nil {
		t.Fatal(err)
	}
	if global.ID != "all" || global.SkillsDir != filepath.Join(home, ".agents", "skills") || global.ConfigPath != "" {
		t.Fatalf("unexpected global environment: %+v", global)
	}
	if _, err := svc.Install(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{global}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".agents", "skills", "demo", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	rows, err := store.Installations()
	if err != nil || len(rows) != 1 || rows[0].AgentID != "all" {
		t.Fatalf("global installation not recorded: %+v, %v", rows, err)
	}
	ids, err := svc.UIAgents(context.Background())
	if err != nil || len(ids) == 0 || ids[0] != "all" {
		t.Fatalf("all is not first destination: %v, %v", ids, err)
	}
	claudeAvailable := false
	for _, id := range ids {
		if id == "claude" {
			claudeAvailable = true
		}
	}
	if !claudeAvailable {
		t.Fatalf("named claude agent missing: %v", ids)
	}
	if _, err := svc.UIRun(context.Background(), "uninstall", "fixture", "demo", "all", "", "default"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(home, ".agents", "skills", "demo")); !os.IsNotExist(err) {
		t.Fatalf("global skill remains after uninstall: %v", err)
	}
}

func TestGlobalDestinationRejectsMCPWithoutStartingRuntime(t *testing.T) {
	svc, _, store := fixture(t)
	home := t.TempDir()
	global, err := agents.GlobalSkillsEnvironment(home)
	if err != nil {
		t.Fatal(err)
	}
	svc.Source.Catalog[0].MCP = &catalog.MCP{Name: "demo", Image: "fixture", Transport: "streamable-http"}
	runtime := &fakeRuntime{}
	svc.Options.Runtime = runtime
	if _, err := svc.Install(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{global}}); err == nil {
		t.Fatal("global destination accepted an MCP package")
	}
	if runtime.starts != 0 {
		t.Fatal("runtime started before rejecting global destination")
	}
	if rows, err := store.Installations(); err != nil || len(rows) != 0 {
		t.Fatalf("partial installation: %+v, %v", rows, err)
	}
	if _, err := os.Lstat(filepath.Join(home, ".agents", "skills", "demo")); !os.IsNotExist(err) {
		t.Fatalf("skill installed despite MCP rejection: %v", err)
	}
}

func TestGlobalAndCodexCanShareSkillDestination(t *testing.T) {
	svc, _, store := fixture(t)
	home := t.TempDir()
	global, err := agents.GlobalSkillsEnvironment(home)
	if err != nil {
		t.Fatal(err)
	}
	codex, err := agents.ResolveEnvironment("codex", "codex", home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Install(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{global, codex}}); err != nil {
		t.Fatal(err)
	}
	rows, err := store.Installations()
	if err != nil || len(rows) != 2 {
		t.Fatalf("shared destinations lack separate ownership: %+v, %v", rows, err)
	}
	if _, err := svc.Uninstall(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{global}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".agents", "skills", "demo", "SKILL.md")); err != nil {
		t.Fatalf("removing all broke codex reference: %v", err)
	}
	if _, err := svc.Uninstall(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{codex}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(home, ".agents", "skills", "demo")); !os.IsNotExist(err) {
		t.Fatalf("last owner did not remove skill: %v", err)
	}
}
