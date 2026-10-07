package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

type fakeExec struct {
	calls         [][]string
	f             func([]string) ([]byte, error)
	stderr        string
	contextErrors []error
}

func (f *fakeExec) Run(ctx context.Context, a []string, _ string, _ []byte, _ map[string]string, cb func([]byte)) ([]byte, error) {
	f.calls = append(f.calls, append([]string{}, a...))
	f.contextErrors = append(f.contextErrors, ctx.Err())
	if cb != nil && f.stderr != "" {
		cb([]byte(f.stderr))
	}
	if f.f != nil {
		return f.f(a)
	}
	return nil, nil
}

func TestSecretEnvNotLogged(t *testing.T) {
	r, f, k := testRuntime(t)
	f.f = absent
	f.stderr = "token=private-secret"
	var progress string
	r.OnStderr = func(b []byte) { progress += string(b) }
	_, e := r.Start(context.Background(), k, RunSpec{Image: "fixture", HostPort: 8765, ContainerPort: 80, SecretEnv: map[string]string{"TOKEN": "private-secret"}})
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(progress, "private-secret") {
		t.Fatal(progress)
	}
}
func testRuntime(t *testing.T) (*Runtime, *fakeExec, state.Key) {
	t.Helper()
	s, e := state.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	f := &fakeExec{}
	r := NewDockerRuntime(s)
	r.Executor = f
	r.SkipHealth = true
	return r, f, state.Key{Source: "a", Package: "test", Environment: "dev", Target: "one"}
}
func absent(a []string) ([]byte, error) {
	if len(a) > 1 && a[1] == "inspect" {
		return nil, errors.New("No such object: requested container")
	}
	return []byte("cid"), nil
}
func TestStartFailsClosedWhenInitialInspectFails(t *testing.T) {
	r, f, k := testRuntime(t)
	spec := RunSpec{Image: "fixture", Host: "127.0.0.1", HostPort: 8765, ContainerPort: 80}
	preexisting := dockerInfo{ID: "healthy-existing", Name: "/" + containerName(k), Config: dockerConfig{Labels: locallyOwnedLabels(t, r, k, spec)}, State: dockerState{Running: true, Status: "running"}}
	inspectCalls := 0
	f.f = func(args []string) ([]byte, error) {
		switch args[1] {
		case "inspect":
			inspectCalls++
			if inspectCalls == 1 {
				return nil, errors.New("daemon unavailable")
			}
			return json.Marshal([]dockerInfo{preexisting})
		case "run":
			return nil, errors.New("container name is already in use")
		}
		return nil, nil
	}
	if _, err := r.Start(context.Background(), k, spec); err == nil || !strings.Contains(err.Error(), "daemon unavailable") {
		t.Fatalf("inspection error was ignored: %v", err)
	}
	if len(f.calls) != 1 || f.calls[0][1] != "inspect" {
		t.Fatalf("inspection failure mutated Docker or entered cleanup: %v", f.calls)
	}
}

