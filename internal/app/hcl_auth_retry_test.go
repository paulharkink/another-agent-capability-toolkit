package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

type hclAuthRetryExecutor struct {
	calls     []uxActionCall
	responses int
}

func (e *hclAuthRetryExecutor) Run(_ context.Context, argv []string, _ string, data []byte, _ map[string]string, _ func([]byte)) ([]byte, error) {
	var request uxActionCall
	if err := json.Unmarshal(data, &request); err != nil {
		return nil, err
	}
	e.calls = append(e.calls, request)
	if request.Action == "authenticate" {
		e.responses++
		if e.responses == 1 {
			return []byte(`{"auth_required":true}`), nil
		}
		if len(argv) != 2 || argv[1] != base64.StdEncoding.EncodeToString([]byte("submitted-final-token")) {
			return nil, os.ErrInvalid
		}
	}
	return []byte(`{}`), nil
}

func TestUIInstallRetriesHCLDerivedCredentialAfterPendingAuth(t *testing.T) {
	s, _, store := fixture(t)
	ref, agentID := profileTestAgent(t, s, "opencode")
	manifest := `schema_version = 1
id = "demo"
name = "Demo"
[[inputs]]
name = "submitted_literal"
type = "string"
[[inputs]]
name = "token"
type = "secret"
exclusive_group = "credential_method"
default = "${ inputs.submitted_literal }"
[[inputs]]
name = "source_file"
type = "file"
exclusive_group = "credential_method"
default = "./default-credentials.json"
[skill]
name = "demo"
[mcp]
name = "service"
image = "fixture/image"
transport = "streamable-http"
token_input = "token"
token_header = "${ base64encode(inputs.token) }"
credential_files = ["managed.bin"]
[mcp.actions.authenticate]
command = ["fixture-auth", "${ base64encode(inputs.token) }"]
`
	pkgDir := s.Source.Catalog[0].Dir
	if err := os.WriteFile(filepath.Join(pkgDir, "package.toml"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	pkg, err := catalog.Load(pkgDir)
	if err != nil {
		t.Fatal(err)
	}
	pkg.Sets = []catalog.ComponentSet{{Name: "core", MCPs: []string{"service"}}}
	s.Source.Catalog[0] = pkg
	s.Source.ProfileRoot = filepath.Join(t.TempDir(), "profiles")
	ref = writeProfileForTest(t, s, "demo", "default", "")
	s.Options.Runtime = &fakeRuntime{}
	a := &profileAdapter{home: t.TempDir()}
	s.Options.Adapters = fixtureAdapters{a}
	executor := &hclAuthRetryExecutor{}
	s.Options.Runner = executor

	key := state.Key{Source: s.Source.ID, Package: "demo", Target: "default"}
	definition := pkg.MCPDefinitions()[0]
	child := mcpProfileKey(key, pkg, definition)
	if err := store.SaveAnswersWithActiveInputGroups(key, map[string]any{"source_file": "./old-credentials.json"}, map[string]string{"credential_method": "source_file"}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(store.AuthDir(child), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.AuthDir(child), "managed.bin"), []byte("old managed credential"), 0600); err != nil {
		t.Fatal(err)
	}

	request := viewmodel.SetupInstallRequest{
		Ref: ref, ItemIDs: []string{"set:core"}, DestinationIDs: []string{agentID},
		Inputs:            map[string]any{"submitted_literal": "submitted-final-token"},
		ActiveInputGroups: map[string]string{"credential_method": "token"},
	}
	first, err := s.UIInstall(context.Background(), request)
	if err != nil || len(first.Errors) == 0 || len(a.calls) != 0 {
		t.Fatalf("auth-required attempt did not stop before MCP effects: result=%+v err=%v effects=%v", first, err, a.calls)
	}
	answers, err := store.Answers(key)
	if err != nil || answers["submitted_literal"] != "submitted-final-token" || answers["token"] != nil {
		t.Fatalf("ordinary submitted input or declared-secret omission changed: answers=%#v err=%v", answers, err)
	}
	activeGroups, err := store.ActiveInputGroups(key)
	if err != nil || activeGroups["credential_method"] != "token" {
		t.Fatalf("saved auth method metadata = %#v, %v", activeGroups, err)
	}
	pending, err := store.PendingAuthInputGroups(key, child.ID())
	if err != nil || pending["credential_method"] != "token" {
		t.Fatalf("selected method was not retained as pending: %#v, %v", pending, err)
	}
	if len(executor.calls) != 1 || executor.calls[0].Inputs["token"] != "submitted-final-token" {
		t.Fatalf("first auth did not receive final literal input: %#v", executor.calls)
	}

	second, err := s.UIInstall(context.Background(), request)
	if err != nil || second.AuthenticationStatus != viewmodel.AuthenticationComplete || len(second.Changes) == 0 || len(a.calls) == 0 {
		t.Fatalf("successful retry did not confirm authentication and apply MCP: result=%+v err=%v effects=%v", second, err, a.calls)
	}
	pending, err = store.PendingAuthInputGroups(key, child.ID())
	if err != nil || len(pending) != 0 {
		t.Fatalf("successful auth left selected method pending: %#v, %v", pending, err)
	}
	if len(executor.calls) != 2 || executor.calls[1].Inputs["token"] != "submitted-final-token" {
		t.Fatalf("retry did not repeat auth with the final literal: %#v", executor.calls)
	}
	answers, err = store.Answers(key)
	if err != nil {
		t.Fatal(err)
	}
	if answers["token"] != nil || answers["submitted_literal"] != "submitted-final-token" {
		t.Fatalf("ordinary submitted input or declared-secret omission changed after retry: %#v", answers)
	}
	raw, err := os.ReadFile(filepath.Join(store.Root(), "answers", key.ID()+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var record struct {
		Values map[string]any `json:"values"`
	}
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatal(err)
	}
	if record.Values["submitted_literal"] != "submitted-final-token" {
		t.Fatalf("ordinary submitted input was not persisted as input data: %#v", record.Values)
	}
	if _, persisted := record.Values["token"]; persisted {
		t.Fatalf("declared secret input key was persisted: %#v", record.Values)
	}
}
