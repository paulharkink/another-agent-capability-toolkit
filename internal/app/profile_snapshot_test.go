package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/agents"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/mcp"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
	"os"
	"path/filepath"
	"reflect"
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
func TestObservedInstanceRuntimeDuplicateRowsRemainChildScopedAndStale(t *testing.T) {
	s, _, q := profileApplyFixture(t)
	k, _ := s.Store.ResolveProfileKey(q.Ref.PackID, q.Ref.CapabilityID, q.Ref.Name)
	child := mcpDefinitionKey(k, s.Source.Catalog[0], s.Source.Catalog[0].MCPs[0])
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
	if err != nil || snap.Profiles[0].MCPs[0].Status != "running" || len(snap.Profiles[0].MCPs[0].RuntimeRecords) != 2 || snap.Profiles[0].MCPs[0].Ownership != "other-aact" {
		t.Fatalf("%+v %v", snap, err)
	}
}

func TestCapabilityInstanceSnapshotKeepsDuplicateMissingMCPRuntimesRecoverable(t *testing.T) {
	s, _, q := profileApplyFixture(t)
	p := &s.Source.Catalog[0]
	p.MCP = nil
	p.MCPs = []catalog.MCP{{Name: "grafana-inspector", Image: "fixture", Transport: "streamable-http"}}
	key, err := s.Store.ResolveInstanceKey(q.Ref.PackID, q.Ref.CapabilityID, q.Ref.Name)
	if err != nil {
		t.Fatal(err)
	}
	childKey := mcpDefinitionKey(key, *p, p.MCPs[0])
	for _, runtimeID := range []string{"stale-container-a", "stale-container-b"} {
		if err := s.Store.Record(state.Installation{Key: childKey, AgentID: "docker", Component: "runtime", Destination: "aact-grafana", SourcePath: runtimeID, Mode: "docker"}); err != nil {
			t.Fatal(err)
		}
	}
	orphanKey := childKey
	orphanKey.MCP = "removed-definition"
	if err := s.Store.Record(state.Installation{Key: orphanKey, AgentID: "docker", Component: "runtime", Destination: "aact-orphan", SourcePath: "orphan-container", Mode: "docker"}); err != nil {
		t.Fatal(err)
	}
	s.Options.Runtime = &snapshotRuntime{instances: []mcp.Instance{
		{Key: childKey, ID: "stale-container-a", Name: "aact-grafana-a", Status: "missing", Ownership: "local"},
		{Key: childKey, ID: "stale-container-b", Name: "aact-grafana-b", Status: "missing", Ownership: "local"},
	}}

	snapshot, err := s.InstanceSnapshot(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	instanceIndex := -1
	for i := range snapshot.Instances {
		if snapshot.Instances[i].Key == key {
			instanceIndex = i
			break
		}
	}
	if instanceIndex < 0 {
		t.Fatalf("configured CapabilityInstance %s is absent: %+v", key.Target, snapshot.Instances)
	}
	instance := snapshot.Instances[instanceIndex]
	if instance.ConfigStatus == "conflict" || len(instance.MCPs) != 1 {
		t.Fatalf("stale child records were promoted to a parent conflict or hid the declared MCP: %+v", instance)
	}
	if got := instance.MCPs[0].Status; got != "missing" {
		t.Fatalf("duplicate missing runtime rows should stay recoverable under the declared MCP, status=%q error=%q", got, instance.MCPs[0].Error)
	}
	childValue := reflect.ValueOf(instance.MCPs[0])
	runtimeRecords := childValue.FieldByName("RuntimeRecords")
	if !runtimeRecords.IsValid() || runtimeRecords.Len() != 2 {
		t.Fatalf("declared MCP child must expose both runtime records, got field=%v", runtimeRecords)
	}
	if got := runtimeRecords.Index(0).FieldByName("ID").String(); got != "stale-container-a" {
		t.Fatalf("first runtime identity = %q, want stale-container-a", got)
	}
	if got := runtimeRecords.Index(1).FieldByName("ID").String(); got != "stale-container-b" {
		t.Fatalf("second runtime identity = %q, want stale-container-b", got)
	}
	rows, err := s.Store.Installations()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("runtime metadata for undefined MCP was not reconciled without touching linked missing records: %+v", rows)
	}
}

