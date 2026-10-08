package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/agents"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/picker"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

func TestUXRegistrationOptionsAndBoundaryRejectGenericAll(t *testing.T) {
	isolateUXUserHome(t, t.TempDir())
	svc, _, store := fixture(t)
	key := state.Key{Source: "foreign-windows", Package: "demo", Target: "cluster"}
	for _, id := range []string{"all", "generic", "generic:work", "generic-mcp:work"} {
		t.Run(id, func(t *testing.T) {
			_, err := configureUIRegistrationsTest(context.Background(), svc, viewmodel.RegistrationRequest{
				Key: key, URL: "http://127.0.0.1:1/mcp", Transport: "streamable-http", AgentIDs: []string{id},
			})
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), "named") {
				t.Fatalf("UI registration boundary accepted non-named destination %q: %v", id, err)
			}
			rows, readErr := store.Installations()
			if readErr != nil || len(rows) != 0 {
				t.Fatalf("rejected destination caused ledger effects: rows=%+v err=%v", rows, readErr)
			}
		})
	}
}

func seedLegacyAndNamedRegistrations(t *testing.T) (*Service, *state.Store, state.Key, *changedRuntime, string, string) {
	t.Helper()
	home := t.TempDir()
	isolateUXUserHome(t, home)
	svc, _, store := fixture(t)
	svc.Source.Catalog[0].MCP = &catalog.MCP{Name: "demo", Transport: "streamable-http"}
	runtime := &changedRuntime{}
	svc.Options.Runtime = runtime
	key := state.Key{Source: svc.Source.ID, Package: "demo", Target: "default"}
	legacyConfig := filepath.Join(home, "legacy-mcp.json")
	legacy := agents.Environment{ID: "generic:legacy", Kind: "generic", Home: home, SkillsDir: filepath.Join(home, ".agents", "skills"), ConfigPath: legacyConfig}
	claude, err := agents.ResolveEnvironment("claude", "claude", home)
	if err != nil {
		t.Fatal(err)
	}
	oldURL := "http://127.0.0.1:1/legacy-mcp"
	if _, err := configureRegistrationsTest(context.Background(), svc, RegistrationRequest{
		Key: key, URL: oldURL, Transport: "streamable-http", Agents: []agents.Environment{legacy, claude},
	}); err != nil {
		t.Fatalf("could not create real legacy and named MCP registrations: %v", err)
	}
	return svc, store, key, runtime, legacyConfig, oldURL
}

func TestUXNamedSavePreservesLegacyMCPRegistrationAndItsEndpoint(t *testing.T) {
	svc, store, key, _, legacyConfig, oldURL := seedLegacyAndNamedRegistrations(t)
	newURL := "http://127.0.0.1:2/new-mcp"
	result, err := configureUIRegistrationsTest(context.Background(), svc, viewmodel.RegistrationRequest{
		Key: key, URL: newURL, Transport: "streamable-http", AgentIDs: []string{"claude"},
	})
	if err != nil || len(result.Changes) == 0 {
		t.Fatalf("complete capability save failed: result=%+v err=%v", result, err)
	}
	rows, err := store.Installations()
	if err != nil {
		t.Fatal(err)
	}
	var legacyRow, skillRow, namedRow bool
	for _, row := range rows {
		legacyRow = legacyRow || row.Component == "mcp" && row.AgentID == "generic:legacy" && row.URL == oldURL && row.Destination == legacyConfig
		skillRow = skillRow || row.Component == "skill" && row.AgentID == "claude"
		namedRow = namedRow || row.Component == "mcp" && row.AgentID == "claude" && row.URL == newURL
	}
	if !legacyRow || skillRow || !namedRow {
		t.Fatalf("desired capability bindings were not applied: %+v", rows)
	}
	contents, err := os.ReadFile(legacyConfig)
	if err != nil || !strings.Contains(string(contents), oldURL) {
		t.Fatalf("unchecked legacy config was changed: content=%s err=%v", contents, err)
	}
}

func TestUXExplicitRemovalRemovesOnlySelectedRecordedMCPWithoutCheckingEndpoint(t *testing.T) {
	svc, store, key, runtime, legacyConfig, _ := seedLegacyAndNamedRegistrations(t)
	result, err := configureUIRegistrationsTest(context.Background(), svc, viewmodel.RegistrationRequest{
		Key: key, RemoveAgentIDs: []string{"generic:legacy"},
	})
	if err != nil {
		t.Fatalf("explicit legacy removal failed: result=%+v err=%v", result, err)
	}
	if !result.Connection.CheckedAt.IsZero() {
		t.Fatalf("removal unexpectedly checked the endpoint: %+v", result.Connection)
	}
	if runtime.starts != 0 || runtime.stops != 0 {
		t.Fatalf("removal changed the MCP runtime: starts=%d stops=%d", runtime.starts, runtime.stops)
	}
	rows, err := store.Installations()
	if err != nil {
		t.Fatal(err)
	}
	var named, skillRow bool
	for _, row := range rows {
		named = named || row.Component == "mcp" && row.AgentID == "claude"
		skillRow = skillRow || row.Component == "skill" && row.AgentID == "claude"
		if row.AgentID == "generic:legacy" {
			t.Fatalf("selected legacy MCP registration remains in ledger: %+v", row)
		}
	}
	contents, readErr := os.ReadFile(legacyConfig)
	if readErr != nil || strings.Contains(string(contents), "legacy-mcp") {
		t.Fatalf("selected legacy MCP registration remains in its actual config: %s err=%v", contents, readErr)
	}
	if !named || skillRow {
		t.Fatalf("unchecking one agent changed another agent's MCP registration or touched profile skill state: %+v", rows)
	}
}

