package app

import (
	"context"
	"errors"
	"io"
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
			key := state.Key{Source: "fixture", Package: "demo", Environment: "home", Target: "pms15"}
			if err := store.RecordProfile(state.ProfileRecord{Key: key}); err != nil {
				t.Fatal(err)
			}
			endpoint := "http://127.0.0.1:8765/mcp"
			if err := store.Record(state.Installation{Key: key, AgentID: "opencode", Component: "mcp", Mode: "registration", Destination: "/tmp/opencode.json", URL: endpoint}); err != nil {
				t.Fatal(err)
			}
			if withRuntimeRecord {
				if err := store.Record(state.Installation{Key: key, AgentID: "docker", Component: "runtime", Mode: "docker", Destination: "aact-pms15", SourcePath: "missing-container", URL: endpoint}); err != nil {
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
