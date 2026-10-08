package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
)

func dockerListing(d dockerInfo) func([]string) ([]byte, error) {
	return func(args []string) ([]byte, error) {
		if args[1] == "ps" {
			return []byte(d.ID + "\n"), nil
		}
		return json.Marshal([]dockerInfo{d})
	}
}

func TestSameProfileKeyDoesNotTransferRuntimeOwnership(t *testing.T) {
	one, _, key := testRuntime(t)
	two, executor, _ := testRuntime(t)
	ownerID, err := one.Store.InstallationID()
	if err != nil {
		t.Fatal(err)
	}
	labels := labels(key, RunSpec{Image: "fixture", HostPort: 8765, ContainerPort: 80})
	labels["aact.owner"] = ownerID
	d := dockerInfo{ID: "one-container", Name: "/" + containerName(key), Config: dockerConfig{Labels: labels}, State: dockerState{Running: true, Status: "running"}}
	executor.f = dockerListing(d)
	items, err := two.List(context.Background())
	if err != nil || len(items) != 1 || items[0].Ownership != "other-aact" || items[0].Status != "running" {
		t.Fatalf("foreign observation: %+v, %v", items, err)
	}
	if err := two.Stop(context.Background(), key); err == nil {
		t.Fatal("foreign runtime stopped")
	}
	if _, err := two.Start(context.Background(), key, RunSpec{Image: "fixture", HostPort: 8765, ContainerPort: 80}); err == nil {
		t.Fatal("foreign runtime reused or replaced")
	}
	for _, args := range executor.calls {
		if args[1] == "rm" || args[1] == "run" || args[1] == "rename" {
			t.Fatalf("foreign runtime mutated: %v", executor.calls)
		}
	}
}

func TestLegacyRuntimeRequiresExactLocallyRecordedContainerID(t *testing.T) {
	r, executor, key := testRuntime(t)
	spec := RunSpec{Image: "fixture", HostPort: 8765, ContainerPort: 80}
	d := dockerInfo{ID: "legacy-id", Name: "/" + containerName(key), Config: dockerConfig{Labels: labels(key, spec)}, State: dockerState{Running: true, Status: "running"}}
	executor.f = dockerListing(d)
	items, err := r.List(context.Background())
	if err != nil || len(items) != 1 || items[0].Ownership != "unknown" {
		t.Fatalf("unrecorded legacy runtime: %+v, %v", items, err)
	}
	if err := r.Store.Record(state.Installation{Key: key, AgentID: "docker", Component: "runtime", Destination: containerName(key), SourcePath: "legacy-id", Mode: "docker"}); err != nil {
		t.Fatal(err)
	}
	items, err = r.List(context.Background())
	if err != nil || len(items) != 1 || items[0].Ownership != "local" {
		t.Fatalf("recorded legacy runtime: %+v, %v", items, err)
	}
	d.ID = "replacement-id"
	executor.f = dockerListing(d)
	items, err = r.List(context.Background())
	if err != nil || len(items) != 2 || items[0].Ownership != "unknown" || items[1].Status != "missing" || items[1].ID != "legacy-id" {
		t.Fatalf("stale legacy record claimed replacement: %+v, %v", items, err)
	}
	if err := r.Stop(context.Background(), key); err == nil {
		t.Fatal("unrecorded replacement stopped")
	}
}

func TestDockerObservationAndLastSuccessfulActionRemainSeparate(t *testing.T) {
	r, executor, key := testRuntime(t)
	ownerID, err := r.Store.InstallationID()
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Store.Record(state.Installation{Key: key, AgentID: "docker", Component: "runtime", Destination: containerName(key), SourcePath: "cid", Mode: "docker", LastAction: "stop", LastActionAt: time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)}); err != nil {
		t.Fatal(err)
	}
	ownedLabels := labels(key, RunSpec{Image: "fixture", HostPort: 8765, ContainerPort: 80})
	ownedLabels["aact.owner"] = ownerID
	executor.f = dockerListing(dockerInfo{ID: "cid", Config: dockerConfig{Labels: ownedLabels}, State: dockerState{Running: true, Status: "running"}})
	items, err := r.List(context.Background())
	if err != nil || len(items) != 1 || items[0].Ownership != "local" || items[0].Status != "running" || items[0].LastAction != "stop" {
		t.Fatalf("Docker/state disagreement hidden: %+v, %v", items, err)
	}
	executor.f = func(args []string) ([]byte, error) {
		if args[1] == "ps" {
			return nil, errors.New("daemon unavailable")
		}
		return nil, nil
	}
	if items, err = r.List(context.Background()); err == nil || items != nil || !strings.Contains(err.Error(), "daemon unavailable") {
		t.Fatalf("Docker failure invented an observation: %+v, %v", items, err)
	}
}

