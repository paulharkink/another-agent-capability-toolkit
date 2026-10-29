package app

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/mcp"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
)

type profileRuntime struct {
	instances []mcp.Instance
	listErr   error
	logsCalls int
}

type emptyDockerExecutor struct{}

func (emptyDockerExecutor) Run(context.Context, []string, string, []byte, map[string]string, func([]byte)) ([]byte, error) {
	return nil, nil
}

func TestUIProfileSnapshotDoesNotTreatSavedRegistrationAsObservedRuntime(t *testing.T) {
	for _, withRuntimeRecord := range []bool{false, true} {
		t.Run(map[bool]string{false: "saved registration only", true: "missing local runtime"}[withRuntimeRecord], func(t *testing.T) {
			svc, _, store := fixture(t)
			svc.Source.Catalog[0].MCP = &catalog.MCP{Name: "demo", Transport: "streamable-http"}
			key := state.Key{Source: "fixture", Package: "demo", Environment: "sample-env", Target: "target-a"}
			if err := store.RecordProfile(state.ProfileRecord{Key: key}); err != nil {
				t.Fatal(err)
			}
			endpoint := "http://127.0.0.1:8765/mcp"
			if err := store.Record(state.Installation{Key: key, AgentID: "opencode", Component: "mcp", Mode: "registration", Destination: "/tmp/opencode.json", URL: endpoint}); err != nil {
				t.Fatal(err)
			}
			if withRuntimeRecord {
				if err := store.Record(state.Installation{Key: key, AgentID: "docker", Component: "runtime", Mode: "docker", Destination: "aact-target-a", SourcePath: "missing-container", URL: endpoint}); err != nil {
					t.Fatal(err)
				}
			}
			runtime := mcp.NewDockerRuntime(store)
			runtime.Executor = emptyDockerExecutor{}
			svc.Options.Runtime = runtime

			snapshot, err := svc.UIProfileSnapshot(context.Background())
			if err != nil || len(snapshot.Profiles) != 1 {
				t.Fatalf("profile snapshot failed: %+v, %v", snapshot, err)
			}
			profile := snapshot.Profiles[0]
			wantStatus := "never-started"
			if withRuntimeRecord {
				wantStatus = "missing"
			}
			if profile.RuntimeStatus != wantStatus || profile.Ownership != "local" || !profile.CanStart || profile.CanStop {
				t.Fatalf("saved endpoint was mistaken for an observed external runtime: %+v", profile)
			}
			if profile.URL != endpoint || !profile.CanConfigureRegistrations || !reflect.DeepEqual(profile.RegisteredAgents, []string{"opencode"}) {
				t.Fatalf("saved registration facts were lost: %+v", profile)
			}
		})
	}
}

func (*profileRuntime) Start(context.Context, state.Key, mcp.RunSpec) (mcp.Instance, error) {
	return mcp.Instance{}, errors.New("unexpected start")
}
func (*profileRuntime) Stop(context.Context, state.Key) error {
	return errors.New("unexpected stop")
}
func (r *profileRuntime) List(context.Context) ([]mcp.Instance, error) { return r.instances, r.listErr }
func (r *profileRuntime) Logs(context.Context, state.Key) (io.ReadCloser, error) {
	r.logsCalls++
	return nil, errors.New("unexpected logs")
}

type exactProfileRuntime struct {
	profileRuntime
	removedIDs []string
	store      *state.Store
}

func (r *exactProfileRuntime) StopExact(_ context.Context, key state.Key, id string) error {
	r.removedIDs = append(r.removedIDs, id)
	for i := range r.instances {
		if r.instances[i].Key == key && r.instances[i].ID == id {
			r.instances = append(r.instances[:i], r.instances[i+1:]...)
			if r.store != nil {
				rows, err := r.store.Installations()
				if err != nil {
					return err
				}
				for _, row := range rows {
					if row.Component == "runtime" && row.Key == key && row.SourcePath == id {
						return r.store.Remove(row)
					}
				}
			}
			return nil
		}
	}
	if r.store != nil {
		rows, err := r.store.Installations()
		if err == nil {
			for _, row := range rows {
				if row.Component == "runtime" && row.Key == key && row.SourcePath == id {
					return r.store.Remove(row)
				}
			}
		}
	}
	return errors.New("exact runtime not found")
}

