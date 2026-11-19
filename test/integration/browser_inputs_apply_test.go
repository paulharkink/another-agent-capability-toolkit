package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/app"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/mcp"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/vault"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/viewmodel"
)

type browserInputsRuntime struct {
	instance         mcp.Instance
	browser          atomic.Bool
	deny             atomic.Bool
	reject           atomic.Bool
	breakReplacement atomic.Bool
	viewerFailure    atomic.Bool
}

func (r *browserInputsRuntime) List(context.Context) ([]mcp.Instance, error) {
	return []mcp.Instance{r.instance}, nil
}
func (r *browserInputsRuntime) Logs(context.Context, state.Key) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}
func (r *browserInputsRuntime) Stop(context.Context, state.Key) error {
	r.instance.Status = "stopped"
	return nil
}
func (r *browserInputsRuntime) StartBrowser(context.Context, state.Key) (string, error) {
	if r.viewerFailure.Load() {
		return "", errors.New("synthetic viewer unavailable")
	}
	return "http://localhost:19771", nil
}
func (r *browserInputsRuntime) Start(_ context.Context, key state.Key, spec mcp.RunSpec) (mcp.Instance, error) {
	if r.breakReplacement.Load() {
		if r.instance.Status == "running" {
			return mcp.Instance{}, mcp.ErrRunningWithDifferentSettings
		}
		return mcp.Instance{}, errors.New("synthetic replacement failure")
	}
	if r.reject.Load() {
		return mcp.Instance{}, errors.New("synthetic update failure")
	}
	r.instance.Key = key
	r.instance.Status = "running"
	r.browser.Store(spec.BrowserController != nil)
	return r.instance, nil
}

