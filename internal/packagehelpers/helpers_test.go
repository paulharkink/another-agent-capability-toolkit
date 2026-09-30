package packagehelpers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/mcp"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type helperProcess struct {
	Calls       [][]string
	Requests    []mcp.ActionRequest
	FailAccount bool
	AuthJSON    string
	FailAuth    bool
	BuildOutput string
}

func (f *helperProcess) Run(_ context.Context, argv []string, _ string, stdin []byte, _ map[string]string, _ func([]byte)) ([]byte, error) {
	f.Calls = append(f.Calls, append([]string{}, argv...))
	if len(argv) > 1 && argv[1] == "build" {
		return []byte(f.BuildOutput), nil
	}
	if strings.Contains(strings.Join(argv, " "), "auth.py") {
		var q mcp.ActionRequest
		if err := json.Unmarshal(stdin, &q); err != nil {
			return nil, err
		}
		f.Requests = append(f.Requests, q)
		if f.FailAuth {
			return nil, errors.New("authentication fixture failed")
		}
		if f.AuthJSON != "" {
			return []byte(f.AuthJSON), nil
		}
		return []byte(`{"auth_required":true}`), nil
	}
	if f.FailAccount && strings.Contains(strings.Join(argv, " "), "account show") {
		return nil, errors.New("cached account missing")
	}
	return nil, nil
}
func helperRequest(t *testing.T, name string, raw map[string]any) mcp.ActionRequest {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "packages", name))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	target := filepath.Join(root, "target.toml")
	if err = os.WriteFile(target, []byte("# source remains untouched\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return mcp.ActionRequest{ProtocolVersion: 1, Action: "prepare", PackageDir: dir, StateDir: filepath.Join(root, "state"), Target: config.Target{Environment: "lab", Name: "fixture", Path: target, Raw: raw}, Inputs: map[string]any{}}
}

func TestClusterStagesFormValuesAndReferencedResourcesWithoutChangingSource(t *testing.T) {
	q := helperRequest(t, "cluster-inspector", map[string]any{"cluster": map[string]any{"api_server": "https://old.example.test", "ca_file": "ca.pem"}, "mcp": map[string]any{"local_port": int64(8765)}})
	ca := filepath.Join(filepath.Dir(q.Target.Path), "ca.pem")
	os.WriteFile(ca, []byte("synthetic-ca"), 0644)
	q.Inputs = map[string]any{"api_server": "https://edited.example.test", "local_port": int64(18865), "token": "synthetic-secret"}
	f := &helperProcess{AuthJSON: `{"auth_required":false}`}
	h := Helper{Executor: f}
	result, err := h.Run(context.Background(), "cluster-inspector", q)
	if err != nil {
		t.Fatal(err)
	}
	if result.AuthRequired || result.Runtime == nil {
		t.Fatal("valid auth lost runtime")
	}
	if len(f.Requests) != 1 {
		t.Fatal(f.Calls)
	}
	mapped := f.Requests[0]
	if !strings.HasPrefix(mapped.Target.Path, "/config/") || mapped.StateDir != "/state" {
		t.Fatalf("unmapped action paths: %+v", mapped)
	}
	staged := filepath.Join(q.StateDir, "config", "target.toml")
	content, err := os.ReadFile(staged)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "https://edited.example.test") || strings.Contains(string(content), "synthetic-secret") {
		t.Fatal(string(content))
	}
	if _, err = os.Stat(filepath.Join(q.StateDir, "config", "ca.pem")); err != nil {
		t.Fatal(err)
	}
	source, _ := os.ReadFile(q.Target.Path)
	if string(source) != "# source remains untouched\n" {
		t.Fatal(string(source))
	}
}

func TestGrafanaMissingAuthReturnsWithoutRuntimeAndUsesPrivateUser(t *testing.T) {
	q := helperRequest(t, "grafana-inspector", map[string]any{"grafana": map[string]any{"url": "https://grafana.example.test", "auth_mode": "api_token"}})
	f := &helperProcess{}
	h := Helper{Executor: f}
	result, err := h.Run(context.Background(), "grafana-inspector", q)
	if err != nil {
		t.Fatal(err)
	}
	if !result.AuthRequired || result.Runtime != nil {
		t.Fatalf("unexpected result %+v", result)
	}
	args := strings.Join(f.Calls[len(f.Calls)-1], " ")
	if os.Getuid() >= 0 && !strings.Contains(args, "--user") {
		t.Fatal(args)
	}
	if strings.Contains(args, "python3") || !strings.Contains(args, "--entrypoint python") {
		t.Fatal(args)
	}
}

func TestAzureMissingCachedLoginNeverStartsDeviceFlowDuringPrepare(t *testing.T) {
	q := helperRequest(t, "azure-inspector", map[string]any{"azure": map[string]any{"tenant_id": "tenant-fixture", "subscription_id": "subscription-fixture"}})
	f := &helperProcess{FailAccount: true}
	h := Helper{Executor: f}
	result, err := h.Run(context.Background(), "azure-inspector", q)
	if err != nil {
		t.Fatal(err)
	}
	if !result.AuthRequired {
		t.Fatal(result)
	}
	for _, a := range f.Calls {
		if strings.Contains(strings.Join(a, " "), " login ") {
			t.Fatal(a)
		}
	}
}

