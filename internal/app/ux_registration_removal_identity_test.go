package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/agents"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

func TestUXRegistrationRemovalRejectsAmbiguousAgentOnlyIdentity(t *testing.T) {
	base := t.TempDir()
	isolateUXUserHome(t, base)
	svc, _, store := fixture(t)
	runtime := &changedRuntime{}
	svc.Options.Runtime = runtime
	key := state.Key{Source: "foreign-windows", Package: "demo", Target: "cluster"}
	homeA := filepath.Join(base, "home-a")
	homeB := filepath.Join(base, "home-b")
	pathA := filepath.Join(homeA, ".claude.json")
	pathB := filepath.Join(homeB, ".claude.json")
	endpoint := "http://127.0.0.1:8765/mcp"
	_, err := svc.ConfigureRegistrations(context.Background(), RegistrationRequest{
		Key: key, URL: endpoint, Transport: "streamable-http", Agents: []agents.Environment{
			{ID: "claude", Kind: "claude", Home: homeA, ConfigPath: pathA},
			{ID: "claude", Kind: "claude", Home: homeB, ConfigPath: pathB},
		},
	})
	if err != nil {
		t.Fatalf("seeded two real Claude registrations: %v", err)
	}
	skillPath := filepath.Join(base, "skills", "demo")
	if err := store.Record(state.Installation{Key: key, AgentID: "claude", AgentKind: "claude", Component: "skill", Destination: skillPath}); err != nil {
		t.Fatal(err)
	}

	result, err := svc.UIConfigureRegistrations(context.Background(), viewmodel.RegistrationRequest{
		Key: key, RemoveAgentIDs: []string{"claude"},
	})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "locate") {
		t.Fatalf("unavailable source should require locating the package: result=%+v err=%v", result, err)
	}
	for _, path := range []string{pathA, pathB} {
		contents, readErr := os.ReadFile(path)
		if readErr != nil || !strings.Contains(string(contents), endpoint) {
			t.Fatalf("ambiguous removal changed config %s: contents=%s err=%v", path, contents, readErr)
		}
	}
	rows, readErr := store.Installations()
	if readErr != nil {
		t.Fatal(readErr)
	}
	var mcpRows, skillRows int
	for _, row := range rows {
		if row.Key != key {
			continue
		}
		if row.Component == "mcp" && row.AgentID == "claude" {
			mcpRows++
		}
		if row.Component == "skill" && row.AgentID == "claude" {
			skillRows++
		}
	}
	if mcpRows != 2 || skillRows != 1 {
		t.Fatalf("ambiguous removal changed actual ledger scope: mcp=%d skill=%d rows=%+v", mcpRows, skillRows, rows)
	}
	if runtime.starts != 0 || runtime.stops != 0 {
		t.Fatalf("registration removal changed runtime: starts=%d stops=%d", runtime.starts, runtime.stops)
	}
}

