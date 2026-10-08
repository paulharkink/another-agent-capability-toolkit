package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/agents"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/process"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
)

type hermesTestExecutor struct{ calls int }

func (e *hermesTestExecutor) Run(context.Context, []string, string, []byte, map[string]string, func([]byte)) ([]byte, error) {
	e.calls++
	return nil, nil
}

var _ process.Executor = (*hermesTestExecutor)(nil)

func TestSkillsOnlyInstallsBundledClusterInspectorWithoutMCPEffects(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := catalog.Load(filepath.Join(repoRoot, "packages", "cluster-inspector"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	store, err := state.Open(filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	hermesHome := filepath.Join(root, "hermes-home")
	service := New(config.Source{
		ID:              "bundled",
		Root:            repoRoot,
		ManifestPath:    filepath.Join(repoRoot, "aact.toml"),
		Catalog:         []catalog.Package{pkg},
		PackageDefaults: map[string]map[string]any{},
	}, store, Options{})
	runtime := &fakeRuntime{}
	executor := &hermesTestExecutor{}
	service.Options.Runtime = runtime
	service.Options.Runner = executor
	service.Options.DiscoveryProbe = &agents.DiscoveryProbe{
		GOOS: "linux", Home: root, Getenv: func(string) string { return "" },
		LookPath: func(name string) (string, error) {
			if name == "hermes" {
				return filepath.Join(root, "bin", "hermes"), nil
			}
			return "", os.ErrNotExist
		},
	}

	ref := config.ProfileRef{PackID: "bundled", CapabilityID: "cluster-inspector", Name: "default"}
	ensureProfileForTest(service, ref)
	service.Options.AgentScopes = map[string]agents.Scope{"hermes": {ID: "hermes", Home: hermesHome, ExplicitHome: true}}
	result, err := service.ApplyProfile(context.Background(), ProfileRequest{
		Ref: ref, Inputs: map[string]any{
			"registration_name": "cluster-inspector",
			"api_server":        "https://fixture.invalid",
		},
		DestinationIDs: []string{"hermes"}, SkillsOnly: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	installed := filepath.Join(hermesHome, ".hermes", "skills", "cluster-inspector", "SKILL.md")
	if body, err := os.ReadFile(installed); err != nil || !strings.Contains(string(body), "# Cluster Inspector") {
		t.Fatalf("rendered bundled skill not installed at %s: %v", installed, err)
	}
	if runtime.starts != 0 {
		t.Fatalf("skills-only install started MCP runtime %d times", runtime.starts)
	}
	if executor.calls != 0 {
		t.Fatalf("skills-only install invoked package subprocess %d times", executor.calls)
	}
	rows, err := store.Installations()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Component != "skill" || rows[0].AgentKind != "hermes" {
		t.Fatalf("expected only Hermes skill installation state, got %#v", rows)
	}
	profiles, err := store.Profiles()
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 1 || profiles[0].Key.Profile != "" || profiles[0].Key.MCP != "" || profiles[0].Selection == nil {
		t.Fatalf("skills-only intent should be saved once at capability profile scope, without MCP child rows: %#v", profiles)
	}
	if result.Saved != true || len(result.Changes) != 1 {
		t.Fatalf("unexpected skill-only result: %+v", result)
	}
}

func TestSkillsOnlyRejectsPackageWithoutSkill(t *testing.T) {
	service, env, store := fixture(t)
	service.Source.Catalog[0].Skill = nil
	service.Source.Catalog[0].MCP = &catalog.MCP{Name: "demo", Transport: "streamable-http"}
	ref := config.ProfileRef{PackID: service.Source.ID, CapabilityID: "demo", Name: "default"}
	ensureProfileForTest(service, ref)
	service.Options.AgentScopes = map[string]agents.Scope{"codex": {ID: "codex", Home: env.Home, ExplicitHome: true}}
	_, err := service.ApplyProfile(context.Background(), ProfileRequest{
		Ref: ref, DestinationIDs: []string{"codex"}, SkillsOnly: true,
	})
	if err == nil || !strings.Contains(err.Error(), "no skill") {
		t.Fatalf("skills-only install should reject an MCP-only package, got %v", err)
	}
	rows, err := store.Installations()
	if err != nil || len(rows) != 0 {
		t.Fatalf("rejected install wrote installation state: %#v, %v", rows, err)
	}
}
