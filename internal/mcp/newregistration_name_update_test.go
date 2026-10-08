package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
)

func TestStartReplacesCurrentOwnedRuntimeWhenRegistrationNameChanges(t *testing.T) {
	r, executor, key := testRuntime(t)
	containers := map[string]dockerInfo{}
	var calls [][]string
	nextID := 0
	executor.f = func(args []string) ([]byte, error) {
		calls = append(calls, append([]string(nil), args...))
		if len(args) < 2 {
			return nil, errors.New("invalid Docker command")
		}
		switch args[1] {
		case "ps":
			ids := make([]string, 0, len(containers))
			for id := range containers {
				ids = append(ids, id)
			}
			return []byte(strings.Join(ids, "\n")), nil
		case "inspect":
			for _, container := range containers {
				if args[2] == container.ID || args[2] == strings.TrimPrefix(container.Name, "/") {
					return json.Marshal([]dockerInfo{container})
				}
			}
			return nil, errors.New("No such object: requested container")
		case "run":
			for _, container := range containers {
				if container.State.Running && container.Config.Labels["aact.url"] == "http://127.0.0.1:8765" {
					return nil, errors.New("Bind for 127.0.0.1:8765 failed: port is already allocated")
				}
			}
			name := ""
			labels := map[string]string{}
			for i := 2; i < len(args); i++ {
				switch args[i] {
				case "--name":
					name = args[i+1]
				case "--label":
					parts := strings.SplitN(args[i+1], "=", 2)
					labels[parts[0]] = parts[1]
				}
			}
			nextID++
			id := fmt.Sprintf("container-%d", nextID)
			containers[id] = dockerInfo{ID: id, Name: "/" + name, Config: dockerConfig{Labels: labels}, State: dockerState{Running: true, Status: "running"}}
			return []byte(id), nil
		case "rm":
			delete(containers, args[len(args)-1])
			return nil, nil
		case "stop":
			container := containers[args[2]]
			container.State.Running = false
			container.State.Status = "exited"
			containers[args[2]] = container
			return nil, nil
		case "rename":
			container := containers[args[2]]
			container.Name = "/" + args[3]
			container.State.Running = false
			container.State.Status = "exited"
			containers[args[2]] = container
			return nil, nil
		default:
			return nil, fmt.Errorf("unexpected Docker operation: %v", args)
		}
	}

	base := RunSpec{Image: "fixture", HostPort: 8765, ContainerPort: 80, RegistrationName: "Registration A"}
	first, err := r.Start(context.Background(), key, base)
	if err != nil {
		t.Fatal(err)
	}
	secondSpec := base
	secondSpec.RegistrationName = "Registration B"
	second, err := r.Start(context.Background(), key, secondSpec)
	if err != nil {
		t.Fatalf("same profile runtime could not adopt its new registration name: %v", err)
	}
	if second.ID == first.ID || second.Name != "aact-registration-b" {
		t.Fatalf("replacement identity = %+v, first = %+v", second, first)
	}
	if _, stillPresent := containers[first.ID]; stillPresent {
		t.Fatalf("obsolete runtime %s still occupies its port", first.ID)
	}
	if current := containers[second.ID]; current.ID == "" || current.Config.Labels["aact.registration_name"] != secondSpec.RegistrationName {
		t.Fatalf("replacement container labels are incorrect: %+v", current)
	}
	rows, err := r.Store.Installations()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].SourcePath != second.ID {
		t.Fatalf("runtime history retained obsolete or duplicate rows: %+v", rows)
	}
	var runCount, oldStopIndex, newRunIndex int
	for i, args := range calls {
		if len(args) > 1 && args[1] == "run" {
			runCount++
			if runCount == 2 {
				newRunIndex = i
			}
		}
		if len(args) > 2 && args[1] == "stop" && args[len(args)-1] == first.ID {
			oldStopIndex = i + 1
		}
	}
	if runCount != 2 || oldStopIndex == 0 || oldStopIndex > newRunIndex {
		t.Fatalf("old port owner was not stopped before replacement run: %v", calls)
	}
}

