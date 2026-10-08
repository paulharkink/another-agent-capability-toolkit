package app

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/mcp"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

type diagnosticAuthExecutor struct {
	authRequired        bool
	prepareAuthRequired bool
	emptyDiagnostic     bool
}

func (e diagnosticAuthExecutor) Run(_ context.Context, _ []string, _ string, input []byte, _ map[string]string, _ func([]byte)) ([]byte, error) {
	var request struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(input, &request); err != nil {
		return nil, err
	}
	if request.Action == "authenticate" && e.authRequired || request.Action == "prepare" && e.prepareAuthRequired {
		if e.emptyDiagnostic {
			return []byte(`{"auth_required":true}`), nil
		}
		return []byte(`{"auth_required":true,"diagnostic":"Provider rejected the saved credential (HTTP 401)."}`), nil
	}
	if request.Action == "prepare" {
		return []byte(`{"runtime":{"image":"fixture/image","host":"127.0.0.1","container_port":9000,"transport":"streamable-http","endpoint_path":"/mcp"}}`), nil
	}
	return []byte(`{}`), nil
}

type task2Runtime struct{}

func (task2Runtime) Start(context.Context, state.Key, mcp.RunSpec) (mcp.Instance, error) {
	return mcp.Instance{URL: "http://127.0.0.1:9000/mcp"}, nil
}
func (task2Runtime) Stop(context.Context, state.Key) error        { return nil }
func (task2Runtime) List(context.Context) ([]mcp.Instance, error) { return nil, nil }
func (task2Runtime) Logs(context.Context, state.Key) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}

func task2AuthProfile(t *testing.T, svc *Service) (string, state.Key) {
	t.Helper()
	_, agentID := profileTestAgent(t, svc, "opencode")
	pkg := &svc.Source.Catalog[0]
	pkg.Skill = nil
	pkg.Inputs = nil
	pkg.MCP = &catalog.MCP{Image: "fixture/image", Transport: "streamable-http", ContainerPort: 9000, EndpointPath: "/mcp", Actions: map[string]catalog.Command{
		"authenticate": {Argv: []string{"fixture-auth"}},
		"prepare":      {Argv: []string{"fixture-prepare"}},
	}}
	svc.Options.Runtime = task2Runtime{}
	return agentID, state.Key{Source: "fixture", Package: "demo", Target: "default"}
}

func TestApplyProfileReportsAuthenticationCompleteOnlyAfterSuccessfulAction(t *testing.T) {
	svc, _, _ := fixture(t)
	agentID, _ := task2AuthProfile(t, svc)
	svc.Options.Runner = diagnosticAuthExecutor{}
	result, err := svc.ApplyProfile(context.Background(), ProfileRequest{Ref: writeProfileForTest(t, svc, "demo", "default", ""), DestinationIDs: []string{agentID}})
	if err != nil {
		t.Fatal(err)
	}
	if result.AuthenticationStatus != viewmodel.AuthenticationComplete || len(result.Changes) == 0 {
		t.Fatalf("successful authentication and actual registration not reported distinctly: %+v", result)
	}
}

