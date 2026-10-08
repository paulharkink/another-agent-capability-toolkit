package packagehelpers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/mcp"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type helperProcess struct {
	Calls         [][]string
	Requests      []mcp.ActionRequest
	FailAccount   bool
	AccountOutput string
	AuthJSON      string
	FailAuth      bool
	BuildOutput   string
	BuildIID      string
	BuildArgs     []string
}

func (f *helperProcess) Run(_ context.Context, argv []string, _ string, stdin []byte, _ map[string]string, onStderr func([]byte)) ([]byte, error) {
	f.Calls = append(f.Calls, append([]string{}, argv...))
	if len(argv) > 1 && argv[1] == "build" {
		f.BuildArgs = append([]string(nil), argv[2:]...)
		iid := f.BuildIID
		if iid == "" {
			iid = "sha256:" + strings.Repeat("b", 64)
		}
		for i, arg := range argv {
			if arg == "--iidfile" && i+1 < len(argv) {
				if err := os.WriteFile(argv[i+1], []byte(iid+"\n"), 0600); err != nil {
					return nil, err
				}
			}
		}
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
		if onStderr != nil && f.AccountOutput != "" {
			onStderr([]byte(f.AccountOutput))
		}
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
	f := &helperProcess{FailAccount: true, AccountOutput: "Please run 'az login' to set up account."}
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

func TestAzureExplicitDeviceFlowStreamsOnlyInteractiveAction(t *testing.T) {
	q := helperRequest(t, "azure-inspector", map[string]any{"azure": map[string]any{"tenant_id": "tenant-fixture", "subscription_id": "subscription-fixture"}})
	q.Action = "authenticate"
	q.Interactive = true
	f := &helperProcess{FailAccount: true, AccountOutput: "Please run 'az login' to set up account."}
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

func TestExplicitDefaultKubeconfigIsMountedReadOnly(t *testing.T) {
	home := t.TempDir()
	defaultFile := filepath.Join(home, ".kube", "config")
	if err := os.MkdirAll(filepath.Dir(defaultFile), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(defaultFile, []byte("synthetic fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	q := helperRequest(t, "cluster-inspector", map[string]any{"cluster": map[string]any{"api_server": "https://changed.example.test"}})
	q.Action = "authenticate"
	q.Inputs = map[string]any{"kubeconfig": defaultFile}
	f := &helperProcess{AuthJSON: `{"auth_required":false}`}
	h := Helper{Executor: f}
	result, err := h.Run(context.Background(), "cluster-inspector", q)
	if err != nil || result.Runtime == nil {
		t.Fatalf("explicit default kubeconfig rejected: %v", err)
	}
	args := strings.Join(f.Calls[len(f.Calls)-1], " ")
	wantMount := "type=bind,src=" + defaultFile + ",dst=/selected-kubeconfig,readonly"
	if !strings.Contains(args, wantMount) || f.Requests[0].Inputs["kubeconfig"] != "/selected-kubeconfig" {
		t.Fatalf("explicit source was not mounted read-only and remapped: %s %+v", args, f.Requests[0].Inputs)
	}
}

func TestMissingKubeconfigDoesNotSelectHostDefaultImplicitly(t *testing.T) {
	home := t.TempDir()
	defaultFile := filepath.Join(home, ".kube", "config")
	if err := os.MkdirAll(filepath.Dir(defaultFile), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(defaultFile, []byte("synthetic fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	q := helperRequest(t, "cluster-inspector", map[string]any{"cluster": map[string]any{"api_server": "https://changed.example.test"}})
	f := &helperProcess{}
	h := Helper{Executor: f}
	_, err := h.Run(context.Background(), "cluster-inspector", q)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Join(f.Calls[len(f.Calls)-1], " ")
	if strings.Contains(args, "/selected-kubeconfig") || f.Requests[0].Inputs["kubeconfig"] != nil {
		t.Fatalf("host default was selected implicitly: %s %+v", args, f.Requests[0].Inputs)
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

type noisyFailureDiagnostics struct{}

func (noisyFailureDiagnostics) Run(_ context.Context, _ []string, _ string, _ []byte, _ map[string]string, callback func([]byte)) ([]byte, error) {
	callback([]byte(strings.Repeat("build progress ", 900)))
	callback([]byte("FINAL_BUILD_FAILURE"))
	return nil, errors.New("docker build failed")
}

func TestHelperDiagnosticRetainsBoundedTailOnFailure(t *testing.T) {
	var streamed bytes.Buffer
	h := Helper{Executor: noisyFailureDiagnostics{}, OnStderr: func(p []byte) { streamed.Write(p) }}
	_, err := h.command(context.Background(), []string{"build"}, nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "FINAL_BUILD_FAILURE") {
		t.Fatalf("build failure tail was omitted: %v", err)
	}
	if !strings.Contains(streamed.String(), "FINAL_BUILD_FAILURE") {
		t.Fatal("streamed progress callback lost the final diagnostic")
	}
	if len(err.Error()) > 9000 {
		t.Fatalf("diagnostic was not bounded: %d bytes", len(err.Error()))
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
	fixture := &helperProcess{AuthJSON: `{"auth_required":false}`, BuildIID: id}
	h := Helper{Executor: fixture}
	result, err := h.Run(context.Background(), "cluster-inspector", q)
	if err != nil {
		t.Fatal(err)
	}
	if result.Runtime.Image != id {
		t.Fatalf("built image identity lost: %q", result.Runtime.Image)
	}
	buildArgs := strings.Join(fixture.BuildArgs, " ")
	if !strings.Contains(buildArgs, "--progress=plain") || !strings.Contains(buildArgs, "--iidfile") || strings.Contains(buildArgs, "--quiet") {
		t.Fatalf("build progress or immutable image identity flag missing: %s", buildArgs)
	}
}

func TestInvalidBuildImageIDIsRejected(t *testing.T) {
	q := helperRequest(t, "cluster-inspector", map[string]any{"cluster": map[string]any{"api_server": "https://cluster.example.test"}})
	fixture := &helperProcess{BuildIID: "tag:mutable"}
	_, err := (&Helper{Executor: fixture}).Run(context.Background(), "cluster-inspector", q)
	if err == nil || !strings.Contains(err.Error(), "valid image identity") {
		t.Fatalf("invalid image identity accepted: %v", err)
	}
}

func TestClusterHelperPreservesProviderDiagnostic(t *testing.T) {
	q := helperRequest(t, "cluster-inspector", map[string]any{"cluster": map[string]any{"api_server": "https://cluster.example.test"}})
	f := &helperProcess{AuthJSON: `{"auth_required":true,"diagnostic":"Kubernetes API rejected credentials (HTTP 401)."}`}
	result, err := (&Helper{Executor: f}).Run(context.Background(), "cluster-inspector", q)
	if err != nil {
		t.Fatal(err)
	}
	if !result.AuthRequired || result.Diagnostic != "Kubernetes API rejected credentials (HTTP 401)." {
		t.Fatalf("provider auth result lost its primary diagnostic: %+v", result)
	}
}

func TestAzureExpectedLoginProbeIsDiagnosticAndNotPrimaryStderr(t *testing.T) {
	q := helperRequest(t, "azure-inspector", map[string]any{"azure": map[string]any{"tenant_id": "tenant-fixture", "subscription_id": "subscription-fixture"}})
	f := &helperProcess{FailAccount: true, AccountOutput: "Please run 'az login' to set up account."}
	var streamed bytes.Buffer
	result, err := (&Helper{Executor: f, OnStderr: func(p []byte) { streamed.Write(p) }}).Run(context.Background(), "azure-inspector", q)
	if err != nil {
		t.Fatal(err)
	}
	if !result.AuthRequired || !strings.Contains(result.Diagnostic, "az login") {
		t.Fatalf("expected actionable login diagnostic, got %+v", result)
	}
	if streamed.Len() != 0 {
		t.Fatalf("expected auth probe output not to appear as primary stderr: %q", streamed.String())
	}
}

func TestAzureUnexpectedAccountProbeFailureRemainsPrimaryError(t *testing.T) {
	q := helperRequest(t, "azure-inspector", map[string]any{"azure": map[string]any{"tenant_id": "tenant-fixture", "subscription_id": "subscription-fixture"}})
	f := &helperProcess{FailAccount: true, AccountOutput: "Azure endpoint is temporarily unreachable."}
	var streamed bytes.Buffer
	result, err := (&Helper{Executor: f, OnStderr: func(p []byte) { streamed.Write(p) }}).Run(context.Background(), "azure-inspector", q)
	if err == nil || result.AuthRequired {
		t.Fatalf("unexpected probe failure was collapsed into auth required: result=%+v err=%v", result, err)
	}
	if !strings.Contains(err.Error(), "cached account missing") || !strings.Contains(err.Error(), "temporarily unreachable") {
		t.Fatalf("primary probe cause was not retained: %v", err)
	}
	if !strings.Contains(streamed.String(), "temporarily unreachable") {
		t.Fatalf("unexpected failure should remain visible: %q", streamed.String())
	}
}