func TestStartRegistrationNameChangeRefusesDuplicateOwnedRuntimes(t *testing.T) {
	r, executor, key := testRuntime(t)
	ownerID, err := r.Store.InstallationID()
	if err != nil {
		t.Fatal(err)
	}
	makeOwned := func(id, name string) dockerInfo {
		l := labels(key, RunSpec{Image: "fixture", HostPort: 8765, ContainerPort: 80, RegistrationName: "Registration A"})
		l["aact.owner"] = ownerID
		return dockerInfo{ID: id, Name: "/" + name, Config: dockerConfig{Labels: l}, State: dockerState{Running: true, Status: "running"}}
	}
	instances := []dockerInfo{makeOwned("first-id", "aact-registration-a"), makeOwned("second-id", "manual-rename")}
	for _, item := range instances {
		if err := r.Store.Record(state.Installation{Key: key, AgentID: "docker", Component: "runtime", Destination: strings.TrimPrefix(item.Name, "/"), SourcePath: item.ID, Mode: "docker"}); err != nil {
			t.Fatal(err)
		}
	}
	mutated := false
	executor.f = func(args []string) ([]byte, error) {
		if len(args) > 1 && (args[1] == "run" || args[1] == "rm" || args[1] == "rename") {
			mutated = true
		}
		switch args[1] {
		case "inspect":
			for _, item := range instances {
				if args[2] == item.ID || args[2] == strings.TrimPrefix(item.Name, "/") {
					return json.Marshal([]dockerInfo{item})
				}
			}
			return nil, errors.New("No such object: requested container")
		case "ps":
			return []byte("first-id\nsecond-id\n"), nil
		default:
			return nil, fmt.Errorf("unexpected Docker operation: %v", args)
		}
	}
	_, err = r.Start(context.Background(), key, RunSpec{Image: "fixture", HostPort: 8765, ContainerPort: 80, RegistrationName: "Registration B"})
	if err == nil || !strings.Contains(err.Error(), "multiple") {
		t.Fatalf("duplicate owned runtimes were not refused as ambiguous: %v", err)
	}
	if mutated {
		t.Fatalf("ambiguous runtime inventory was mutated: %v", executor.calls)
	}
}

func TestStartRegistrationNameChangeDoesNotMutateForeignSameKeyRuntime(t *testing.T) {
	r, executor, key := testRuntime(t)
	var err error
	labels := labels(key, RunSpec{Image: "fixture", HostPort: 8765, ContainerPort: 80, RegistrationName: "Registration A"})
	labels["aact.owner"] = "another-installation"
	foreign := dockerInfo{ID: "foreign-id", Name: "/aact-registration-b", Config: dockerConfig{Labels: labels}, State: dockerState{Running: true, Status: "running"}}
	mutated := false
	executor.f = func(args []string) ([]byte, error) {
		if len(args) > 1 && (args[1] == "run" || args[1] == "rm" || args[1] == "rename") {
			mutated = true
		}
		switch args[1] {
		case "inspect":
			if args[2] == foreign.ID || args[2] == "aact-registration-b" || args[2] == containerName(key) {
				return json.Marshal([]dockerInfo{foreign})
			}
			return nil, errors.New("No such object: requested container")
		case "ps":
			return []byte(foreign.ID), nil
		default:
			return nil, fmt.Errorf("unexpected Docker operation: %v", args)
		}
	}
	_, err = r.Start(context.Background(), key, RunSpec{Image: "fixture", HostPort: 8765, ContainerPort: 80, RegistrationName: "Registration B"})
	if err == nil {
		t.Fatal("foreign runtime was adopted during registration-name change")
	}
	if mutated {
		t.Fatalf("foreign runtime inventory was mutated: %v", executor.calls)
	}
}