func TestUXExactRegistrationRemovalKeepsUnselectedDestinationAndSkill(t *testing.T) {
	base := t.TempDir()
	isolateUXUserHome(t, base)
	svc, _, store := fixture(t)
	runtime := &changedRuntime{}
	svc.Options.Runtime = runtime
	key := state.Key{Source: "foreign-windows", Package: "demo", Target: "cluster"}
	homeA := filepath.Join(base, "home-a")
	homeB := filepath.Join(base, "home-b")
	pathA := filepath.Join(homeA, ".claude.json")
	pathB := filepath.Join(homeB, ".claude.json")
	endpoint := "http://127.0.0.1:8765/mcp"
	_, err := svc.ConfigureRegistrations(context.Background(), RegistrationRequest{
		Key: key, URL: endpoint, Transport: "streamable-http", Agents: []agents.Environment{
			{ID: "claude", Kind: "claude", Home: homeA, ConfigPath: pathA},
			{ID: "claude", Kind: "claude", Home: homeB, ConfigPath: pathB},
		},
	})
	if err != nil {
		t.Fatalf("seeded two real Claude registrations: %v", err)
	}
	skillPath := filepath.Join(base, "skills", "demo")
	if err := store.Record(state.Installation{Key: key, AgentID: "claude", AgentKind: "claude", Component: "skill", Destination: skillPath}); err != nil {
		t.Fatal(err)
	}
	var request viewmodel.RegistrationRequest
	requestJSON, err := json.Marshal(viewmodel.RegistrationRequest{
		Key: key, RemoveRegistrations: []viewmodel.RegistrationIdentity{{AgentID: "claude", Destination: pathA}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(requestJSON, &request); err != nil {
		t.Fatal(err)
	}
	result, err := svc.UIConfigureRegistrations(context.Background(), request)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "locate") {
		t.Fatalf("unavailable source did not require locating the package: result=%+v err=%v", result, err)
	}
	if !result.Connection.CheckedAt.IsZero() {
		t.Fatalf("exact removal unexpectedly checked the endpoint: %+v", result.Connection)
	}
	contentsA, errA := os.ReadFile(pathA)
	if errA != nil || !strings.Contains(string(contentsA), endpoint) {
		t.Fatalf("unavailable source changed selected config: content=%s err=%v", contentsA, errA)
	}
	contentsB, errB := os.ReadFile(pathB)
	if errB != nil || !strings.Contains(string(contentsB), endpoint) {
		t.Fatalf("unselected config was changed: content=%s err=%v", contentsB, errB)
	}
	rows, readErr := store.Installations()
	if readErr != nil {
		t.Fatal(readErr)
	}
	var remainingMCP, remainingSkill int
	for _, row := range rows {
		if row.Key != key || row.AgentID != "claude" {
			continue
		}
		if row.Component == "mcp" {
			remainingMCP++
		}
		if row.Component == "skill" && row.Destination == skillPath {
			remainingSkill++
		}
	}
	if remainingMCP != 2 || remainingSkill != 1 {
		t.Fatalf("unavailable source changed registration ledger: rows=%+v", rows)
	}
	if runtime.starts != 0 || runtime.stops != 0 {
		t.Fatalf("registration removal changed runtime: starts=%d stops=%d", runtime.starts, runtime.stops)
	}
}

func TestUXMixedExactAndAmbiguousLegacyRemovalRejectsBeforeMutation(t *testing.T) {
	base := t.TempDir()
	isolateUXUserHome(t, base)
	svc, _, store := fixture(t)
	key := state.Key{Source: "foreign-windows", Package: "demo", Target: "cluster"}
	homeA := filepath.Join(base, "home-a")
	homeB := filepath.Join(base, "home-b")
	pathA := filepath.Join(homeA, ".claude.json")
	pathB := filepath.Join(homeB, ".claude.json")
	endpoint := "http://127.0.0.1:8765/mcp"
	_, err := svc.ConfigureRegistrations(context.Background(), RegistrationRequest{
		Key: key, URL: endpoint, Transport: "streamable-http", Agents: []agents.Environment{
			{ID: "claude", Kind: "claude", Home: homeA, ConfigPath: pathA},
			{ID: "claude", Kind: "claude", Home: homeB, ConfigPath: pathB},
		},
	})
	if err != nil {
		t.Fatalf("seeded two real Claude registrations: %v", err)
	}
	// The exact request asks for one known row while the legacy ID is
	// ambiguous. The service must reject the mixed request before touching
	// either actual config or its ledger.
	var request viewmodel.RegistrationRequest
	requestJSON, err := json.Marshal(viewmodel.RegistrationRequest{
		Key: key, RemoveAgentIDs: []string{"claude"},
		RemoveRegistrations: []viewmodel.RegistrationIdentity{{AgentID: "claude", Destination: pathA}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(requestJSON, &request); err != nil {
		t.Fatal(err)
	}
	result, err := svc.UIConfigureRegistrations(context.Background(), request)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "mix") {
		t.Fatalf("mixed exact and ambiguous legacy removal should be rejected, result=%+v err=%v", result, err)
	}
	for _, path := range []string{pathA, pathB} {
		contents, readErr := os.ReadFile(path)
		if readErr != nil || !strings.Contains(string(contents), endpoint) {
			t.Fatalf("rejected mixed removal changed config %s: contents=%s err=%v", path, contents, readErr)
		}
	}
	rows, readErr := store.Installations()
	if readErr != nil {
		t.Fatal(readErr)
	}
	var count int
	for _, row := range rows {
		if row.Key == key && row.AgentID == "claude" && row.Component == "mcp" {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("rejected mixed removal mutated ledger: rows=%+v", rows)
	}
}
