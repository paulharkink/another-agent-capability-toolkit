package integration_test

import (
	"context"
	"fmt"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/agents"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/app"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/mcp"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Runtime is injected; all skill/config/state files are real isolated native files.
type integratedRuntime struct {
	store     *state.Store
	instances []mcp.Instance
}

func (r *integratedRuntime) Start(_ context.Context, key state.Key, spec mcp.RunSpec) (mcp.Instance, error) {
	i := mcp.Instance{Key: key, ID: key.ID(), Status: "running", Ownership: "local", URL: "http://127.0.0.1:39991/mcp"}
	found := false
	for n, old := range r.instances {
		if old.Key == key {
			r.instances[n] = i
			found = true
		}
	}
	if !found {
		r.instances = append(r.instances, i)
	}
	return i, r.store.Record(state.Installation{Key: key, Component: "runtime", URL: i.URL, Destination: i.ID})
}
func (r *integratedRuntime) Stop(_ context.Context, key state.Key) error {
	for n, i := range r.instances {
		if i.Key == key {
			r.instances[n].Status = "stopped"
		}
	}
	return nil
}
func (r *integratedRuntime) List(context.Context) ([]mcp.Instance, error) { return r.instances, nil }
func (r *integratedRuntime) Logs(context.Context, state.Key) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("native fixture logs")), nil
}

func TestCapabilityProfilesIntegratedImportApplyObserveRemove(t *testing.T) {
	ctx := context.Background()
	root, _ := filepath.Abs("../../examples/capability-pack")
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pack, err := config.DiscoverPack(root, filepath.Join(root, "aact.toml"), "", store.Root())
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	probe := agents.DiscoveryProbe{Home: home, GOOS: "linux", Getenv: func(string) string { return "" }, LookPath: func(name string) (string, error) {
		if name == "opencode" {
			return "injected-detection-evidence", nil
		}
		return "", fmt.Errorf("not installed")
	}}
	rt := &integratedRuntime{store: store}
	svc := app.New(pack.LegacySource(), store, app.Options{Runtime: rt, DiscoveryProbe: &probe, AgentScopes: map[string]agents.Scope{"opencode": {ID: "opencode", Home: home, ExplicitHome: true}}})
	ref := config.ProfileRef{PackID: pack.ID, CapabilityID: "company-guidance", Name: "ota"}
	before, err := svc.ProfileSnapshot(ctx, ref.CapabilityID)
	if err != nil || len(before.Profiles) != 2 {
		t.Fatalf("discovery: %+v %v", before, err)
	}
	q := app.ProfileRequest{Ref: ref, DestinationIDs: []string{"opencode"}, ItemIDs: []string{"set:reference", "skill:review"}, Inputs: map[string]any{"project": "INVALID", "enabled": false}}
	out, err := svc.ApplyProfile(ctx, q)
	if err != nil || len(out.Changes) != 3 {
		t.Fatalf("apply: %+v %v", out, err)
	}
	for _, name := range []string{"guide", "review"} {
		data, err := os.ReadFile(filepath.Join(home, ".config", "opencode", "skills", name, "SKILL.md"))
		if err != nil || !strings.Contains(string(data), "example-project") {
			t.Fatalf("rendered %s: %s %v", name, data, err)
		}
	}
	snapshot, err := svc.ProfileSnapshot(ctx, ref.CapabilityID)
	if err != nil {
		t.Fatal(err)
	}
	row := snapshot.Profiles[0]
	if row.ConfigStatus != "configured" || len(row.Components) != 3 || len(row.MCPs) != 1 || row.MCPs[0].Status != "running" {
		t.Fatalf("actual snapshot: %+v", row)
	}
	for _, c := range row.Components {
		if c.Status != "installed" {
			t.Fatalf("not observed: %+v", c)
		}
	}
	// Deselect the independent review skill, keeping linked guide + MCP together.
	q.ItemIDs = []string{"set:reference"}
	if _, err := svc.ApplyProfile(ctx, q); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "opencode", "skills", "review")); !os.IsNotExist(err) {
		t.Fatalf("deselected skill remains: %v", err)
	}
	// Unrelated user config/skills survive removal.
	unrelated := filepath.Join(home, ".config", "opencode", "skills", "other")
	os.MkdirAll(unrelated, 0700)
	os.WriteFile(filepath.Join(unrelated, "SKILL.md"), []byte("untouched"), 0600)
	if _, err := svc.RemoveProfile(ctx, app.ProfileRequest{Ref: ref}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(unrelated, "SKILL.md")); err != nil {
		t.Fatal("unrelated skill removed:", err)
	}
	snapshot, err = svc.ProfileSnapshot(ctx, ref.CapabilityID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Profiles[0].MCPs[0].Status != "stopped" {
		t.Fatalf("runtime removal: %+v", snapshot.Profiles[0])
	}
	// Local profiles have editable inputs, unlike these pack-fixed project names.
	q.Ref.Name = "local"
	if err := svc.CreateProfile(ctx, q.Ref); err != nil {
		t.Fatal(err)
	}
	q.Inputs = map[string]any{"project": "BAD VALUE", "enabled": false}
	prior := len(rt.instances)
	if _, err := svc.ApplyProfile(ctx, q); err == nil || !strings.Contains(err.Error(), "BAD VALUE") || len(rt.instances) != prior {
		t.Fatalf("regex acceptance: %v", err)
	}
	q.Inputs = map[string]any{"project": "local-team", "enabled": false}
	q.SkillsOnly = true
	if _, err := svc.ApplyProfile(ctx, q); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(pack.ProfileRoot, ref.CapabilityID, "local.toml")); !os.IsNotExist(err) {
		t.Fatal("wrote pack profile")
	}
}
