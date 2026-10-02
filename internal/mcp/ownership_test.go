package mcp

import (
	"context"
	"encoding/json"
	"errors"
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

func TestStopRecordsLastSuccessfulActionAfterContainerRemoval(t *testing.T) {
	r, executor, key := testRuntime(t)
	id, err := r.Store.InstallationID()
	if err != nil {
		t.Fatal(err)
	}
	containerLabels := labels(key, RunSpec{Image: "fixture", HostPort: 8765, ContainerPort: 80})
	containerLabels["aact.owner"] = id
	d := dockerInfo{ID: "cid", Name: "/" + containerName(key), Config: dockerConfig{Labels: containerLabels}, State: dockerState{Running: true, Status: "running"}}
	executor.f = func(args []string) ([]byte, error) {
		if args[1] == "inspect" {
			return json.Marshal([]dockerInfo{d})
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