func TestFailedRunCannotRemoveContainerCreatedByAnotherStart(t *testing.T) {
	r, f, k := testRuntime(t)
	spec := RunSpec{Image: "fixture", Host: "127.0.0.1", HostPort: 8765, ContainerPort: 80}
	other := dockerInfo{ID: "other-start", Name: "/" + containerName(k), Config: dockerConfig{Labels: locallyOwnedLabels(t, r, k, spec)}, State: dockerState{Running: true, Status: "running"}}
	inspectCalls := 0
	f.f = func(args []string) ([]byte, error) {
		switch args[1] {
		case "inspect":
			inspectCalls++
			if inspectCalls == 1 {
				return nil, errors.New("No such object: requested container")
			}
			return json.Marshal([]dockerInfo{other})
		case "run":
			return nil, errors.New("container name is already in use")
		}
		return nil, nil
	}
	if _, err := r.Start(context.Background(), k, spec); err == nil {
		t.Fatal("name collision accepted")
	}
	for _, args := range f.calls {
		if args[1] == "rm" || args[1] == "rename" {
			t.Fatalf("another start's container was mutated: %v", f.calls)
		}
	}
}
func locallyOwnedLabels(t *testing.T, r *Runtime, k state.Key, spec RunSpec) map[string]string {
	t.Helper()
	id, err := r.Store.InstallationID()
	if err != nil {
		t.Fatal(err)
	}
	l := labels(k, spec)
	l["aact.owner"] = id
	return l
}
func TestDockerBuildRunAndMountArguments(t *testing.T) {
	r, f, k := testRuntime(t)
	f.f = absent
	_, e := r.Start(context.Background(), k, RunSpec{BuildContext: t.TempDir(), Host: "127.0.0.1", HostPort: 8765, ContainerPort: 80, Transport: "streamable-http", EndpointPath: "/mcp", Env: map[string]string{"A": "b"}, SecretEnv: map[string]string{"TOKEN": "private"}, Mounts: []Mount{{Source: t.TempDir(), Destination: "/state", ReadOnly: true}}})
	if e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(f.calls)
	s := string(b)
	for _, v := range []string{"build", "run", "127.0.0.1:8765:80", "readonly", "aact.key=" + k.ID(), "--env-file"} {
		if !strings.Contains(s, v) {
			t.Errorf("missing %s: %s", v, s)
		}
	}
	if strings.Contains(s, "private") {
		t.Fatal("secret appears in Docker argv")
	}
}
func TestNoUnlabelledContainerRemoval(t *testing.T) {
	r, f, k := testRuntime(t)
	f.f = func([]string) ([]byte, error) {
		return []byte(`[{"Id":"foreign","Config":{"Labels":{}},"State":{"Running":true}}]`), nil
	}
	if e := r.Stop(context.Background(), k); e == nil {
		t.Fatal("foreign stop accepted")
	}
	if len(f.calls) != 1 {
		t.Fatal(f.calls)
	}
}
func TestInstanceIdentityIncludesSource(t *testing.T) {
	a := state.Key{Source: "a", Package: "p", Target: "t"}
	b := a
	b.Source = "b"
	if containerName(a) == containerName(b) {
		t.Fatal("source collision")
	}
}
func TestPortCollisionIsVisible(t *testing.T) {
	r, f, k := testRuntime(t)
	f.f = func(a []string) ([]byte, error) {
		if a[1] == "inspect" {
			return nil, errors.New("No such object: requested container")
		}
		return nil, errors.New("port is already allocated")
	}
	_, e := r.Start(context.Background(), k, RunSpec{Image: "fixture", HostPort: 8765, ContainerPort: 80})
	if e == nil || !strings.Contains(e.Error(), "8765") {
		t.Fatal(e)
	}
}
func TestInventoryReconcilesMissingStoppedRunning(t *testing.T) {
	r, f, k := testRuntime(t)
	f.f = func(a []string) ([]byte, error) {
		if a[1] == "ps" {
			return []byte("cid\n"), nil
		}
		return json.Marshal([]dockerInfo{{ID: "cid", Name: "/owned", Config: dockerConfig{Labels: labels(k, RunSpec{Image: "x", HostPort: 1, ContainerPort: 1})}, State: dockerState{Running: false, Status: "exited"}}})
	}
	v, e := r.List(context.Background())
	if e != nil || len(v) != 1 || v[0].Key != k || v[0].Status != "exited" {
		t.Fatalf("%+v %v", v, e)
	}
}

func TestInventoryIncludesMissingOwnedRuntime(t *testing.T) {
	r, f, k := testRuntime(t)
	if e := r.Store.Record(state.Installation{Key: k, AgentID: "docker", Component: "runtime", Destination: containerName(k), URL: "http://127.0.0.1:8765/mcp", SourcePath: "lost-id", Mode: "docker"}); e != nil {
		t.Fatal(e)
	}
	f.f = func([]string) ([]byte, error) { return nil, nil }
	items, e := r.List(context.Background())
	if e != nil || len(items) != 1 || items[0].Status != "missing" {
		t.Fatal(items, e)
	}
}
func TestOnlyLoopbackPublication(t *testing.T) {
	r, _, k := testRuntime(t)
	_, e := r.Start(context.Background(), k, RunSpec{Image: "x", Host: "0.0.0.0", HostPort: 1, ContainerPort: 1})
	if e == nil {
		t.Fatal("public listener allowed")
	}
}