func TestAzureUnsupportedHintsRejectedBeforeDocker(t *testing.T) {
	q := helperRequest(t, "azure-inspector", map[string]any{"azure": map[string]any{"tenant_id": "t", "subscription_id": "s", "resource_hints": map[string]any{"resource": "unsupported"}}})
	f := &helperProcess{}
	h := Helper{Executor: f}
	_, err := h.Run(context.Background(), "azure-inspector", q)
	if err == nil || len(f.Calls) != 0 {
		t.Fatalf("invalid Azure configuration accepted: %v %+v", err, f.Calls)
	}
}

func TestForgejoReusesScopedTokenAndNormalizesURL(t *testing.T) {
	q := helperRequest(t, "forgejo", map[string]any{"forgejo": map[string]any{"base_url": "https://forgejo.example.test///"}})
	os.MkdirAll(q.StateDir, 0700)
	os.WriteFile(filepath.Join(q.StateDir, "access-token"), []byte("synthetic-token"), 0600)
	h := Helper{Executor: &helperProcess{}}
	result, err := h.Run(context.Background(), "forgejo", q)
	if err != nil {
		t.Fatal(err)
	}
	if result.AuthRequired || result.Runtime == nil {
		t.Fatal(result)
	}
	if result.Runtime.SecretEnv["FORGEJO_ACCESS_TOKEN"] != "synthetic-token" {
		t.Fatal("cached token not reused")
	}
	args := strings.Join(result.Runtime.Args, " ")
	if !strings.Contains(args, "--url https://forgejo.example.test") || strings.Contains(args, "test///") {
		t.Fatal(args)
	}
	if result.Runtime.ContainerPort != 8080 || result.Runtime.EndpointPath != "/mcp" {
		t.Fatal(result)
	}
}

func TestForgejoInvalidReplacementKeepsOldToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/user" || r.Header.Get("Authorization") != "token invalid-fixture" {
			t.Error(r.URL.Path)
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	q := helperRequest(t, "forgejo", map[string]any{"forgejo": map[string]any{"base_url": server.URL}})
	q.Action = "authenticate"
	q.Inputs = map[string]any{"token": "invalid-fixture"}
	os.MkdirAll(q.StateDir, 0700)
	path := filepath.Join(q.StateDir, "access-token")
	os.WriteFile(path, []byte("previous-fixture"), 0600)
	h := Helper{Executor: &helperProcess{}}
	result, err := h.Run(context.Background(), "forgejo", q)
	if err != nil {
		t.Fatal(err)
	}
	if !result.AuthRequired {
		t.Fatal(result)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "previous-fixture" {
		t.Fatal("invalid token replaced previous credentials")
	}
}
func TestForgejoValidExplicitTokenStoredPrivately(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "token valid-fixture" {
			t.Error("wrong token header")
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":1,"login":"fixture"}`)
	}))
	defer server.Close()
	q := helperRequest(t, "forgejo", map[string]any{"forgejo": map[string]any{"base_url": server.URL}})
	q.Action = "authenticate"
	q.Inputs = map[string]any{"token": "valid-fixture"}
	h := Helper{Executor: &helperProcess{}}
	result, err := h.Run(context.Background(), "forgejo", q)
	if err != nil {
		t.Fatal(err)
	}
	if result.Runtime == nil || result.AuthRequired {
		t.Fatal(result)
	}
	info, err := os.Stat(filepath.Join(q.StateDir, "access-token"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&0777 != 0600 {
		t.Fatal(info.Mode())
	}
}
func TestAzureExplicitDeviceFlowStreamsOnlyInteractiveAction(t *testing.T) {
	q := helperRequest(t, "azure-inspector", map[string]any{"azure": map[string]any{"tenant_id": "tenant-fixture", "subscription_id": "subscription-fixture"}})
	q.Action = "authenticate"
	q.Interactive = true
	f := &helperProcess{FailAccount: true}
	h := Helper{Executor: f}
	result, err := h.Run(context.Background(), "azure-inspector", q)
	if err != nil {
		t.Fatal(err)
	}
	if result.Runtime == nil || result.AuthRequired {
		t.Fatal(result)
	}
	count := 0
	for _, a := range f.Calls {
		if strings.Contains(strings.Join(a, " "), " login ") {
			count++
			if !strings.Contains(strings.Join(a, " "), "--use-device-code --tenant tenant-fixture") {
				t.Fatal(a)
			}
		}
	}
	if count != 1 {
		t.Fatal(f.Calls)
	}
}

func TestDefaultKubeconfigSymlinkRejectedWithoutReadingCredentials(t *testing.T) {
	home := t.TempDir()
	os.Mkdir(filepath.Join(home, ".kube"), 0700)
	defaultFile := filepath.Join(home, ".kube", "config")
	os.WriteFile(defaultFile, []byte("synthetic credential"), 0600)
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(defaultFile, alias); err != nil {
		t.Skip("platform symlink privilege unavailable")
	}
	if !isDefaultKubeconfig(alias, home) {
		t.Fatal("default kubeconfig symlink accepted")
	}
	if isDefaultKubeconfig(filepath.Join(t.TempDir(), "selected"), home) {
		t.Fatal("explicit unrelated path rejected")
	}
}

func TestBadPrepareLeavesExistingConfigAndAssets(t *testing.T) {
	for _, tc := range []struct {
		name string
		fail bool
		auth string
	}{
		{"auth required", false, `{"auth_required":true}`},
		{"auth error", true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := helperRequest(t, "cluster-inspector", map[string]any{"cluster": map[string]any{"api_server": "https://changed.example.test"}})
			old := filepath.Join(q.StateDir, "config")
			os.MkdirAll(old, 0700)
			os.WriteFile(filepath.Join(old, "target.toml"), []byte("old target"), 0600)
			os.WriteFile(filepath.Join(old, "ca.pem"), []byte("old certificate"), 0600)
			h := Helper{Executor: &helperProcess{FailAuth: tc.fail, AuthJSON: tc.auth}}
			h.Run(context.Background(), "cluster-inspector", q)
			cfg, _ := os.ReadFile(filepath.Join(old, "target.toml"))
			cert, _ := os.ReadFile(filepath.Join(old, "ca.pem"))
			if string(cfg) != "old target" || string(cert) != "old certificate" {
				t.Fatalf("failed prepare replaced runtime resources: %s %s", cfg, cert)
			}
			scratch, _ := filepath.Glob(filepath.Join(q.StateDir, ".config-stage-*"))
			if len(scratch) != 0 {
				t.Fatal(scratch)
			}
		})
	}
}
func TestExplicitEmptyOptionalFieldClearsPreviousSourceValue(t *testing.T) {
	q := helperRequest(t, "grafana-inspector", map[string]any{"grafana": map[string]any{"url": "https://grafana.example.test", "auth_mode": "api_token", "datasource_uid": "old-loki"}})
	q.Inputs = map[string]any{"datasource_uid": ""}
	h := Helper{Executor: &helperProcess{AuthJSON: `{"auth_required":false}`}}
	result, err := h.Run(context.Background(), "grafana-inspector", q)
	if err != nil || result.Runtime == nil {
		t.Fatalf("%v %+v", err, result)
	}
	data, err := os.ReadFile(filepath.Join(q.StateDir, "config", "target.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "datasource_uid") || strings.Contains(string(data), "old-loki") {
		t.Fatal(string(data))
	}
}

type fragmentedDiagnostics struct{}

func (fragmentedDiagnostics) Run(_ context.Context, _ []string, _ string, _ []byte, _ map[string]string, callback func([]byte)) ([]byte, error) {
	callback([]byte("token=frag"))
	callback([]byte("mented-secret\n"))
	return nil, errors.New("failure fragmented-secret")
}
func TestHelperDiagnosticFragmentsAndReturnedErrorRedacted(t *testing.T) {
	var output bytes.Buffer
	h := Helper{Executor: fragmentedDiagnostics{}, OnStderr: func(p []byte) { output.Write(p) }}
	_, err := h.command(context.Background(), []string{"run"}, nil, nil, []string{"fragmented-secret"})
	if strings.Contains(output.String(), "fragmented-secret") || strings.Contains(err.Error(), "fragmented-secret") {
		t.Fatalf("secret leaked: %s %v", output.String(), err)
	}
}

func TestRuntimeDigestChangesForEditedConfigAtStableMount(t *testing.T) {
	q := helperRequest(t, "cluster-inspector", map[string]any{"cluster": map[string]any{"api_server": "https://old.example.test"}})
	h := Helper{Executor: &helperProcess{AuthJSON: `{"auth_required":false}`}}
	first, err := h.Run(context.Background(), "cluster-inspector", q)
	if err != nil {
		t.Fatal(err)
	}
	q.Inputs = map[string]any{"api_server": "https://edited.example.test"}
	second, err := h.Run(context.Background(), "cluster-inspector", q)
	if err != nil {
		t.Fatal(err)
	}
	if first.Runtime.Mounts[0].Source != second.Runtime.Mounts[0].Source {
		t.Fatal("expected stable config mount")
	}
	a, b := first.Runtime.Env["AACT_CONFIG_DIGEST"], second.Runtime.Env["AACT_CONFIG_DIGEST"]
	if a == "" || b == "" || a == b {
		t.Fatalf("edited config digest unchanged: %q %q", a, b)
	}
}

func TestRuntimeUsesExactBuiltImageID(t *testing.T) {
	q := helperRequest(t, "cluster-inspector", map[string]any{"cluster": map[string]any{"api_server": "https://cluster.example.test"}})
	id := "sha256:" + strings.Repeat("a", 64)
	h := Helper{Executor: &helperProcess{AuthJSON: `{"auth_required":false}`, BuildOutput: id + "\n"}}
	result, err := h.Run(context.Background(), "cluster-inspector", q)
	if err != nil {
		t.Fatal(err)
	}
	if result.Runtime.Image != id {
		t.Fatalf("built image identity lost: %q", result.Runtime.Image)
	}
}
