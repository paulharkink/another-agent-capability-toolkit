package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/agents"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
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

func TestPlainSkillSnapshotAlwaysContainsPackOwnedDefaultInstance(t *testing.T) {
	s, _, _ := fixture(t)
	s.Source.InstanceRoot = filepath.Join(t.TempDir(), "instances")
	if err := os.MkdirAll(filepath.Join(s.Source.InstanceRoot, "demo"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Source.InstanceRoot, "demo", "legacy.toml"), []byte("[inputs]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	legacyKey := state.Key{Source: s.Source.ID, Package: "demo", Target: "legacy"}
	if err := s.Store.RecordInstance(state.InstanceRecord{Key: legacyKey, Name: "legacy", Local: true}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.InstanceSnapshot(context.Background(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Instances) != 1 || snapshot.Instances[0].Ref.PackID != s.Source.ID ||
		snapshot.Instances[0].Ref.CapabilityID != "demo" || snapshot.Instances[0].Ref.Name != "default" {
		t.Fatalf("plain skill must resolve through its Pack-owned default Instance: %+v", snapshot.Instances)
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

func TestCapabilityInstanceSnapshotProjectsRunningForeignCustomParentReadOnly(t *testing.T) {
	s, adapter, q := profileApplyFixture(t)
	p := s.Source.Catalog[0]
	parent := state.Key{Source: s.Source.ID, Package: p.ID, Environment: "custom-env", Target: "custom-target"}
	definition := p.MCPDefinitions()[0]
	child := mcpDefinitionKey(parent, p, definition)
	endpoint := "http://localhost:18770/mcp"
	runtime := &snapshotRuntime{instances: []mcp.Instance{{
		Key: child, ID: "foreign-live", Name: "aact-foreign", Status: "running", Ownership: "other-aact",
		RegistrationName: "alpha-remote", URL: endpoint,
	}}}
	s.Options.Runtime = runtime
	managedSkill := state.Installation{Key: parent, AgentID: adapter.ID(), AgentKind: adapter.ID(), Component: "skill", Destination: filepath.Join(adapter.home, "skills", "one"), Mode: "symlink"}
	managedMCP := state.Installation{Key: child, AgentID: adapter.ID(), AgentKind: adapter.ID(), Component: "mcp", Destination: filepath.Join(adapter.home, "config.toml"), RegistrationName: "alpha-remote", URL: endpoint, Transport: "streamable-http", Mode: "registration"}
	for _, row := range []state.Installation{managedSkill, managedMCP} {
		if err := s.Store.Record(row); err != nil {
			t.Fatal(err)
		}
	}
	observedRequests := 0
	adapter.observe = func(request agents.ObservationRequest) agents.Observation {
		if request.Key != parent {
			return agents.Observation{}
		}
		if !request.IncludeInventory {
			t.Errorf("foreign observation must inspect current native agent inventory: %+v", request)
		}
		observedRequests++
		if len(request.Managed) == 0 {
			return agents.Observation{Detection: agents.Detection{State: "installed", Installed: true}}
		}
		if len(request.Managed) != 2 {
			t.Errorf("foreign observation used the wrong local binding scope: managed=%+v", request.Managed)
		}
		return agents.Observation{Detection: agents.Detection{State: "installed", Installed: true}, Components: []agents.ComponentObservation{
			{Kind: "skill", Name: "one", Path: managedSkill.Destination, Status: "installed", Managed: true},
			{Kind: "mcp", Name: "alpha-remote", RegistrationName: "alpha-remote", URL: endpoint, Transport: "streamable-http", Status: "installed", Managed: true},
		}}
	}

	snapshot, err := s.InstanceSnapshot(context.Background(), q.Ref.CapabilityID)
	if err != nil {
		t.Fatal(err)
	}
	var observed *viewmodel.CapabilityInstance
	for i := range snapshot.Instances {
		if snapshot.Instances[i].Key == parent {
			observed = &snapshot.Instances[i]
			break
		}
	}
	if observed == nil {
		t.Fatalf("running foreign runtime did not produce an L2 parent: %+v", snapshot.Instances)
	}
	if observed.Origin != "observed" || observed.ConfigStatus != "read-only" || observed.ConfigError == "" {
		t.Fatalf("foreign parent must be an observed read-only view, not a local configuration: %+v", observed)
	}
	if len(observed.MCPs) != 1 || observed.MCPs[0].Key != child || observed.MCPs[0].Ownership != "other-aact" {
		t.Fatalf("foreign child runtimes were not grouped under their parent: %+v", observed.MCPs)
	}
	if len(observed.Components) != 2 {
		t.Fatalf("Windows-owned agent observations were omitted from the observed parent: %+v", observed.Components)
	}
	if observedRequests != 1 {
		t.Fatal("foreign parent did not observe this installation's exact-key agent bindings")
	}
	wantComponents := map[string]bool{"skill:one:installed": false, "mcp:alpha:installed": false}
	for _, component := range observed.Components {
		key := component.Kind + ":" + component.Name + ":" + component.Status
		if _, ok := wantComponents[key]; ok {
			wantComponents[key] = true
		}
	}
	for key, found := range wantComponents {
		if !found {
			t.Errorf("observed parent omitted native agent state %q: %+v", key, observed.Components)
		}
	}
	preview, err := s.UISetupPreview(context.Background(), viewmodel.SetupRequest{Ref: config.InstanceRef{PackID: parent.Source, CapabilityID: parent.Package, Name: parent.Target}})
	if err != nil {
		t.Fatal(err)
	}
	if preview.InstanceOrigin != "observed" || len(preview.Inputs) != 0 || preview.Key != parent {
		t.Fatalf("foreign setup view exposed local values or lost observed identity: %+v", preview)
	}

	for _, row := range []state.Installation{managedSkill, managedMCP} {
		if err := s.Store.Remove(row); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err = s.InstanceSnapshot(context.Background(), q.Ref.CapabilityID)
	if err != nil {
		t.Fatal(err)
	}
	observed = nil
	for i := range snapshot.Instances {
		if snapshot.Instances[i].Key == parent {
			observed = &snapshot.Instances[i]
			break
		}
	}
	if observed == nil {
		t.Fatalf("Docker-observed parent disappeared after local bindings were removed: %+v", snapshot.Instances)
	}
	if observedRequests != 2 {
		t.Fatalf("fresh agent inventory was not observed after local bindings disappeared: requests=%d", observedRequests)
	}
	if len(observed.Components) == 0 {
		t.Fatalf("known empty native binding inventory must project absent components, not Unknown: %+v", observed)
	}
	for _, component := range observed.Components {
		if component.Status != "absent" {
			t.Fatalf("empty successful inventory should project absent component status: %+v", observed.Components)
		}
	}
	adapter.observeErr = fmt.Errorf("native agent inventory unavailable")
	snapshot, err = s.InstanceSnapshot(context.Background(), q.Ref.CapabilityID)
	if err != nil {
		t.Fatal(err)
	}
	observed = nil
	for i := range snapshot.Instances {
		if snapshot.Instances[i].Key == parent {
			observed = &snapshot.Instances[i]
			break
		}
	}
	if observed == nil || len(observed.Components) == 0 {
		t.Fatalf("failed inventory observation should preserve an explicit unknown component: %+v", observed)
	}
	for _, component := range observed.Components {
		if component.Status != "unavailable" {
			t.Fatalf("failed inventory observation must not be flattened to absence: %+v", observed.Components)
		}
	}
	adapter.observeErr = nil
	adapter.observe = func(agents.ObservationRequest) agents.Observation {
		return agents.Observation{Detection: agents.Detection{State: "installed", Installed: true}}
	}
	runtime.instances[0].RegistrationName = ""
	snapshot, err = s.InstanceSnapshot(context.Background(), q.Ref.CapabilityID)
	if err != nil {
		t.Fatal(err)
	}
	observed = nil
	for i := range snapshot.Instances {
		if snapshot.Instances[i].Key == parent {
			observed = &snapshot.Instances[i]
			break
		}
	}
	if observed == nil || len(observed.Components) == 0 || observed.Components[0].Status != "unavailable" {
		t.Fatalf("runtime without exact registration metadata must remain unknown, not absent: %+v", observed)
	}
}

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
	if instance.ConfigStatus == "conflict" || len(instance.MCPs) != 2 {
		t.Fatalf("stale child records were promoted to a parent conflict or the recoverable orphan was hidden: %+v", instance)
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
	orphan := instance.MCPs[1]
	if orphan.MCPID != "removed-definition" || orphan.Key != orphanKey || len(orphan.RuntimeRecords) != 1 || orphan.RuntimeRecords[0].ID != "orphan-container" {
		t.Fatalf("runtime for removed MCP definition must remain visible under its exact recoverable record: %+v", orphan)
	}
	rows, err := s.Store.Installations()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("declared missing runtimes and the undefined-MCP orphan must all remain recoverable: %+v", rows)
	}
}

func TestCapabilityInstanceSnapshotForgetsStoppedMissingRuntimeBesideLiveReplacement(t *testing.T) {
	s, _, q := profileApplyFixture(t)
	p := &s.Source.Catalog[0]
	p.MCP = nil
	p.MCPs = []catalog.MCP{{Name: "grafana-inspector", Image: "fixture", Transport: "streamable-http"}}
	key, err := s.Store.ResolveInstanceKey(q.Ref.PackID, q.Ref.CapabilityID, q.Ref.Name)
	if err != nil {
		t.Fatal(err)
	}
	childKey := mcpDefinitionKey(key, *p, p.MCPs[0])
	for _, row := range []state.Installation{
		{Key: childKey, AgentID: "docker", Component: "runtime", Destination: "aact-grafana", SourcePath: "stopped-old-id", Mode: "docker", LastAction: "stop"},
		{Key: childKey, AgentID: "docker", Component: "runtime", Destination: "aact-grafana", SourcePath: "current-live-id", Mode: "docker", LastAction: "start"},
	} {
		if err := s.Store.Record(row); err != nil {
			t.Fatal(err)
		}
	}
	s.Options.Runtime = &snapshotRuntime{instances: []mcp.Instance{
		{Key: childKey, ID: "stopped-old-id", Name: "aact-grafana", Status: "missing", Ownership: "local", LastAction: "stop"},
		{Key: childKey, ID: "current-live-id", Name: "aact-grafana", Status: "running", Ownership: "local", URL: "http://localhost:9913/mcp", LastAction: "start"},
	}}

	snapshot, err := s.InstanceSnapshot(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Instances) != 1 || len(snapshot.Instances[0].MCPs) != 1 {
		t.Fatalf("unexpected CapabilityInstance/MCP snapshot: %+v", snapshot.Instances)
	}
	runtime := snapshot.Instances[0].MCPs[0]
	if runtime.Status != "running" || runtime.Error != "" || len(runtime.RuntimeRecords) != 1 || runtime.RuntimeRecords[0].ID != "current-live-id" {
		t.Fatalf("stopped missing history poisoned the live MCP runtime: %+v", runtime)
	}
	rows, err := s.Store.Installations()
	if err != nil || len(rows) != 1 || rows[0].SourcePath != "current-live-id" {
		t.Fatalf("stopped missing row was not forgotten without changing the live runtime row: %+v, %v", rows, err)
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