func TestRuntimeUserIsExplicit(t *testing.T) {
	r, f, k := testRuntime(t)
	f.f = absent
	_, e := r.Start(context.Background(), k, RunSpec{Image: "fixture", HostPort: 8765, ContainerPort: 80, User: "123:456"})
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, a := range f.calls {
		for n, v := range a {
			if v == "--user" && n+1 < len(a) && a[n+1] == "123:456" {
				found = true
			}
		}
	}
	if !found {
		t.Fatal(f.calls)
	}
}

func unhealthySpec(t *testing.T) RunSpec {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) }))
	t.Cleanup(server.Close)
	u, _ := url.Parse(server.URL)
	port, _ := strconv.Atoi(u.Port())
	return RunSpec{Image: "fixture", Host: u.Hostname(), HostPort: port, ContainerPort: 80, Transport: "streamable-http", EndpointPath: "/mcp"}
}

func TestFailedNewRuntimeHealthCleansWithIndependentContextAndPreservesLedger(t *testing.T) {
	r, f, k := testRuntime(t)
	r.SkipHealth = false
	f.f = absent
	prior := state.Installation{Key: k, AgentID: "docker", Component: "runtime", Destination: containerName(k), SourcePath: "prior-id", URL: "http://127.0.0.1:18765/mcp", Mode: "docker"}
	if err := r.Store.Record(prior); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := r.Start(ctx, k, unhealthySpec(t)); err == nil {
		t.Fatal("unhealthy runtime accepted")
	}
	removed := false
	for n, args := range f.calls {
		if len(args) > 2 && args[1] == "rm" && args[len(args)-1] == "cid" {
			removed = true
			if f.contextErrors[n] != nil {
				t.Fatal("cleanup inherited cancelled startup context")
			}
		}
	}
	if !removed {
		t.Fatalf("unhealthy new container leaked: %v", f.calls)
	}
	rows, err := r.Store.Installations()
	if err != nil || len(rows) != 1 || rows[0].SourcePath != prior.SourcePath {
		t.Fatalf("prior ledger replaced after failure: %+v %v", rows, err)
	}
}

func TestExistingRunningRuntimeHealthFailureNeverBecomesSuccess(t *testing.T) {
	r, f, k := testRuntime(t)
	r.SkipHealth = false
	spec := unhealthySpec(t)
	f.f = func(args []string) ([]byte, error) {
		return json.Marshal([]dockerInfo{{ID: "existing", Name: "/" + containerName(k), Config: dockerConfig{Labels: locallyOwnedLabels(t, r, k, spec)}, State: dockerState{Running: true, Status: "running"}}})
	}
	for attempt := 0; attempt < 2; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		_, err := r.Start(ctx, k, spec)
		cancel()
		if err == nil {
			t.Fatalf("attempt %d falsely accepted unhealthy existing runtime", attempt)
		}
	}
	for _, args := range f.calls {
		if args[1] == "rm" {
			t.Fatal("prior existing container removed", f.calls)
		}
	}
}

func TestInventoryIncludesAndDeduplicatesExternalMCPRegistrations(t *testing.T) {
	r, f, k := testRuntime(t)
	f.f = func([]string) ([]byte, error) { return nil, nil }
	for _, agent := range []string{"claude", "codex"} {
		for _, endpoint := range []string{"https://one.example.test/mcp", "https://two.example.test/mcp"} {
			if err := r.Store.Record(state.Installation{Key: k, AgentID: agent, Component: "mcp", Destination: agent + endpoint, URL: endpoint, ExternalRegistration: true}); err != nil {
				t.Fatal(err)
			}
		}
	}
	items, err := r.List(context.Background())
	if err != nil || len(items) != 2 {
		t.Fatalf("external inventory missing or duplicated: %+v %v", items, err)
	}
	for _, item := range items {
		if item.Status != "external" || item.Key != k {
			t.Fatal(item)
		}
	}
}