func TestCapabilityInstanceSnapshotDoesNotCrossLinkRuntimeBySharedURL(t *testing.T) {
	s, _, q := profileApplyFixture(t)
	p := &s.Source.Catalog[0]
	p.MCP = nil
	p.MCPs = []catalog.MCP{
		{Name: "first-server", Image: "fixture", Transport: "streamable-http"},
		{Name: "second-server", Image: "fixture", Transport: "streamable-http"},
	}
	key, err := s.Store.ResolveInstanceKey(q.Ref.PackID, q.Ref.CapabilityID, q.Ref.Name)
	if err != nil {
		t.Fatal(err)
	}
	firstKey := mcpDefinitionKey(key, *p, p.MCPs[0])
	secondKey := mcpDefinitionKey(key, *p, p.MCPs[1])
	sharedURL := "http://localhost:9911/mcp"
	for _, child := range []state.Key{firstKey, secondKey} {
		if err := s.Store.Record(state.Installation{
			Key: child, AgentID: "codex", Component: "mcp", Destination: "/tmp/config.toml",
			RegistrationName: child.MCPDefinition, URL: sharedURL, ExternalRegistration: true,
		}); err != nil {
			t.Fatal(err)
		}
	}
	s.Options.Runtime = &snapshotRuntime{instances: []mcp.Instance{{
		Key: firstKey, ID: "first-container", Status: "running", Ownership: "local", URL: sharedURL,
	}}}

	snapshot, err := s.InstanceSnapshot(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	var first, second *viewmodel.RuntimeStatus
	for i := range snapshot.Instances {
		if snapshot.Instances[i].Key != key {
			continue
		}
		for j := range snapshot.Instances[i].MCPs {
			child := &snapshot.Instances[i].MCPs[j]
			switch child.MCPID {
			case "first-server":
				first = child
			case "second-server":
				second = child
			}
		}
	}
	if first == nil || len(first.RuntimeRecords) != 1 || first.RuntimeRecords[0].ID != "first-container" {
		t.Fatalf("exact-key child lost its runtime: %+v", first)
	}
	if second == nil || len(second.RuntimeRecords) != 0 {
		t.Fatalf("runtime was cross-linked to sibling with the same registration URL: %+v", second)
	}
}

func TestCapabilityInstanceSnapshotReconcilesLedgerWhenLiveIDHasDifferentKey(t *testing.T) {
	s, _, q := profileApplyFixture(t)
	p := &s.Source.Catalog[0]
	p.MCP = nil
	p.MCPs = []catalog.MCP{
		{Name: "first-server", Image: "fixture", Transport: "streamable-http"},
		{Name: "second-server", Image: "fixture", Transport: "streamable-http"},
	}
	key, err := s.Store.ResolveInstanceKey(q.Ref.PackID, q.Ref.CapabilityID, q.Ref.Name)
	if err != nil {
		t.Fatal(err)
	}
	firstKey := mcpDefinitionKey(key, *p, p.MCPs[0])
	secondKey := mcpDefinitionKey(key, *p, p.MCPs[1])
	ledger := state.Installation{Key: firstKey, AgentID: "docker", Component: "runtime", Destination: "aact-first", SourcePath: "same-live-id", Mode: "docker"}
	if err := s.Store.Record(ledger); err != nil {
		t.Fatal(err)
	}
	runtime := &exactProfileRuntime{profileRuntime: profileRuntime{instances: []mcp.Instance{{
		Key: secondKey, ID: ledger.SourcePath, Name: "aact-second", Status: "running", Ownership: "local", URL: "http://localhost:9912/mcp",
	}}}, store: s.Store}
	s.Options.Runtime = runtime

	snapshot, err := s.InstanceSnapshot(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	var first, second *viewmodel.RuntimeStatus
	for i := range snapshot.Instances {
		if snapshot.Instances[i].Key != key {
			continue
		}
		for j := range snapshot.Instances[i].MCPs {
			child := &snapshot.Instances[i].MCPs[j]
			switch child.MCPID {
			case "first-server":
				first = child
			case "second-server":
				second = child
			}
		}
	}
	if first == nil || len(first.RuntimeRecords) != 0 {
		t.Fatalf("mis-keyed runtime remained actionable under the old child: %+v", first)
	}
	if second == nil || len(second.RuntimeRecords) != 1 || second.RuntimeRecords[0].ID != ledger.SourcePath {
		t.Fatalf("live runtime was not visible under its observed MCP key: %+v", second)
	}
	rows, err := s.Store.Installations()
	if err != nil || len(rows) != 1 || rows[0].Key != secondKey || rows[0].SourcePath != ledger.SourcePath || rows[0].Destination != "aact-second" || rows[0].URL != "http://localhost:9912/mcp" {
		t.Fatalf("live runtime ledger metadata was not rekeyed to its observed MCP: %+v, %v", rows, err)
	}
	if _, err := s.UIRemoveRuntimeRecord(context.Background(), secondKey, ledger.SourcePath); err != nil {
		t.Fatalf("visible runtime row could not remove its exact runtime after reconciliation: %v", err)
	}
	if !reflect.DeepEqual(runtime.removedIDs, []string{ledger.SourcePath}) {
		t.Fatalf("visible row removed unexpected runtime IDs: %v", runtime.removedIDs)
	}
	rows, err = s.Store.Installations()
	if err != nil || len(rows) != 0 {
		t.Fatalf("exact runtime deletion left ledger metadata: %+v, %v", rows, err)
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