func TestStartUpdatesRunningLegacyHashNamedRuntimeToReadableName(t *testing.T) {
	r, executor, key := testRuntime(t)
	ownerID, err := r.Store.InstallationID()
	if err != nil {
		t.Fatal(err)
	}
	legacySpec := RunSpec{Image: "fixture", HostPort: 8765, ContainerPort: 80}
	legacy := dockerInfo{ID: "legacy-runtime-id", Name: "/" + containerName(key), Config: dockerConfig{Labels: labels(key, legacySpec)}, State: dockerState{Running: true, Status: "running"}}
	legacy.Config.Labels["aact.owner"] = ownerID
	if err := r.Store.Record(state.Installation{Key: key, AgentID: "docker", Component: "runtime", Destination: legacy.Name[1:], SourcePath: legacy.ID, Mode: "docker"}); err != nil {
		t.Fatal(err)
	}
	removed := false
	var replacementName string
	executor.f = func(args []string) ([]byte, error) {
		switch args[1] {
		case "inspect":
			if args[2] == containerName(key) && !removed {
				return json.Marshal([]dockerInfo{legacy})
			}
			return nil, errors.New("No such object: requested container")
		case "stop":
			legacy.State.Running = false
			legacy.State.Status = "exited"
			return nil, nil
		case "rename":
			legacy.Name = "/" + args[3]
			return nil, nil
		case "run":
			for i := 2; i+1 < len(args); i++ {
				if args[i] == "--name" {
					replacementName = args[i+1]
					break
				}
			}
			return []byte("readable-runtime-id"), nil
		case "rm":
			if args[len(args)-1] == legacy.ID {
				removed = true
			}
			return nil, nil
		default:
			return nil, fmt.Errorf("unexpected Docker operation: %v", args)
		}
	}
	instance, err := r.Start(context.Background(), key, RunSpec{Image: "fixture", HostPort: 8765, ContainerPort: 80, RegistrationName: "Readable Legacy Runtime"})
	if err != nil {
		t.Fatal(err)
	}
	if instance.ID != "readable-runtime-id" || replacementName != "aact-readable-legacy-runtime" || !removed {
		t.Fatalf("legacy runtime was not replaced by the readable registration name: instance=%+v name=%q removed=%v calls=%v", instance, replacementName, removed, executor.calls)
	}
	rows, err := r.Store.Installations()
	if err != nil || len(rows) != 1 || rows[0].SourcePath != instance.ID {
		t.Fatalf("legacy runtime history was not retired after replacement: rows=%+v err=%v", rows, err)
	}
}

func TestFailedRegistrationNameReplacementLeavesPriorRuntimeStoppedInBackup(t *testing.T) {
	r, executor, key := testRuntime(t)
	ownerID, err := r.Store.InstallationID()
	if err != nil {
		t.Fatal(err)
	}
	legacySpec := RunSpec{Image: "fixture", HostPort: 8765, ContainerPort: 80}
	legacy := dockerInfo{ID: "legacy-runtime-id", Name: "/" + containerName(key), Config: dockerConfig{Labels: labels(key, legacySpec)}, State: dockerState{Running: true, Status: "running"}}
	legacy.Config.Labels["aact.owner"] = ownerID
	if err := r.Store.Record(state.Installation{Key: key, AgentID: "docker", Component: "runtime", Destination: containerName(key), SourcePath: legacy.ID, Mode: "docker"}); err != nil {
		t.Fatal(err)
	}
	var stopCount, renameCount int
	executor.f = func(args []string) ([]byte, error) {
		switch args[1] {
		case "inspect":
			if args[2] == containerName(key) {
				return json.Marshal([]dockerInfo{legacy})
			}
			return nil, errors.New("No such object: requested container")
		case "stop":
			stopCount++
			legacy.State.Running = false
			legacy.State.Status = "exited"
			return nil, nil
		case "rename":
			renameCount++
			legacy.Name = "/" + args[3]
			return nil, nil
		case "run":
			return nil, errors.New("replacement image failed")
		default:
			return nil, fmt.Errorf("unexpected Docker operation: %v", args)
		}
	}
	_, err = r.Start(context.Background(), key, RunSpec{Image: "fixture", HostPort: 8765, ContainerPort: 80, RegistrationName: "Readable Legacy Runtime"})
	if err == nil || !strings.Contains(err.Error(), "replacement image failed") {
		t.Fatalf("replacement failure was not returned: %v", err)
	}
	if legacy.State.Running || !strings.HasPrefix(legacy.Name, "/aact-previous-") || stopCount != 1 || renameCount != 1 {
		t.Fatalf("failed replacement did not preserve the actual stopped backup state: runtime=%+v stopCount=%d renameCount=%d calls=%v", legacy, stopCount, renameCount, executor.calls)
	}
	for _, args := range executor.calls {
		if args[1] == "start" || (args[1] == "rename" && args[3] == containerName(key)) || (args[1] == "rm" && args[len(args)-1] == legacy.ID) {
			t.Fatalf("failed replacement restarted/restored/removed prior runtime: %v", executor.calls)
		}
	}
	rows, err := r.Store.Installations()
	if err != nil {
		t.Fatal(err)
	}
	retained := 0
	for _, row := range rows {
		if row.Key == key && row.AgentID == "docker" && row.Component == "runtime" && row.SourcePath == legacy.ID {
			retained++
		}
	}
	if retained != 1 {
		t.Fatalf("failed replacement did not retain exactly one prior runtime history row for key/CID: rows=%+v", rows)
	}
}
