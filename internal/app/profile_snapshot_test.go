package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/agents"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/mcp"
	"os"
	"path/filepath"
	"testing"
)

func TestCapabilityProfileSnapshotDiscoveryReadiness(t *testing.T) {
	s, _, q := profileApplyFixture(t)
	snapshot, err := s.ProfileSnapshot(context.Background(), "demo")
	if err != nil || len(snapshot.Profiles) != 1 || snapshot.Profiles[0].ConfigStatus != "configured" || len(snapshot.Profiles[0].MCPs) != 2 || snapshot.Profiles[0].MCPs[0].Status != "stopped" {
		t.Fatalf("%+v %v", snapshot, err)
	}
	q.Ref.Name = "extra"
	if err = s.CreateProfile(context.Background(), q.Ref); err != nil {
		t.Fatal(err)
	}
	snapshot, err = s.ProfileSnapshot(context.Background(), "demo")
	if err != nil || len(snapshot.Profiles) != 2 || snapshot.Profiles[0].ConfigStatus != "needs-input" {
		t.Fatalf("%+v %v", snapshot, err)
	}
}
func TestObservedProfileDetectsDeletedSkillAndRegistration(t *testing.T) {
	s, _, q := profileApplyFixture(t)
	p := &s.Source.Catalog[0]
	p.Skills = p.Skills[:1]
	p.MCPs = p.MCPs[:1]
	p.Sets = []catalog.ComponentSet{{Name: "core", Skills: []string{"one"}, MCPs: []string{"alpha"}}}
	home := t.TempDir()
	s.Options.Adapters = agents.NewRegistry(agents.Dependencies{Store: s.Store, Probe: agents.DiscoveryProbe{GOOS: "linux", Home: home, LookPath: func(name string) (string, error) {
		if name == "opencode" {
			return "fixture", nil
		}
		return "", fmt.Errorf("absent")
	}, Getenv: func(string) string { return "" }}})
	s.Options.AgentScopes = map[string]agents.Scope{"opencode": {ID: "opencode", Home: home, ExplicitHome: true}}
	q.DestinationIDs = []string{"opencode"}
	q.ExternalURLs = map[string]string{"alpha": "http://localhost:9001/mcp"}
	out, err := s.ApplyProfile(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := s.ProfileSnapshot(context.Background(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range snap.Profiles[0].Components {
		if c.Status != "installed" {
			t.Fatalf("not observed installed: %+v", c)
		}
		if c.Kind == "skill" && !c.Managed {
			t.Fatalf("profile-owned skill lost managed provenance: %+v", c)
		}
	}
	for _, row := range out.Changes {
		if row.Component == "skill" {
			os.Remove(row.Destination)
		} else {
			os.WriteFile(row.Destination, []byte(`{"mcp":{}}`), 0600)
		}
	}
	snap, err = s.ProfileSnapshot(context.Background(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range snap.Profiles[0].Components {
		if c.Status != "absent" {
			t.Fatalf("ledger hid deleted component: %+v", c)
		}
	}
}

type snapshotRuntime struct {
	fakeRuntime
	instances []mcp.Instance
	err       error
}

func (r *snapshotRuntime) List(context.Context) ([]mcp.Instance, error) { return r.instances, r.err }
func TestObservedProfileRuntimeOwnershipConflictAndStale(t *testing.T) {
	s, _, q := profileApplyFixture(t)
	k, _ := s.Store.ResolveProfileKey(q.Ref.PackID, q.Ref.CapabilityID, q.Ref.Name)
	child := mcpProfileKey(k, s.Source.Catalog[0], s.Source.Catalog[0].MCPs[0])
	r := &snapshotRuntime{instances: []mcp.Instance{{Key: child, Status: "running", Ownership: "other-aact", URL: "http://localhost:9001/mcp"}}}
	s.Options.Runtime = r
	snap, err := s.ProfileSnapshot(context.Background(), "demo")
	if err != nil || snap.Profiles[0].MCPs[0].Ownership != "other-aact" {
		t.Fatalf("%+v %v", snap, err)
	}
	r.err = errors.New("Docker socket unavailable")
	snap, err = s.ProfileSnapshot(context.Background(), "demo")
	if err != nil || !snap.Profiles[0].MCPs[0].Stale || snap.Profiles[0].MCPs[0].Status != "unavailable" || snap.Profiles[0].MCPs[0].Ownership != "unknown" || len(snap.Errors) == 0 {
		t.Fatalf("%+v %v", snap, err)
	}
	r.err = nil
	r.instances = append(r.instances, r.instances[0])
	snap, err = s.ProfileSnapshot(context.Background(), "demo")
	if err != nil || snap.Profiles[0].MCPs[0].Status != "conflict" {
		t.Fatalf("%+v %v", snap, err)
	}
}
func TestCapabilityProfileSnapshotInvalidFileStaysVisible(t *testing.T) {
	s, _, _ := profileApplyFixture(t)
	os.WriteFile(filepath.Join(s.Source.ProfileRoot, "demo", "broken.toml"), []byte("???"), 0600)
	snap, err := s.ProfileSnapshot(context.Background(), "demo")
	if err != nil || len(snap.Profiles) != 2 || snap.Profiles[0].ConfigStatus != "invalid" || snap.Profiles[0].ConfigError == "" {
		t.Fatalf("%+v %v", snap, err)
	}
	rows, _ := s.Store.Installations()
	if len(rows) != 0 {
		t.Fatal("observation changed state")
	}
}

func TestProfileSnapshotDistinguishesUnmanagedInventoryFromProfileEffects(t *testing.T) {
	s, _, q := profileApplyFixture(t)
	q.Ref.Name = "sibling"
	if err := s.CreateProfile(context.Background(), q.Ref); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	s.Options.Adapters = agents.NewRegistry(agents.Dependencies{Store: s.Store, Probe: agents.DiscoveryProbe{GOOS: "linux", Home: home, LookPath: func(name string) (string, error) {
		if name == "opencode" {
			return "fixture-opencode", nil
		}
		return "", fmt.Errorf("not installed")
	}, Getenv: func(string) string { return "" }}})
	s.Options.AgentScopes = map[string]agents.Scope{"opencode": {ID: "opencode", Home: home, ExplicitHome: true}}
	path := filepath.Join(home, ".config", "opencode", "skills", "one")
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "SKILL.md"), []byte("# External skill\n"), 0600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.ProfileSnapshot(context.Background(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Profiles) != 2 {
		t.Fatalf("profiles = %+v", snapshot.Profiles)
	}
	for _, profile := range snapshot.Profiles {
		found := false
		for _, component := range profile.Components {
			if component.AgentID == "opencode" && component.Kind == "skill" && component.Name == "one" {
				if component.Status != "installed" || component.Managed {
					t.Fatalf("inventory was attributed to profile %q: %+v", profile.Ref.Name, component)
				}
				found = true
			}
		}
		if !found {
			t.Fatalf("profile %q omitted external installed skill", profile.Ref.Name)
		}
	}
}