func TestSavingBrowserInputsAppliesToRunningInstance(t *testing.T) {
	ctx := context.Background()
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	secrets, err := vault.Open(filepath.Join(root, "vault.kdbx"), []byte("synthetic-vault-password"))
	if err != nil {
		t.Fatal(err)
	}
	store.SetSecretBackend(secrets)
	key := state.Key{Source: "fixture", Package: "grafana", Target: "ot"}
	runtime := &browserInputsRuntime{instance: mcp.Instance{Key: key, ID: "fixture", Status: "running", Ownership: "local"}}
	var protocolFailure atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/mcp" {
			if protocolFailure.Load() {
				http.Error(w, "fixture MCP unavailable", http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-03-26"}}`)
			return
		}
		json.NewEncoder(w).Encode(map[string]bool{"authenticated": runtime.browser.Load() && !runtime.deny.Load()})
	}))
	defer server.Close()
	runtime.instance.URL = server.URL + "/mcp"
	p := catalog.Package{ID: "grafana", Name: "Grafana", Dir: root, Inputs: []catalog.Input{{Name: "auth_mode", Type: "choice", Default: "session_cookie", Options: []catalog.Choice{{Value: "session_cookie", Label: "Cookie"}, {Value: "browser_credentials", Label: "Browser"}}}}, MCP: &catalog.MCP{Name: "grafana", Image: "fixture", Transport: "streamable-http", ContainerPort: 8765, HealthPath: "/health", BrowserController: &catalog.BrowserController{When: map[string]any{"auth_mode": "browser_credentials"}}}}
	svc := app.New(config.Pack{ID: "fixture", Root: root, ProfileRoot: filepath.Join(root, "environments"), Catalog: []catalog.Package{p}}.LegacySource(), store, app.Options{Runtime: runtime})
	ref := config.InstanceRef{PackID: "fixture", CapabilityID: "grafana", Name: "ot"}
	if err := svc.CreateInstance(ctx, ref); err != nil {
		t.Fatal(err)
	}
	req := viewmodel.SetupInstallRequest{SetupRequest: viewmodel.SetupRequest{Ref: ref}, Inputs: map[string]any{"auth_mode": "browser_credentials"}}
	if err := svc.UISaveInputs(ctx, req); err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(server.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var result map[string]bool
	if err = json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if !result["authenticated"] {
		t.Fatal("saving browser credentials left the running instance on cookie authentication")
	}
	runtime.deny.Store(true)
	if err := svc.UISaveInputs(ctx, req); err == nil || !strings.Contains(err.Error(), "http://localhost:19771") {
		t.Fatalf("failed browser login should show the viewer URL: %v", err)
	}
	runtime.viewerFailure.Store(true)
	if err := svc.UISaveInputs(ctx, req); err == nil || !strings.Contains(err.Error(), "synthetic viewer unavailable") {
		t.Fatalf("viewer startup error should be visible after saving: %v", err)
	}
	runtime.viewerFailure.Store(false)
	runtime.deny.Store(false)
	protocolFailure.Store(true)
	if err := svc.UISaveInputs(ctx, req); err == nil || !strings.Contains(err.Error(), "MCP protocol") {
		t.Fatalf("unavailable MCP protocol should be reported: %v", err)
	}
	protocolFailure.Store(false)
	req.Inputs["auth_mode"] = "session_cookie"
	runtime.reject.Store(true)
	if err := svc.UISaveInputs(ctx, req); err == nil || !strings.Contains(err.Error(), "inputs saved") {
		t.Fatalf("failed update should report that inputs were saved: %v", err)
	}
	runtime.reject.Store(false)
	if err := svc.UISaveInputs(ctx, req); err != nil {
		t.Fatal(err)
	}
	resp, err = http.Get(server.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if result["authenticated"] {
		t.Fatal("switching back to cookie authentication left the browser settings active")
	}
	req.Inputs["auth_mode"] = "browser_credentials"
	runtime.breakReplacement.Store(true)
	if err := svc.UISaveInputs(ctx, req); err == nil || !strings.Contains(err.Error(), "start it again") {
		t.Fatalf("failed replacement should explain how to recover: %v", err)
	}
	runtime.breakReplacement.Store(false)
	if err := svc.UISaveInputs(ctx, req); err != nil {
		t.Fatal(err)
	}
	snapshot, err := svc.InstanceSnapshot(ctx, "grafana")
	if err != nil {
		t.Fatal(err)
	}
	if got := snapshot.Instances[0].MCPs[0].Status; got != "stopped" {
		t.Fatalf("saving browser inputs started a stopped MCP: %s", got)
	}
	// A saved provider token must never be written into agent configuration,
	// even if a stale registration would otherwise need a refresh.
	runtime.instance.Status = "running"
	svc.Source.Catalog[0].MCP.TokenHeader = "Authorization"
	svc.Source.Catalog[0].MCP.TokenInput = "token"
	svc.Source.Catalog[0].Inputs = append(svc.Source.Catalog[0].Inputs, catalog.Input{Name: "token", Type: "secret"})
	if err := store.Record(state.Installation{Key: key, AgentID: "opencode", Component: "mcp", URL: "http://localhost:1234/mcp", RegistrationName: "old", Destination: filepath.Join(root, "agent.json")}); err != nil {
		t.Fatal(err)
	}
	req.Inputs["token"] = "synthetic-provider-secret"
	runtime.deny.Store(true)
	err = svc.UISaveInputs(ctx, req)
	if err == nil || !strings.Contains(err.Error(), "agent registration needs refreshing") || !strings.Contains(err.Error(), "http://localhost:19771") || strings.Contains(err.Error(), "synthetic-provider-secret") {
		t.Fatalf("unsafe agent registration refresh was not rejected: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "agent.json")); !os.IsNotExist(err) {
		t.Fatalf("agent configuration was unexpectedly written: %v", err)
	}
	_, err = svc.RunInstanceMCP(ctx, "start", app.InstanceRequest{Ref: ref}, "")
	if err == nil || !strings.Contains(err.Error(), "cannot store provider credentials") || strings.Contains(err.Error(), "synthetic-provider-secret") {
		t.Fatalf("manual recovery did not reject unsafe agent credentials: %v", err)
	}
}

func TestAuthenticateBrowserCredentialsStartsLogin(t *testing.T) {
	ctx := context.Background()
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	key := state.Key{Source: "fixture", Package: "grafana", Target: "fresh"}
	runtime := &browserInputsRuntime{instance: mcp.Instance{Key: key, ID: "fixture", Status: "stopped", Ownership: "local"}}
	var denied atomic.Bool
	var healthFailure atomic.Bool
	var slowLogin atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/mcp" {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-03-26"}}`)
			return
		}
		if healthFailure.Load() {
			http.Error(w, "synthetic health failure", http.StatusServiceUnavailable)
			return
		}
		if slowLogin.Load() {
			time.Sleep(31 * time.Second)
		}
		json.NewEncoder(w).Encode(map[string]bool{"authenticated": runtime.browser.Load() && !denied.Load()})
	}))
	defer server.Close()
	runtime.instance.URL = server.URL + "/mcp"
	p := catalog.Package{ID: "grafana", Name: "Grafana", Dir: root, Inputs: []catalog.Input{{Name: "auth_mode", Type: "choice", Default: "browser_credentials", Options: []catalog.Choice{{Value: "browser_credentials", Label: "Browser"}, {Value: "session_cookie", Label: "Cookie"}}}}, MCP: &catalog.MCP{Name: "grafana", Image: "fixture", Transport: "streamable-http", ContainerPort: 8765, HealthPath: "/health", BrowserController: &catalog.BrowserController{When: map[string]any{"auth_mode": "browser_credentials"}}, Actions: map[string]catalog.Command{"authenticate": {Argv: []string{"/bin/sh", "-c", `printf '{"auth_required":false}'`}}}}}
	svc := app.New(config.Pack{ID: "fixture", Root: root, ProfileRoot: filepath.Join(root, "environments"), Catalog: []catalog.Package{p}}.LegacySource(), store, app.Options{Runtime: runtime})
	ref := config.InstanceRef{PackID: "fixture", CapabilityID: "grafana", Name: "fresh"}
	if err := svc.CreateInstance(ctx, ref); err != nil {
		t.Fatal(err)
	}
	runtime.instance.Status = "running"
	viewer, err := svc.UIConfigurationInstanceRun(ctx, "browser-login", ref, "grafana")
	if err != nil || strings.Contains(viewer, "http://localhost:19771") {
		t.Fatalf("browser login should try saved credentials before opening the viewer: %q %v", viewer, err)
	}
	response, err := http.Get(server.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	var browserResult map[string]bool
	if err = json.NewDecoder(response.Body).Decode(&browserResult); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if !browserResult["authenticated"] {
		t.Fatal("browser login did not authenticate the MCP")
	}
	runtime.instance.Status = "stopped"
	runtime.browser.Store(false)
	if _, err := svc.UIConfigurationInstanceRun(ctx, "authenticate", ref, "grafana"); err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(server.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var result map[string]bool
	if err = json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if !result["authenticated"] {
		t.Fatal("Authenticate reported success without starting browser login")
	}
	slowLogin.Store(true)
	if _, err := svc.UIConfigurationInstanceRun(ctx, "authenticate", ref, "grafana"); err != nil {
		t.Fatalf("slow browser authentication did not complete: %v", err)
	}
	slowLogin.Store(false)
	stale := state.Installation{Key: key, AgentID: "opencode", Component: "mcp", URL: "http://localhost:1234/mcp", RegistrationName: "old", Destination: filepath.Join(root, "agent.json")}
	if err := store.Record(stale); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UIConfigurationInstanceRun(ctx, "authenticate", ref, "grafana"); err == nil || !strings.Contains(err.Error(), "agent registration needs refreshing") {
		t.Fatalf("stale agent registration was not reported: %v", err)
	}
	if err := store.Remove(stale); err != nil {
		t.Fatal(err)
	}
	denied.Store(true)
	viewer, err = svc.UIConfigurationInstanceRun(ctx, "authenticate", ref, "grafana")
	if err == nil || !strings.Contains(viewer, "http://localhost:19771") {
		t.Fatalf("failed browser login should report the interactive viewer: %q %v", viewer, err)
	}
	runtime.viewerFailure.Store(true)
	_, err = svc.UIConfigurationInstanceRun(ctx, "authenticate", ref, "grafana")
	if err == nil || !strings.Contains(err.Error(), "synthetic viewer unavailable") {
		t.Fatalf("viewer startup failure should be reported: %v", err)
	}
	runtime.viewerFailure.Store(false)
	healthFailure.Store(true)
	viewer, err = svc.UIConfigurationInstanceRun(ctx, "authenticate", ref, "grafana")
	if err == nil || !strings.Contains(viewer, "http://localhost:19771") {
		t.Fatalf("uncertain browser login should offer the viewer: %q %v", viewer, err)
	}
}