func TestInventoryExternalDedupeRequiresActualContainerAndMatchingURL(t *testing.T) {
	r, f, k := testRuntime(t)
	spec := RunSpec{Image: "fixture", HostPort: 8765, ContainerPort: 80, EndpointPath: "/mcp"}
	for _, endpoint := range []string{specURL(spec), "https://remote.example.test/mcp"} {
		if err := r.Store.Record(state.Installation{Key: k, AgentID: "codex", Component: "mcp", Destination: endpoint, URL: endpoint, ExternalRegistration: true}); err != nil {
			t.Fatal(err)
		}
	}
	f.f = func(args []string) ([]byte, error) {
		if args[1] == "ps" {
			return []byte("cid"), nil
		}
		return json.Marshal([]dockerInfo{{ID: "cid", Config: dockerConfig{Labels: labels(k, spec)}, State: dockerState{Running: true}}})
	}
	items, err := r.List(context.Background())
	if err != nil || len(items) != 2 {
		t.Fatalf("container/remote URLs incorrectly merged: %+v %v", items, err)
	}
	if items[1].Status != "external" || items[1].URL != "https://remote.example.test/mcp" {
		t.Fatal(items)
	}
}

func TestInventoryDoesNotTreatSavedRegistrationAsRuntimeObservation(t *testing.T) {
	r, f, k := testRuntime(t)
	f.f = func(args []string) ([]byte, error) {
		if len(args) > 1 && args[1] == "ps" {
			return nil, nil
		}
		return nil, errors.New("unexpected docker command")
	}
	endpoint := "http://127.0.0.1:8765/mcp"
	if err := r.Store.Record(state.Installation{Key: k, AgentID: "opencode", Component: "mcp", Mode: "registration", URL: endpoint}); err != nil {
		t.Fatal(err)
	}
	items, err := r.List(context.Background())
	if err != nil || len(items) != 0 {
		t.Fatalf("saved registration was presented as observed runtime: %+v %v", items, err)
	}
	if err := r.Store.Record(state.Installation{Key: k, AgentID: "claude", Component: "mcp", Mode: "registration", URL: "https://attached.example.test/mcp", ExternalRegistration: true}); err != nil {
		t.Fatal(err)
	}
	items, err = r.List(context.Background())
	if err != nil || len(items) != 1 || items[0].Status != "external" || items[0].Ownership != "unknown" {
		t.Fatalf("explicit external registration was not preserved: %+v %v", items, err)
	}
}

func TestStoppedOwnedRuntimeSurvivesFailedReplacement(t *testing.T) {
	for _, failure := range []string{"docker run", "health"} {
		t.Run(failure, func(t *testing.T) {
			r, f, k := testRuntime(t)
			spec := unhealthySpec(t)
			r.SkipHealth = failure != "health"
			old := dockerInfo{ID: "old-id", Name: "/" + containerName(k), Config: dockerConfig{Labels: locallyOwnedLabels(t, r, k, spec)}, State: dockerState{Running: false, Status: "exited"}}
			currentName := containerName(k)
			oldRemoved := false
			f.f = func(args []string) ([]byte, error) {
				switch args[1] {
				case "inspect":
					if args[2] == currentName {
						return json.Marshal([]dockerInfo{old})
					}
					return nil, errors.New("No such object: requested container")
				case "rename":
					if args[2] == "old-id" {
						currentName = args[3]
					}
				case "rm":
					if args[len(args)-1] == "old-id" {
						oldRemoved = true
					}
				case "run":
					if failure == "docker run" {
						return nil, errors.New("fixture startup failed")
					}
					return []byte("new-id"), nil
				}
				return nil, nil
			}
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			if _, err := r.Start(ctx, k, spec); err == nil {
				t.Fatal("failed replacement accepted")
			}
			if oldRemoved || currentName != containerName(k) {
				t.Fatalf("stopped prior runtime lost: removed=%v name=%s calls=%v", oldRemoved, currentName, f.calls)
			}
		})
	}
}
