package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

func TestUIConfigureRegistrationsTargetsCurrentEnvironmentOnly(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	svc, _, store := fixture(t)
	svc.Options.Runtime = &fakeRuntime{}
	key := state.Key{Source: "foreign-windows", Package: "demo", Target: "cluster"}
	result, err := svc.UIConfigureRegistrations(context.Background(), viewmodel.RegistrationRequest{
		Key: key, URL: "http://127.0.0.1:8765/mcp", Transport: "streamable-http", AgentIDs: []string{"generic:work"},
	})
	if err != nil || len(result.Changes) != 1 || result.Changes[0].Key != key {
		t.Fatalf("foreign registration failed: %+v, %v", result, err)
	}
	rows, err := store.Installations()
	if err != nil || len(rows) != 1 || rows[0].AgentID != "generic:work" {
		t.Fatalf("current environment registration missing: %+v, %v", rows, err)
	}
}

func TestUIConfigureRegistrationsReportsUnreachableEndpointWithoutClaimingRuntime(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	svc, _, _ := fixture(t)
	svc.Options.Runtime = &fakeRuntime{}
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := server.URL + "/mcp"
	server.Close()
	result, err := svc.UIConfigureRegistrations(context.Background(), viewmodel.RegistrationRequest{
		Key: state.Key{Source: "foreign-windows", Package: "demo", Target: "cluster"},
		URL: url, Transport: "streamable-http", AgentIDs: []string{"generic:work"},
	})
	if err != nil || len(result.Changes) != 1 || result.Connection.Reachable || result.Connection.Error == "" {
		t.Fatalf("unreachable endpoint warning absent: %+v, %v", result, err)
	}
}

func TestUIConfigureRegistrationsRejectsAmbiguousAgentConfigPaths(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	svc, _, store := fixture(t)
	key := state.Key{Source: "foreign-windows", Package: "demo", Target: "cluster"}
	home := t.TempDir()
	for _, name := range []string{"first.json", "second.json"} {
		if err := store.Record(state.Installation{Key: key, AgentID: "generic:work", AgentKind: "generic", AgentHome: home, Component: "mcp", Destination: filepath.Join(home, name), RegistrationName: "demo", URL: "http://127.0.0.1:8765/mcp"}); err != nil {
			t.Fatal(err)
		}
	}
	before, err := store.Installations()
	if err != nil || len(before) != 2 {
		t.Fatalf("fixture lost one config path: %+v, %v", before, err)
	}
	_, err = svc.UIConfigureRegistrations(context.Background(), viewmodel.RegistrationRequest{Key: key, URL: "http://127.0.0.1:8765/mcp", Transport: "streamable-http", AgentIDs: []string{"generic:work"}})
	if err == nil {
		t.Fatal("ambiguous config paths were silently collapsed")
	}
	after, readErr := store.Installations()
	if readErr != nil || len(after) != 2 {
		t.Fatalf("ambiguous registration was removed: %+v, %v", after, readErr)
	}
}
