package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/app"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/mcp"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/vault"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/viewmodel"
)

func TestClusterAuthMethodActionFixture(t *testing.T) {
	if os.Getenv("AACT_CLUSTER_METHOD_FIXTURE") != "1" {
		return
	}
	var request mcp.ActionRequest
	if err := json.NewDecoder(os.Stdin).Decode(&request); err != nil {
		os.Exit(2)
	}
	token, _ := request.Inputs["token"].(string)
	password, _ := request.Inputs["password"].(string)
	method := "invalid"
	if token != "" && password == "" {
		method = "token"
	}
	if token == "" && password != "" {
		method = "password"
	}
	if err := os.WriteFile(os.Getenv("AACT_CLUSTER_METHOD_RESULT"), []byte(method), 0600); err != nil {
		os.Exit(3)
	}
	fmt.Println(`{"auth_required":false}`)
	os.Exit(0)
}

func TestClusterAuthenticationUsesSelectedSavedMethod(t *testing.T) {
	p, err := catalog.Load(filepath.Join("..", "..", "packages", "cluster-inspector"))
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	p.MCP.Actions["authenticate"] = catalog.Command{Argv: []string{executable, "-test.run=^TestClusterAuthMethodActionFixture$"}}
	p.MCP.ReleaseImage = ""
	p.RawManifest = nil
	root := t.TempDir()
	store, err := state.Open(filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	secret, err := vault.Open(filepath.Join(root, "credentials.kdbx"), []byte("fixture-master-password"))
	if err != nil {
		t.Fatal(err)
	}
	store.SetSecretBackend(secret)
	source := config.Pack{ID: "fixture", Root: root, ProfileRoot: filepath.Join(root, "instances"), Catalog: []catalog.Package{p}}.LegacySource()
	runtime := &integratedRuntime{store: store}
	svc := app.New(source, store, app.Options{Runtime: runtime})
	ref := config.InstanceRef{PackID: "fixture", CapabilityID: "cluster-inspector", Name: "fixture"}
	if err := svc.CreateInstance(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AACT_CLUSTER_METHOD_FIXTURE", "1")
	result := filepath.Join(root, "selected")
	t.Setenv("AACT_CLUSTER_METHOD_RESULT", result)
	steps := []struct {
		method string
		inputs map[string]any
	}{
		{"token", map[string]any{"api_server": "https://cluster.test", "registration_name": "fixture", "token": "fixture-token"}},
		{"password", map[string]any{"username": "fixture-user", "password": "fixture-password"}},
		{"token", map[string]any{}},
	}
	for _, step := range steps {
		req := viewmodel.SetupInstallRequest{SetupRequest: viewmodel.SetupRequest{Ref: ref}, Inputs: step.inputs, ActiveInputGroups: map[string]string{"cluster_credentials": step.method}}
		if err := svc.UISaveInputs(context.Background(), req); err != nil {
			t.Fatalf("save %s: %v", step.method, err)
		}
		if _, err := svc.RunInstanceMCP(context.Background(), "authenticate", app.InstanceRequest{Ref: ref}, ""); err != nil {
			t.Fatalf("authenticate %s: %v", step.method, err)
		}
		actual, err := os.ReadFile(result)
		if err != nil || string(actual) != step.method {
			t.Fatalf("selected %s, action received %q (%v)", step.method, actual, err)
		}
	}
	if _, err := svc.RunInstanceMCP(context.Background(), "authenticate", app.InstanceRequest{Ref: ref, Inputs: map[string]any{"password": "replacement"}}, ""); err != nil {
		t.Fatalf("explicit password: %v", err)
	}
	actual, err := os.ReadFile(result)
	if err != nil || string(actual) != "password" {
		t.Fatalf("explicit password selected %q (%v)", actual, err)
	}
	if _, err := svc.RunInstanceMCP(context.Background(), "authenticate", app.InstanceRequest{Ref: ref, Inputs: map[string]any{"password": "replacement", "token": "replacement"}}, ""); err == nil {
		t.Fatal("conflicting explicit credentials accepted")
	}
}
