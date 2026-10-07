package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

func TestUIConfigureRegistrationsTargetsCurrentEnvironmentOnly(t *testing.T) {
	isolateUXUserHome(t, t.TempDir())
	svc, _, store := fixture(t)
	runtime := &changedRuntime{}
	svc.Options.Runtime = runtime
	key := state.Key{Source: "foreign-windows", Package: "demo", Target: "cluster"}
	result, err := svc.UIConfigureRegistrations(context.Background(), viewmodel.RegistrationRequest{
		Key: key, URL: "http://127.0.0.1:8765/mcp", Transport: "streamable-http", AgentIDs: []string{"claude"},
	})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "locate") || result.Connection.CheckedAt.IsZero() {
		t.Fatalf("unavailable source did not request locating the package: %+v, %v", result, err)
	}
	if runtime.starts != 0 || runtime.stops != 0 {
		t.Fatalf("foreign endpoint registration changed the MCP runtime: starts=%d stops=%d", runtime.starts, runtime.stops)
	}
	rows, err := store.Installations()
	if err != nil || len(rows) != 0 {
		t.Fatalf("unavailable source caused registration effects: %+v, %v", rows, err)
	}
}

func TestUIConfigureRegistrationsRequiresSourceForCompleteCapability(t *testing.T) {
	isolateUXUserHome(t, t.TempDir())
	svc, _, store := fixture(t)
	runtime := &changedRuntime{}
	svc.Options.Runtime = runtime
	result, err := svc.UIConfigureRegistrations(context.Background(), viewmodel.RegistrationRequest{
		Key: state.Key{Source: "missing-source", Package: "demo", Target: "cluster"},
		URL: "http://127.0.0.1:8765/mcp", Transport: "streamable-http", AgentIDs: []string{"claude"},
	})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "locate") {
		t.Fatalf("missing source did not provide an actionable error: %+v %v", result, err)
	}
	rows, readErr := store.Installations()
	if readErr != nil || len(rows) != 0 || runtime.starts != 0 || runtime.stops != 0 {
		t.Fatalf("missing source produced effects: rows=%+v err=%v starts=%d stops=%d", rows, readErr, runtime.starts, runtime.stops)
	}
}

func TestUIConfigureRegistrationsAttachesKnownCapabilityWithSkill(t *testing.T) {
	isolateUXUserHome(t, t.TempDir())
	svc, _, store := fixture(t)
	svc.Source.Catalog[0].MCP = &catalog.MCP{Name: "demo", Transport: "streamable-http"}
	runtime := &changedRuntime{}
	svc.Options.Runtime = runtime
	key := state.Key{Source: svc.Source.ID, Package: "demo", Target: "default"}
	result, err := svc.UIConfigureRegistrations(context.Background(), viewmodel.RegistrationRequest{
		Key: key, URL: "http://127.0.0.1:8765/mcp", Transport: "streamable-http", AgentIDs: []string{"claude"},
	})
	if err != nil || len(result.Changes) != 2 {
		t.Fatalf("complete capability attach failed: %+v %v", result, err)
	}
	rows, err := store.Installations()
	if err != nil {
		t.Fatal(err)
	}
	components := map[string]bool{}
	for _, row := range rows {
		components[row.Component] = true
	}
	if !components["skill"] || !components["mcp"] || runtime.starts != 0 || runtime.stops != 0 {
		t.Fatalf("external capability attach did not install skill+registration only: rows=%+v starts=%d stops=%d", rows, runtime.starts, runtime.stops)
	}
}

func TestUIConfigureRegistrationsMapsSingleManifestMCPToNamedEndpoint(t *testing.T) {
	isolateUXUserHome(t, t.TempDir())
	svc, _, store := fixture(t)
	svc.Source.Catalog[0].MCPs = []catalog.MCP{{Name: "primary", Transport: "streamable-http"}}
	_, err := svc.UIConfigureRegistrations(context.Background(), viewmodel.RegistrationRequest{
		Key: state.Key{Source: svc.Source.ID, Package: "demo", Target: "default"},
		URL: "http://foreign.example/mcp", Transport: "streamable-http", AgentIDs: []string{"claude"},
	})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := store.Installations()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range rows {
		if row.Component == "mcp" {
			found = true
			if row.Key.MCP != "primary" || row.URL != "http://foreign.example/mcp" {
				t.Fatalf("single list MCP identity was not mapped: %+v", row)
			}
		}
	}
	if !found {
		t.Fatalf("single list MCP registration missing: %+v", rows)
	}
}

func TestUIConfigureRegistrationsReportsUnreachableEndpointWithoutClaimingRuntime(t *testing.T) {
	isolateUXUserHome(t, t.TempDir())
	svc, _, _ := fixture(t)
	svc.Options.Runtime = &fakeRuntime{}
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := server.URL + "/mcp"
	server.Close()
	result, err := svc.UIConfigureRegistrations(context.Background(), viewmodel.RegistrationRequest{
		Key: state.Key{Source: "foreign-windows", Package: "demo", Target: "cluster"},
		URL: url, Transport: "streamable-http", AgentIDs: []string{"claude"},
	})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "locate") || result.Connection.Reachable || result.Connection.Error == "" {
		t.Fatalf("source or endpoint diagnosis absent: %+v, %v", result, err)
	}
}

func TestUIConfigureRegistrationsRejectsAmbiguousAgentConfigPaths(t *testing.T) {
	isolateUXUserHome(t, t.TempDir())
	svc, _, store := fixture(t)
	key := state.Key{Source: "foreign-windows", Package: "demo", Target: "cluster"}
	home := t.TempDir()
	for _, name := range []string{"first.json", "second.json"} {
		if err := store.Record(state.Installation{Key: key, AgentID: "claude", AgentKind: "claude", AgentHome: home, Component: "mcp", Destination: filepath.Join(home, name), RegistrationName: "demo", URL: "http://127.0.0.1:8765/mcp"}); err != nil {
			t.Fatal(err)
		}
	}
	before, err := store.Installations()
	if err != nil || len(before) != 2 {
		t.Fatalf("fixture lost one config path: %+v, %v", before, err)
	}
	_, err = svc.UIConfigureRegistrations(context.Background(), viewmodel.RegistrationRequest{Key: key, URL: "http://127.0.0.1:8765/mcp", Transport: "streamable-http", AgentIDs: []string{"claude"}})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "locate") {
		t.Fatalf("unavailable source did not request locating the package: %v", err)
	}
	after, readErr := store.Installations()
	if readErr != nil || len(after) != 2 {
		t.Fatalf("ambiguous registration was removed: %+v, %v", after, readErr)
	}
}