func TestUIRemoveRuntimeRecordDeletesOnlyExactLocallyOwnedRecord(t *testing.T) {
	svc, _, store := fixture(t)
	svc.Source.Catalog[0].MCP = &catalog.MCP{Name: "demo"}
	parent := state.Key{Source: "fixture", Package: "demo", Target: "test-cluster"}
	child := mcpDefinitionKey(parent, svc.Source.Catalog[0], *svc.Source.Catalog[0].MCP)
	rows := []state.Installation{
		{Key: child, AgentID: "docker", Component: "runtime", Mode: "docker", Destination: "aact-demo-a", SourcePath: "container-a"},
		{Key: child, AgentID: "docker", Component: "runtime", Mode: "docker", Destination: "aact-demo-b", SourcePath: "container-b"},
	}
	for _, row := range rows {
		if err := store.Record(row); err != nil {
			t.Fatal(err)
		}
	}
	runtime := &exactProfileRuntime{profileRuntime: profileRuntime{instances: []mcp.Instance{{Key: child, ID: "container-a", Name: "aact-demo-a", Status: "running", Ownership: "local"}}}, store: store}
	svc.Options.Runtime = runtime

	if _, err := svc.UIRemoveRuntimeRecord(context.Background(), child, "container-a"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(runtime.removedIDs, []string{"container-a"}) {
		t.Fatalf("runtime removal was not exact: %v", runtime.removedIDs)
	}
	remaining, err := store.Installations()
	if err != nil || len(remaining) != 1 || remaining[0].SourcePath != "container-b" {
		t.Fatalf("exact deletion removed sibling runtime ledger rows: %+v, %v", remaining, err)
	}
}

func TestUIRemoveDiscoveredLocallyOwnedRuntimeWithoutLedger(t *testing.T) {
	svc, _, store := fixture(t)
	svc.Source.Catalog[0].MCP = &catalog.MCP{Name: "demo"}
	parent := state.Key{Source: "fixture", Package: "demo", Target: "test-cluster"}
	child := mcpDefinitionKey(parent, svc.Source.Catalog[0], *svc.Source.Catalog[0].MCP)
	runtime := &exactProfileRuntime{profileRuntime: profileRuntime{instances: []mcp.Instance{{
		Key: child, ID: "discovered-container", Name: "aact-demo-test-cluster", Status: "exited", Ownership: "local",
	}}}, store: store}
	svc.Options.Runtime = runtime

	if _, err := svc.UIRemoveRuntimeRecord(context.Background(), child, "discovered-container"); err != nil {
		t.Fatalf("locally owned runtime discovered without a ledger row was not removable: %v", err)
	}
	if !reflect.DeepEqual(runtime.removedIDs, []string{"discovered-container"}) {
		t.Fatalf("removal did not target the exact discovered runtime ID: %v", runtime.removedIDs)
	}
	remaining, err := store.Installations()
	if err != nil || len(remaining) != 0 {
		t.Fatalf("removing an unrecorded runtime changed unrelated ledger rows: %+v, %v", remaining, err)
	}
}

func TestUIRemoveDiscoveredRuntimeWithoutLedgerRefusesForeignOwner(t *testing.T) {
	svc, _, store := fixture(t)
	svc.Source.Catalog[0].MCP = &catalog.MCP{Name: "demo"}
	parent := state.Key{Source: "fixture", Package: "demo", Target: "test-cluster"}
	child := mcpDefinitionKey(parent, svc.Source.Catalog[0], *svc.Source.Catalog[0].MCP)
	runtime := &exactProfileRuntime{profileRuntime: profileRuntime{instances: []mcp.Instance{{
		Key: child, ID: "foreign-container", Name: "aact-demo-test-cluster", Status: "running", Ownership: "other-aact",
	}}}, store: store}
	svc.Options.Runtime = runtime

	if _, err := svc.UIRemoveRuntimeRecord(context.Background(), child, "foreign-container"); err == nil {
		t.Fatal("runtime without a ledger row was removed despite foreign ownership")
	}
	if len(runtime.removedIDs) != 0 {
		t.Fatalf("foreign runtime was passed to exact removal: %v", runtime.removedIDs)
	}
	remaining, err := store.Installations()
	if err != nil || len(remaining) != 0 {
		t.Fatalf("refusing a foreign runtime created or changed ledger rows: %+v, %v", remaining, err)
	}
}

func TestUIRemoveRuntimeRecordRejectsInactivePackEvenWhenRegistered(t *testing.T) {
	svc, _, store := fixture(t)
	root := filepath.Join(t.TempDir(), "remembered")
	packageDir := filepath.Join(root, "demo")
	if err := os.MkdirAll(packageDir, 0755); err != nil {
		t.Fatal(err)
	}
	manifest := "schema_version = 1\nid = 'other-demo'\nname = 'Remembered demo'\n[mcp]\nname = 'remembered-server'\ntransport = 'streamable-http'\n"
	if err := os.WriteFile(filepath.Join(packageDir, "package.toml"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteJSON(filepath.Join(store.Root(), "manager", "sources.json"), []sourceRef{{ID: "remembered", Root: root, PackageDirs: []string{packageDir}}}); err != nil {
		t.Fatal(err)
	}
	parent := state.Key{Source: "remembered", Package: "other-demo", Target: "test-cluster"}
	child := parent
	if err := store.Record(state.Installation{Key: child, AgentID: "docker", Component: "runtime", Mode: "docker", Destination: "aact-remembered", SourcePath: "remembered-container"}); err != nil {
		t.Fatal(err)
	}
	runtime := &exactProfileRuntime{profileRuntime: profileRuntime{instances: []mcp.Instance{{Key: child, ID: "remembered-container", Status: "running", Ownership: "local"}}}, store: store}
	svc.Options.Runtime = runtime

	if _, err := svc.UIRemoveRuntimeRecord(context.Background(), child, "remembered-container"); err == nil {
		t.Fatal("UI action accepted a runtime reference from an inactive Pack")
	}
	if len(runtime.removedIDs) != 0 {
		t.Fatalf("foreign Pack runtime was stopped: %v", runtime.removedIDs)
	}
	remaining, err := store.Installations()
	if err != nil || len(remaining) != 1 || remaining[0].Key != child {
		t.Fatalf("inactive Pack runtime ledger row was mutated: %+v, %v", remaining, err)
	}
}

func TestUIInstanceRunRejectsInactivePackReference(t *testing.T) {
	svc, _, store := fixture(t)
	root := filepath.Join(t.TempDir(), "remembered")
	packageDir := filepath.Join(root, "demo")
	if err := os.MkdirAll(packageDir, 0755); err != nil {
		t.Fatal(err)
	}
	manifest := "schema_version = 1\nid = 'other-demo'\nname = 'Remembered demo'\n[mcp]\nname = 'remembered-server'\ntransport = 'streamable-http'\n"
	if err := os.WriteFile(filepath.Join(packageDir, "package.toml"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteJSON(filepath.Join(store.Root(), "manager", "sources.json"), []sourceRef{{ID: "remembered", Root: root, PackageDirs: []string{packageDir}}}); err != nil {
		t.Fatal(err)
	}
	key := state.Key{Source: "remembered", Package: "other-demo", Target: "default", MCP: "remembered-server"}
	if _, err := svc.UIInstanceRun(context.Background(), "start", key); err == nil {
		t.Fatal("UI lifecycle action accepted a forged reference to an inactive Pack")
	}
}

func TestUIRemoveRuntimeRecordForgetsStaleKeyWithoutStoppingObservedRuntime(t *testing.T) {
	svc, _, store := fixture(t)
	svc.Source.Catalog[0].MCP = &catalog.MCP{Name: "demo"}
	key := state.Key{Source: "fixture", Package: "demo", Target: "selected"}
	child := mcpDefinitionKey(key, svc.Source.Catalog[0], *svc.Source.Catalog[0].MCP)
	row := state.Installation{Key: child, AgentID: "docker", Component: "runtime", Mode: "docker", Destination: "aact-demo", SourcePath: "same-container-id"}
	if err := store.Record(row); err != nil {
		t.Fatal(err)
	}
	otherKey := child
	otherKey.Target = "sibling"
	runtime := &exactProfileRuntime{profileRuntime: profileRuntime{instances: []mcp.Instance{{
		Key: otherKey, ID: row.SourcePath, Status: "running", Ownership: "local",
	}}}, store: store}
	svc.Options.Runtime = runtime

	if message, err := svc.UIRemoveRuntimeRecord(context.Background(), child, row.SourcePath); err != nil || !strings.Contains(message, "left running") {
		t.Fatalf("stale-key record was not made recoverable without stopping the runtime: message=%q err=%v", message, err)
	}
	if len(runtime.removedIDs) != 0 {
		t.Fatalf("mismatched live runtime was stopped: %v", runtime.removedIDs)
	}
	remaining, err := store.Installations()
	if err != nil || len(remaining) != 0 {
		t.Fatalf("stale exact ledger row was not removed: %+v, %v", remaining, err)
	}
}

func TestUIRemoveRuntimeRecordForgetsUnknownOwnerWithoutDeletingContainer(t *testing.T) {
	svc, _, store := fixture(t)
	svc.Source.Catalog[0].MCP = &catalog.MCP{Name: "demo"}
	parent := state.Key{Source: "fixture", Package: "demo", Target: "test-cluster"}
	child := mcpDefinitionKey(parent, svc.Source.Catalog[0], *svc.Source.Catalog[0].MCP)
	row := state.Installation{Key: child, AgentID: "docker", Component: "runtime", Mode: "docker", Destination: "unknown-container", SourcePath: "unknown-id"}
	if err := store.Record(row); err != nil {
		t.Fatal(err)
	}
	runtime := &exactProfileRuntime{profileRuntime: profileRuntime{instances: []mcp.Instance{{Key: child, ID: "unknown-id", Name: "unknown-container", Status: "running", Ownership: "unknown"}}}, store: store}
	svc.Options.Runtime = runtime
	if message, err := svc.UIRemoveRuntimeRecord(context.Background(), child, "unknown-id"); err != nil || !strings.Contains(message, "unknown") {
		t.Fatalf("unknown owner record should be forgettable without container deletion: message=%q err=%v", message, err)
	}
	if len(runtime.removedIDs) != 0 {
		t.Fatalf("unknown runtime was destructively removed: %v", runtime.removedIDs)
	}
	remaining, err := store.Installations()
	if err != nil || len(remaining) != 0 {
		t.Fatalf("unknown-owner exact ledger record was not removed: %+v, %v", remaining, err)
	}
}

func TestUIProfileSnapshotIncludesNeverStartedSavedProfile(t *testing.T) {
	svc, _, store := fixture(t)
	svc.Source.Catalog[0].MCP = &catalog.MCP{Name: "demo", Transport: "streamable-http"}
	key := state.Key{Source: "fixture", Package: "demo", Environment: "prod", Target: "east"}
	if err := store.RecordProfile(state.ProfileRecord{Key: key}); err != nil {
		t.Fatal(err)
	}
	svc.Options.Runtime = &profileRuntime{}
	snapshot, err := svc.UIProfileSnapshot(context.Background())
	if err != nil || len(snapshot.Profiles) != 1 || snapshot.Profiles[0].Key != key || snapshot.Profiles[0].RuntimeStatus != "never-started" || snapshot.Profiles[0].Transport != "streamable-http" {
		t.Fatalf("saved profile missing or misreported: %+v, %v", snapshot, err)
	}
}

func TestUIProfileSnapshotShowsForeignRuntimeAndDockerFailureWithoutErasingProfile(t *testing.T) {
	svc, _, store := fixture(t)
	svc.Source.Catalog[0].MCP = &catalog.MCP{Name: "demo"}
	key := state.Key{Source: "fixture", Package: "demo", Target: "default"}
	if err := store.RecordProfile(state.ProfileRecord{Key: key}); err != nil {
		t.Fatal(err)
	}
	runtime := &profileRuntime{instances: []mcp.Instance{{Key: key, Status: "running", URL: "http://127.0.0.1:8765/mcp", Ownership: "other-aact"}}}
	svc.Options.Runtime = runtime
	snapshot, err := svc.UIProfileSnapshot(context.Background())
	if err != nil || len(snapshot.Profiles) != 1 || snapshot.Profiles[0].Ownership != "other-aact" || snapshot.Profiles[0].CanStop || snapshot.Profiles[0].RuntimeStatus != "running" {
		t.Fatalf("foreign runtime ownership lost: %+v, %v", snapshot, err)
	}
	runtime.instances = nil
	runtime.listErr = errors.New("Docker unavailable")
	snapshot, err = svc.UIProfileSnapshot(context.Background())
	if err != nil || len(snapshot.Profiles) != 1 || snapshot.Profiles[0].RuntimeStatus != "running" || !snapshot.Profiles[0].ObservationStale || snapshot.Profiles[0].ObservedAt.IsZero() || !strings.Contains(snapshot.DockerError, "Docker unavailable") {
		t.Fatalf("Docker failure erased or invented status: %+v, %v", snapshot, err)
	}
}

func TestUIProfileSnapshotDoesNotMistakeForeignRunningMCPForLocalMissingRecord(t *testing.T) {
	svc, _, store := fixture(t)
	key := state.Key{Source: "fixture", Package: "demo", Target: "default"}
	if err := store.RecordProfile(state.ProfileRecord{Key: key}); err != nil {
		t.Fatal(err)
	}
	svc.Options.Runtime = &profileRuntime{instances: []mcp.Instance{
		{Key: key, ID: "foreign-container", Status: "running", URL: "http://127.0.0.1:8765/mcp", Ownership: "other-aact"},
		{Key: key, ID: "old-local-container", Status: "missing", URL: "http://127.0.0.1:8765/mcp", Ownership: "local", LastAction: "stop"},
	}}
	snapshot, err := svc.UIProfileSnapshot(context.Background())
	if err != nil || len(snapshot.Profiles) != 1 || snapshot.Profiles[0].Ownership != "other-aact" || snapshot.Profiles[0].RuntimeStatus != "running" || snapshot.Profiles[0].CanStop || snapshot.Profiles[0].LocalLastAction != "stop" {
		t.Fatalf("foreign live runtime obscured by local historical record: %+v, %v", snapshot, err)
	}
}

func TestUIProfileSnapshotDisablesActionsForTwoRunningOwners(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		svc, _, _ := fixture(t)
		key := state.Key{Source: "fixture", Package: "demo", Target: "default"}
		instances := []mcp.Instance{
			{Key: key, ID: "local-running", Status: "running", URL: "http://127.0.0.1:8765/mcp", Ownership: "local"},
			{Key: key, ID: "foreign-running", Status: "running", URL: "http://127.0.0.1:9765/mcp", Ownership: "other-aact"},
		}
		if reverse {
			instances[0], instances[1] = instances[1], instances[0]
		}
		svc.Options.Runtime = &profileRuntime{instances: instances}
		snapshot, err := svc.UIProfileSnapshot(context.Background())
		if err != nil || len(snapshot.Profiles) != 1 || snapshot.Profiles[0].RuntimeStatus != "conflict" || snapshot.Profiles[0].CanStop || snapshot.Profiles[0].CanConfigureRegistrations {
			t.Fatalf("multiple runtime owners were flattened (reverse=%v): %+v, %v", reverse, snapshot, err)
		}
	}
}

func TestUIProfileSnapshotUsesCurrentRegistrationAfterDockerFailure(t *testing.T) {
	svc, _, store := fixture(t)
	key := state.Key{Source: "fixture", Package: "demo", Target: "default"}
	oldURL := "http://127.0.0.1:8765/mcp"
	newURL := "http://127.0.0.1:9765/mcp"
	row := state.Installation{Key: key, AgentID: "opencode", Component: "mcp", Destination: "/tmp/opencode.json", RegistrationName: "demo", URL: oldURL}
	if err := store.Record(row); err != nil {
		t.Fatal(err)
	}
	runtime := &profileRuntime{instances: []mcp.Instance{{Key: key, Status: "external", URL: oldURL, Ownership: "unknown"}}}
	svc.Options.Runtime = runtime
	if _, err := svc.UIProfileSnapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
	row.URL = newURL
	if err := store.Record(row); err != nil {
		t.Fatal(err)
	}
	runtime.instances = nil
	runtime.listErr = errors.New("Docker unavailable")
	snapshot, err := svc.UIProfileSnapshot(context.Background())
	if err != nil || len(snapshot.Profiles) != 1 || snapshot.Profiles[0].URL != newURL {
		t.Fatalf("stale runtime cache overwrote current registration: %+v, %v", snapshot, err)
	}
}

func TestUIProfileSnapshotPrefersRunningContainerOverExitedSameKey(t *testing.T) {
	svc, _, _ := fixture(t)
	key := state.Key{Source: "fixture", Package: "demo", Target: "default"}
	svc.Options.Runtime = &profileRuntime{instances: []mcp.Instance{
		{Key: key, ID: "current", Status: "running", Ownership: "local"},
		{Key: key, ID: "previous", Status: "exited", Ownership: "local"},
	}}
	snapshot, err := svc.UIProfileSnapshot(context.Background())
	if err != nil || len(snapshot.Profiles) != 1 || snapshot.Profiles[0].RuntimeStatus != "running" || snapshot.Profiles[0].CanStart {
		t.Fatalf("running container hidden by exited duplicate: %+v, %v", snapshot, err)
	}
}

func TestUIProfileSnapshotExplainsDisabledForeignStop(t *testing.T) {
	svc, _, _ := fixture(t)
	key := state.Key{Source: "fixture", Package: "demo", Target: "default"}
	svc.Options.Runtime = &profileRuntime{instances: []mcp.Instance{{Key: key, Status: "running", Ownership: "other-aact"}}}
	snapshot, err := svc.UIProfileSnapshot(context.Background())
	if err != nil || len(snapshot.Profiles) != 1 || snapshot.Profiles[0].CanStop || snapshot.Profiles[0].StopDisabledReason == "" {
		t.Fatalf("foreign Stop lacks visible reason: %+v, %v", snapshot, err)
	}
}
func TestUICapabilityInstancesReportsAuthFailureAndKeepsRuntimeRunning(t *testing.T) {
	svc, _, store := fixture(t)
	svc.Source.Catalog[0].MCP = &catalog.MCP{Name: "demo", Transport: "streamable-http", HealthPath: "/health"}
	key := state.Key{Source: svc.Source.ID, Package: "demo", Target: "smoke"}
	if err := store.RecordInstance(state.InstanceRecord{Key: key, Name: "smoke", Local: true}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/health" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte("{\"authenticated\":false}"))
	}))
	defer server.Close()
	svc.Options.Runtime = &profileRuntime{instances: []mcp.Instance{{Key: key, ID: "smoke-container", Name: "demo", Status: "running", URL: server.URL + "/mcp", Ownership: "local"}}}
	snapshots, err := svc.UICapabilityInstances(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	snapshot := snapshots["demo"]
	if len(snapshot.Instances) != 1 || len(snapshot.Instances[0].MCPs) != 1 {
		t.Fatalf("capability snapshot = %+v", snapshot)
	}
	runtime := snapshot.Instances[0].MCPs[0]
	if runtime.Status != "running" {
		t.Fatalf("auth failure changed runtime state: %+v", runtime)
	}
	if runtime.AuthenticationStatus != "failed" {
		t.Fatalf("auth status = %q, want failed", runtime.AuthenticationStatus)
	}
}
