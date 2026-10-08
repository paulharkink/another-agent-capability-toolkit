package agents

import (
	"context"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"os"
	"path/filepath"
	"testing"
)

func TestSkillAdapterInstallObserveAndRemove(t *testing.T) {
	for _, kind := range []string{"codex", "claude", "opencode", "hermes", "generic"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			store, err := state.Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			r := NewRegistry(Dependencies{Store: store, Probe: DiscoveryProbe{GOOS: "linux", Home: root, Getenv: func(string) string { return "" }, LookPath: func(name string) (string, error) { return "fixture-" + name, nil }}})
			a, err := r.Adapter(kind)
			if err != nil {
				t.Fatal(err)
			}
			manager, ok := a.(SkillManager)
			if !ok || !a.Features().Skills {
				t.Fatal("skill manager not implemented")
			}
			scope := Scope{Home: root, ID: kind, ExplicitHome: true}
			key := state.Key{Source: "pack", Package: "guidance", Target: "ota"}
			source := t.TempDir()
			if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("# Guidance"), 0600); err != nil {
				t.Fatal(err)
			}
			row, err := manager.InstallSkill(context.Background(), scope, SkillRequest{Key: key, Package: catalog.Package{Dir: source}, Skill: catalog.Skill{Name: "guidance", Source: source}})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Record(row); err != nil {
				t.Fatal(err)
			}
			observed, err := a.Observe(context.Background(), scope, ObservationRequest{Key: key, Managed: []state.Installation{row}})
			if err != nil || len(observed.Components) != 1 || observed.Components[0].Status != "installed" {
				t.Fatalf("installed observation: %#v %v", observed, err)
			}
			if err := os.WriteFile(filepath.Join(row.Destination, "SKILL.md"), []byte("# Changed"), 0600); err != nil {
				t.Fatal(err)
			}
			changed, err := a.Observe(context.Background(), scope, ObservationRequest{Key: key, Managed: []state.Installation{row}})
			if err != nil || changed.Components[0].Status != "modified" {
				t.Fatalf("changed managed skill reported intact: %#v %v", changed, err)
			}
			if err := os.RemoveAll(row.Destination); err != nil {
				t.Fatal(err)
			}
			observed, err = a.Observe(context.Background(), scope, ObservationRequest{Key: key, Managed: []state.Installation{row}})
			if err != nil || observed.Components[0].Status != "absent" {
				t.Fatalf("stale ledger falsely installed: %#v %v", observed, err)
			}
			if err := manager.RemoveSkill(context.Background(), scope, row); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestGenericAdapterOnlySharedSkills(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := NewRegistry(Dependencies{Store: store, Probe: DiscoveryProbe{GOOS: "linux", Home: root}})
	a, err := r.Adapter("all")
	if err != nil {
		t.Fatal(err)
	}
	if a.ID() != "generic" || !a.Features().Skills || a.Features().MCPs || len(a.Features().PluginFormats) > 0 {
		t.Fatalf("generic feature claims: %#v", a.Features())
	}
	if _, ok := a.(MCPManager); ok {
		t.Fatal("generic offers MCP management")
	}
	if _, ok := a.(PluginManager); ok {
		t.Fatal("generic offers plugins")
	}
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("# Generic"), 0600); err != nil {
		t.Fatal(err)
	}
	row, err := a.(SkillManager).InstallSkill(context.Background(), Scope{ID: "all", Home: root, ExplicitHome: true}, SkillRequest{Key: state.Key{Source: "pack", Package: "generic"}, Package: catalog.Package{Dir: src}, Skill: catalog.Skill{Name: "guidance"}})
	if err != nil || row.Destination != filepath.Join(root, ".agents", "skills", "guidance") || row.AgentID != "all" {
		t.Fatalf("shared path/alias: %#v %v", row, err)
	}
}

func TestSkillAdapterNativeLocationOverride(t *testing.T) {
	root := t.TempDir()
	xdg := t.TempDir()
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := NewRegistry(Dependencies{Store: store, Probe: DiscoveryProbe{GOOS: "linux", Home: root, LookPath: func(string) (string, error) { return "fixture-opencode", nil }, Getenv: func(name string) string {
		if name == "XDG_CONFIG_HOME" {
			return xdg
		}
		return ""
	}}})
	a, _ := r.Adapter("opencode")
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("# Guidance"), 0600); err != nil {
		t.Fatal(err)
	}
	row, err := a.(SkillManager).InstallSkill(context.Background(), Scope{}, SkillRequest{Key: state.Key{Source: "pack", Package: "guidance"}, Package: catalog.Package{Dir: source}, Skill: catalog.Skill{Name: "guidance"}})
	if err != nil || row.Destination != filepath.Join(xdg, "opencode", "skills", "guidance") {
		t.Fatalf("native skill override ignored: %#v %v", row, err)
	}
}