func TestUXCancelledRegistrationRequestIsQuietAndHasNoFalseEffects(t *testing.T) {
	isolateUXUserHome(t, t.TempDir())
	svc, _, store := fixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := configureUIRegistrationsTest(ctx, svc, viewmodel.RegistrationRequest{
		Key: state.Key{Source: "foreign-windows", Package: "demo", Target: "cluster"},
		URL: "http://127.0.0.1:1/mcp", Transport: "streamable-http", AgentIDs: []string{"claude"},
	})
	if !errors.Is(err, picker.ErrCancelled) {
		t.Fatalf("canceled registration was not normalized to quiet cancellation: result=%+v err=%v", result, err)
	}
	if len(result.Changes) != 0 || len(result.Errors) != 0 {
		t.Fatalf("canceled request claimed or reported a registration effect: %+v", result)
	}
	rows, readErr := store.Installations()
	if readErr != nil || len(rows) != 0 {
		t.Fatalf("canceled request changed the ledger: rows=%+v err=%v", rows, readErr)
	}
}

func TestUXPartialRegistrationFailureReportsAgentStepAndTarget(t *testing.T) {
	home := t.TempDir()
	isolateUXUserHome(t, home)
	svc, _, _ := fixture(t)
	svc.Source.Catalog[0].MCP = &catalog.MCP{Name: "demo", Transport: "streamable-http"}
	svc.Options.AgentScopes = map[string]agents.Scope{"claude": {ID: "claude", Home: home, ConfigPathOverride: filepath.Join(home, ".claude.json"), ExplicitHome: true}}
	// A directory at the adapter's real config-file location causes an actual
	// filesystem failure; it cannot be mistaken for a successful registration.
	if err := os.Mkdir(filepath.Join(home, ".claude.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	key := state.Key{Source: svc.Source.ID, Package: "demo", Target: "default"}
	result, err := configureUIRegistrationsTest(context.Background(), svc, viewmodel.RegistrationRequest{
		Key: key, URL: "http://127.0.0.1:1/mcp", Transport: "streamable-http", AgentIDs: []string{"claude"},
	})
	if err == nil || len(result.Errors) != 1 || len(result.Changes) != 0 {
		t.Fatalf("registration failure was not reported as an actual failed effect: result=%+v err=%v", result, err)
	}
	if result.Step != "register agent" || result.Target != "default" {
		t.Fatalf("failed agent operation metadata missing: step=%q target=%q", result.Step, result.Target)
	}
}

func TestUXRemoveDoesNotStopServer(t *testing.T) {
	home := t.TempDir()
	isolateUXUserHome(t, home)
	svc, _, store := fixture(t)
	svc.Source.Catalog[0].MCP = &catalog.MCP{Name: "demo", Transport: "streamable-http"}
	svc.Options.AgentScopes = map[string]agents.Scope{"claude": {ID: "claude", Home: home, ConfigPathOverride: filepath.Join(home, ".claude.json"), ExplicitHome: true}}
	runtime := &changedRuntime{}
	svc.Options.Runtime = runtime
	key := state.Key{Source: svc.Source.ID, Package: "demo", Target: "default"}
	endpoint := "http://127.0.0.1:1/mcp"
	added, err := configureUIRegistrationsTest(context.Background(), svc, viewmodel.RegistrationRequest{
		Key: key, URL: endpoint, Transport: "streamable-http", AgentIDs: []string{"claude"},
	})
	if err != nil || len(added.Changes) < 1 {
		t.Fatalf("could not create the isolated local registration fixture: result=%+v err=%v", added, err)
	}
	configPath := filepath.Join(home, ".claude.json")
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("named agent config was not actually written: %v", err)
	}
	removed, err := configureUIRegistrationsTest(context.Background(), svc, viewmodel.RegistrationRequest{Key: key, URL: endpoint, Transport: "streamable-http"})
	if err != nil || len(removed.Changes) < 1 {
		t.Fatalf("could not remove the isolated local registration: result=%+v err=%v", removed, err)
	}
	if runtime.starts != 0 || runtime.stops != 0 {
		t.Fatalf("local registration removal changed MCP runtime: starts=%d stops=%d", runtime.starts, runtime.stops)
	}
	rows, err := store.Installations()
	if err != nil || len(rows) != 0 {
		t.Fatalf("registration ledger still contains removed rows: rows=%+v err=%v", rows, err)
	}
}