func TestNewRuntimeCarriesInstallationOwnerAndRecordsStart(t *testing.T) {
	r, executor, key := testRuntime(t)
	executor.f = absent
	item, err := r.Start(context.Background(), key, RunSpec{Image: "fixture", HostPort: 8765, ContainerPort: 80})
	if err != nil || item.Ownership != "local" || item.LastAction != "start" {
		t.Fatalf("start result: %+v, %v", item, err)
	}
	id, err := r.Store.InstallationID()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, args := range executor.calls {
		for _, arg := range args {
			if arg == "aact.owner="+id {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("owner label missing from Docker run: %v", executor.calls)
	}
	rows, err := r.Store.Installations()
	if err != nil || len(rows) != 1 || rows[0].LastAction != "start" || rows[0].LastActionAt.IsZero() {
		t.Fatalf("start record: %+v, %v", rows, err)
	}
}

func TestRenamedOwnedRuntimeLogsAndStopsByObservedID(t *testing.T) {
	r, executor, key := testRuntime(t)
	ownerID, err := r.Store.InstallationID()
	if err != nil {
		t.Fatal(err)
	}
	labels := labels(key, RunSpec{Image: "fixture", HostPort: 8765, ContainerPort: 80})
	labels["aact.owner"] = ownerID
	observed := dockerInfo{ID: "replacement-id", Name: "/renamed-by-operator", Config: dockerConfig{Labels: labels}, State: dockerState{Running: true, Status: "running"}}
	if err := r.Store.Record(state.Installation{Key: key, AgentID: "docker", Component: "runtime", Destination: containerName(key), SourcePath: "replacement-id", Mode: "docker"}); err != nil {
		t.Fatal(err)
	}
	var logID, stopID string
	removed := false
	executor.f = func(args []string) ([]byte, error) {
		switch args[1] {
		case "ps":
			if removed {
				return nil, nil
			}
			return []byte("replacement-id\n"), nil
		case "inspect":
			if args[2] != "replacement-id" {
				t.Fatalf("inspect used desired name instead of observed ID: %v", args)
			}
			return json.Marshal([]dockerInfo{observed})
		case "logs":
			logID = args[len(args)-1]
			return []byte("actual logs"), nil
		case "rm":
			stopID = args[len(args)-1]
			removed = true
			return nil, nil
		default:
			t.Fatalf("unexpected Docker operation: %v", args)
			return nil, nil
		}
	}
	items, err := r.List(context.Background())
	if err != nil || len(items) != 1 || items[0].ID != "replacement-id" || items[0].Name != "renamed-by-operator" || items[0].Status != "running" || items[0].Ownership != "local" {
		t.Fatalf("inventory did not separate current identity from desired/history state: %+v, %v", items, err)
	}
	logs, err := r.Logs(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	content, err := io.ReadAll(logs)
	if err != nil || string(content) != "actual logs" || logID != "replacement-id" {
		t.Fatalf("logs did not use the current observed container ID: content=%q id=%q err=%v", content, logID, err)
	}
	if err := r.Stop(context.Background(), key); err != nil {
		t.Fatal(err)
	}
	if stopID != "replacement-id" {
		t.Fatalf("stop used %q, want observed ID replacement-id", stopID)
	}
	items, err = r.List(context.Background())
	if err != nil || len(items) != 1 || items[0].ID != "replacement-id" || items[0].Status != "missing" {
		t.Fatalf("manual rename created duplicate missing runtime history: %+v, %v", items, err)
	}
}

func TestRuntimeControlRejectsDuplicateOwnedObservations(t *testing.T) {
	r, executor, key := testRuntime(t)
	ownerID, err := r.Store.InstallationID()
	if err != nil {
		t.Fatal(err)
	}
	makeObserved := func(id string) dockerInfo {
		l := labels(key, RunSpec{Image: "fixture", HostPort: 8765, ContainerPort: 80})
		l["aact.owner"] = ownerID
		return dockerInfo{ID: id, Name: "/" + id, Config: dockerConfig{Labels: l}, State: dockerState{Running: true, Status: "running"}}
	}
	instances := []dockerInfo{makeObserved("first-id"), makeObserved("second-id")}
	executor.f = func(args []string) ([]byte, error) {
		switch args[1] {
		case "ps":
			return []byte("first-id\nsecond-id\n"), nil
		case "inspect":
			for _, item := range instances {
				if item.ID == args[2] {
					return json.Marshal([]dockerInfo{item})
				}
			}
		case "logs", "rm":
			t.Fatalf("ambiguous runtime was controlled: %v", args)
		}
		return nil, errors.New("unexpected Docker operation")
	}
	if _, err := r.Logs(context.Background(), key); err == nil || !strings.Contains(err.Error(), "multiple") {
		t.Fatalf("duplicate observations were not reported as ambiguous: %v", err)
	}
	if err := r.Stop(context.Background(), key); err == nil || !strings.Contains(err.Error(), "multiple") {
		t.Fatalf("duplicate observations were not rejected for stop: %v", err)
	}
}

func TestDockerLogsFailureKeepsStderrCauseInReturnedError(t *testing.T) {
	r, executor, key := testRuntime(t)
	ownerID, err := r.Store.InstallationID()
	if err != nil {
		t.Fatal(err)
	}
	labels := labels(key, RunSpec{Image: "fixture", HostPort: 8765, ContainerPort: 80})
	labels["aact.owner"] = ownerID
	observed := dockerInfo{ID: "actual-id", Name: "/renamed", Config: dockerConfig{Labels: labels}, State: dockerState{Running: true, Status: "running"}}
	var logsID string
	executor.f = func(args []string) ([]byte, error) {
		switch args[1] {
		case "ps":
			return []byte("actual-id\n"), nil
		case "inspect":
			return json.Marshal([]dockerInfo{observed})
		case "logs":
			logsID = args[len(args)-1]
			return nil, errors.New("exit status 1")
		default:
			return nil, errors.New("unexpected Docker operation")
		}
	}
	executor.stderr = "Error response from daemon: log stream unavailable"
	_, err = r.Logs(context.Background(), key)
	if err == nil || !strings.Contains(err.Error(), "exit status 1") || !strings.Contains(err.Error(), "log stream unavailable") {
		t.Fatalf("Docker log failure lost stderr cause: %v", err)
	}
	if logsID != "actual-id" {
		t.Fatalf("logs addressed %q, want observed container ID actual-id", logsID)
	}
}

func TestDockerListInspectionFailureReturnsUnknownObservationError(t *testing.T) {
	r, executor, _ := testRuntime(t)
	executor.f = func(args []string) ([]byte, error) {
		if args[1] == "ps" {
			return []byte("observed-id\n"), nil
		}
		return nil, errors.New("Docker daemon inspection unavailable")
	}
	instances, err := r.List(context.Background())
	if err == nil || !strings.Contains(err.Error(), "inspection unavailable") || instances != nil {
		t.Fatalf("inspection failure fabricated runtime state: %+v, %v", instances, err)
	}
}

func TestRecordActionDeduplicatesOnlySameKeyAndContainerID(t *testing.T) {
	r, _, key := testRuntime(t)
	rows := []state.Installation{
		{Key: key, AgentID: "docker", Component: "runtime", Destination: "aact-desired-name", SourcePath: "same-cid", Mode: "docker", LastAction: "start"},
		{Key: key, AgentID: "docker", Component: "runtime", Destination: "aact-manually-renamed", SourcePath: "same-cid", Mode: "docker", LastAction: "stop"},
		{Key: key, AgentID: "docker", Component: "runtime", Destination: "aact-other-container", SourcePath: "different-cid", Mode: "docker"},
	}
	otherKey := key
	otherKey.Target = "other"
	rows = append(rows, state.Installation{Key: otherKey, AgentID: "docker", Component: "runtime", Destination: "aact-other-key", SourcePath: "same-cid", Mode: "docker"})
	for _, row := range rows {
		if err := r.Store.Record(row); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.recordAction(key, Instance{Key: key, ID: "same-cid", Name: "aact-current-name"}, "stop"); err != nil {
		t.Fatal(err)
	}
	got, err := r.Store.Installations()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("cleanup merged unrelated runtime identities: %+v", got)
	}
	if got[0].Destination != "aact-desired-name" || got[0].SourcePath != "same-cid" || got[0].LastAction != "stop" {
		t.Fatalf("canonical runtime history changed identity: %+v", got[0])
	}
	if got[1].SourcePath != "different-cid" || got[2].Key != otherKey || got[2].SourcePath != "same-cid" {
		t.Fatalf("cleanup removed a distinct container ID or profile key: %+v", got)
	}
}

func TestStopRecordsLastSuccessfulActionAfterContainerRemoval(t *testing.T) {
	r, executor, key := testRuntime(t)
	id, err := r.Store.InstallationID()
	if err != nil {
		t.Fatal(err)
	}
	containerLabels := labels(key, RunSpec{Image: "fixture", HostPort: 8765, ContainerPort: 80})
	containerLabels["aact.owner"] = id
	d := dockerInfo{ID: "cid", Name: "/" + containerName(key), Config: dockerConfig{Labels: containerLabels}, State: dockerState{Running: true, Status: "running"}}
	removed := false
	executor.f = func(args []string) ([]byte, error) {
		switch args[1] {
		case "ps":
			if !removed {
				return []byte("cid\n"), nil
			}
			return nil, nil
		case "inspect":
			return json.Marshal([]dockerInfo{d})
		case "rm":
			removed = true
			return nil, nil
		}
		return nil, nil
	}
	if err := r.Stop(context.Background(), key); err != nil {
		t.Fatal(err)
	}
	rows, err := r.Store.Installations()
	if err != nil || len(rows) != 1 || rows[0].SourcePath != "cid" || rows[0].LastAction != "stop" || rows[0].LastActionAt.IsZero() {
		t.Fatalf("last successful action after removal: %+v, %v", rows, err)
	}
	items, err := r.List(context.Background())
	if err != nil || len(items) != 1 || items[0].Status != "missing" || items[0].LastAction != "stop" {
		t.Fatalf("removed container observation: %+v, %v", items, err)
	}
}
