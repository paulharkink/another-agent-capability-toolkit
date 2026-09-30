package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"strings"
	"testing"
)

type fakeExec struct {
	calls  [][]string
	f      func([]string) ([]byte, error)
	stderr string
}

func (f *fakeExec) Run(_ context.Context, a []string, _ string, _ []byte, _ map[string]string, cb func([]byte)) ([]byte, error) {
	f.calls = append(f.calls, append([]string{}, a...))
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
		return nil, errors.New("not found")
	}
	return []byte("cid"), nil
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
			return nil, errors.New("not found")
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