func TestApplyProfileDoesNotInferAuthenticationFromSavedCredentialFile(t *testing.T) {
	svc, _, store := fixture(t)
	agentID, key := task2AuthProfile(t, svc)
	svc.Source.Catalog[0].MCP.CredentialFiles = []string{"session.bin"}
	if err := os.MkdirAll(store.AuthDir(key), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.AuthDir(key), "session.bin"), []byte("credential fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	svc.Options.Runner = diagnosticAuthExecutor{}
	result, err := svc.ApplyProfile(context.Background(), ProfileRequest{Ref: writeProfileForTest(t, svc, "demo", "default", ""), DestinationIDs: []string{agentID}})
	if err != nil {
		t.Fatal(err)
	}
	if result.AuthenticationStatus != viewmodel.AuthenticationNotConfirmed || len(result.Changes) == 0 {
		t.Fatalf("saved credential presence was mistaken for completed authentication: %+v", result)
	}
}

func TestApplyProfileSurfacesAuthDiagnosticBeforeRetryAndStopsBeforeRegistration(t *testing.T) {
	svc, _, _ := fixture(t)
	agentID, _ := task2AuthProfile(t, svc)
	svc.Options.Runner = diagnosticAuthExecutor{authRequired: true}
	result, err := svc.ApplyProfile(context.Background(), ProfileRequest{Ref: writeProfileForTest(t, svc, "demo", "default", ""), DestinationIDs: []string{agentID}})
	if err == nil || !strings.HasPrefix(err.Error(), "Provider rejected the saved credential (HTTP 401).") {
		t.Fatalf("provider cause was not primary: result=%+v err=%v", result, err)
	}
	if !strings.Contains(err.Error(), "aact mcp authenticate demo") || !result.Saved || len(result.Changes) != 0 {
		t.Fatalf("retry or partial-effect status missing: result=%+v err=%v", result, err)
	}
}

func TestRunProfileMCPAuthRequiredUsesProviderDiagnostic(t *testing.T) {
	svc, _, _ := fixture(t)
	_, _ = task2AuthProfile(t, svc)
	svc.Options.Runner = diagnosticAuthExecutor{authRequired: true}
	ref := writeProfileForTest(t, svc, "demo", "default", "")
	result, err := svc.RunProfileMCP(context.Background(), "authenticate", ProfileRequest{Ref: ref}, "")
	if err == nil || !strings.HasPrefix(err.Error(), "Provider rejected the saved credential (HTTP 401).") || result.Saved {
		t.Fatalf("auth action did not retain provider diagnostic as primary: result=%+v err=%v", result, err)
	}
}

func TestPrepareAuthRequiredDiagnosticRemainsPrimaryOverRetryGuidance(t *testing.T) {
	svc, _, _ := fixture(t)
	agentID, _ := task2AuthProfile(t, svc)
	svc.Options.Runner = diagnosticAuthExecutor{prepareAuthRequired: true}
	result, err := svc.ApplyProfile(context.Background(), ProfileRequest{Ref: writeProfileForTest(t, svc, "demo", "default", ""), DestinationIDs: []string{agentID}})
	if err == nil || !strings.HasPrefix(err.Error(), "Provider rejected the saved credential (HTTP 401).") || !strings.Contains(err.Error(), "aact mcp authenticate demo") {
		t.Fatalf("prepare auth diagnostic was obscured: result=%+v err=%v", result, err)
	}
}

func TestApplyProfileLegacyAuthRequiredStopsWithActionableIncompleteResult(t *testing.T) {
	svc, _, store := fixture(t)
	agentID, key := task2AuthProfile(t, svc)
	svc.Source.Catalog[0].Inputs = []catalog.Input{
		{Name: "old_auth", Type: "secret", ExclusiveGroup: "auth_method"},
		{Name: "new_auth", Type: "file", ExclusiveGroup: "auth_method"},
	}
	if err := store.SaveAnswersWithActiveInputGroups(key, map[string]any{}, map[string]string{"auth_method": "old_auth"}); err != nil {
		t.Fatal(err)
	}
	exec := diagnosticAuthExecutor{authRequired: true, emptyDiagnostic: true}
	svc.Options.Runner = exec
	ref := writeProfileForTest(t, svc, "demo", "default", "")
	result, err := svc.ApplyProfile(context.Background(), ProfileRequest{Ref: ref, DestinationIDs: []string{agentID}, ActiveInputGroups: map[string]string{"auth_method": "new_auth"}})
	if err != nil || !result.Saved || result.AuthenticationStatus != viewmodel.AuthenticationNotConfirmed || len(result.Changes) != 0 || len(result.Errors) == 0 {
		t.Fatalf("legacy auth-required result claimed completion or lost action guidance: result=%+v err=%v", result, err)
	}
	child := mcpProfileKey(key, svc.Source.Catalog[0], *svc.Source.Catalog[0].MCP)
	pending, readErr := store.PendingAuthInputGroups(key, child.ID())
	if readErr != nil || pending["auth_method"] != "new_auth" {
		t.Fatalf("incomplete method transition was cleared: pending=%#v err=%v", pending, readErr)
	}
}
