package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"path/filepath"
	"strings"
	"testing"
)

func TestMissingArbitraryPackageCommandKeepsDeclaredExecutable(t *testing.T) {
	packageDir := filepath.Join(t.TempDir(), "custom-package")
	executor := &recordingActionExec{output: `{}`}
	pkg := actionPackage()
	pkg.Dir = packageDir
	if _, err := (&ActionRunner{Executor: executor}).Run(context.Background(), pkg, ActionRequest{Action: "prepare"}); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(packageDir, "bin", "helper")
	if len(executor.argv) == 0 || executor.argv[0] != want {
		t.Fatalf("arbitrary action command changed to %#v, want %q", executor.argv, want)
	}
}

type recordingActionExec struct {
	argv   []string
	cwd    string
	output string
}

func (f *recordingActionExec) Run(_ context.Context, argv []string, cwd string, _ []byte, _ map[string]string, _ func([]byte)) ([]byte, error) {
	f.argv = append([]string(nil), argv...)
	f.cwd = cwd
	return []byte(f.output), nil
}

type actionExec struct {
	request ActionRequest
	output  string
	calls   int
	chunks  []string
}

func (f *actionExec) Run(_ context.Context, _ []string, _ string, b []byte, _ map[string]string, stderr func([]byte)) ([]byte, error) {
	f.calls++
	json.Unmarshal(b, &f.request)
	if stderr != nil {
		if len(f.chunks) == 0 {
			stderr([]byte("progress private-secret"))
		} else {
			for _, s := range f.chunks {
				stderr([]byte(s))
			}
		}
	}
	return []byte(f.output), nil
}

func TestActionRejectsNullAndRedactsSplitSecret(t *testing.T) {
	f := &actionExec{output: `null`, chunks: []string{"private-", "secret"}}
	var p string
	r := ActionRunner{Executor: f, OnStderr: func(b []byte) { p += string(b) }}
	_, e := r.Run(context.Background(), actionPackage(), ActionRequest{Action: "prepare", Inputs: map[string]any{"token": "private-secret"}})
	if e == nil {
		t.Fatal("null accepted")
	}
	if strings.Contains(p, "private-secret") {
		t.Fatal(p)
	}
}
func actionPackage() catalog.Package {
	return catalog.Package{ID: "p", Dir: "/tmp/pkg", Inputs: []catalog.Input{{Name: "token", Type: "secret"}, {Name: "connections", Type: "string", Multiple: true}}, MCP: &catalog.MCP{Actions: map[string]catalog.Command{"prepare": {Argv: []string{"bin/helper", "prepare"}}, "authenticate": {Argv: []string{"bin/helper", "authenticate"}}}}}
}
func TestActionJSONContract(t *testing.T) {
	b, e := json.Marshal(ActionRequest{ProtocolVersion: 1, Action: "prepare", Target: config.Target{Name: "target", Path: "file"}, PackageDir: "p", StateDir: "s", Inputs: map[string]any{}})
	if e != nil {
		t.Fatal(e)
	}
	for _, v := range []string{`"protocol_version"`, `"package_dir"`, `"state_dir"`, `"name":"target"`} {
		if !strings.Contains(string(b), v) {
			t.Fatal(string(b))
		}
	}
	f := &actionExec{output: `{"auth_required":true}`}
	r := ActionRunner{Executor: f}
	out, e := r.Run(context.Background(), actionPackage(), ActionRequest{Action: "prepare"})
	if e != nil || !out.AuthRequired || f.request.ProtocolVersion != 1 {
		t.Fatalf("%+v %v", out, e)
	}
}
func TestNoninteractiveAuthRequiredDoesNotLogin(t *testing.T) {
	f := &actionExec{output: `{"auth_required":true}`}
	r := ActionRunner{Executor: f}
	_, e := r.Run(context.Background(), actionPackage(), ActionRequest{Action: "prepare"})
	if e != nil || f.calls != 1 {
		t.Fatal(e, f.calls)
	}
	_, e = r.Run(context.Background(), actionPackage(), ActionRequest{Action: "authenticate"})
	if e != nil || f.calls != 2 {
		t.Fatal("declared authentication command was not executed", e)
	}
}
func TestInteractiveAuthIsExplicit(t *testing.T) {
	f := &actionExec{output: `{}`}
	r := ActionRunner{Executor: f}
	_, e := r.Run(context.Background(), actionPackage(), ActionRequest{Action: "authenticate", Interactive: true})
	if e != nil || f.calls != 1 {
		t.Fatal(e)
	}
}
func TestPrepareOptionsOnlyForDeclaredInputs(t *testing.T) {
	f := &actionExec{output: `{"choices":{"surprise":[{"value":"x"}]}}`}
	r := ActionRunner{Executor: f}
	if _, e := r.Run(context.Background(), actionPackage(), ActionRequest{Action: "prepare"}); e == nil {
		t.Fatal("undeclared dynamic question accepted")
	}
	f.output = `{"choices":{"connections":[{"value":"x","label":"X"}]}}`
	if _, e := r.Run(context.Background(), actionPackage(), ActionRequest{Action: "prepare"}); e != nil {
		t.Fatal(e)
	}
}
func TestActionRejectsTrailingOutputAndRedactsProgress(t *testing.T) {
	f := &actionExec{output: `{} {}`}
	var progress string
	r := ActionRunner{Executor: f, OnStderr: func(b []byte) { progress += string(b) }}
	_, e := r.Run(context.Background(), actionPackage(), ActionRequest{Action: "prepare", Inputs: map[string]any{"token": "private-secret"}})
	if e == nil {
		t.Fatal("extra output accepted")
	}
	if strings.Contains(progress, "private-secret") {
		t.Fatal(progress)
	}
}

func TestActionFailureWrapsCauseAndKeepsScrubbedDiagnostic(t *testing.T) {
	var progress string
	r := ActionRunner{Executor: &actionFailureExec{err: context.Canceled, stderr: "progress private-secret"}, OnStderr: func(b []byte) { progress += string(b) }}
	_, err := r.Run(context.Background(), actionPackage(), ActionRequest{Action: "prepare", Inputs: map[string]any{"token": "private-secret"}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("wrapped action error does not preserve cancellation: %v", err)
	}
	want := "p prepare failed: context canceled: progress [redacted]"
	if err.Error() != want {
		t.Fatalf("visible error = %q, want %q", err, want)
	}
	if strings.Contains(progress, "private-secret") {
		t.Fatalf("stderr was not redacted: %q", progress)
	}
}

type actionFailureExec struct {
	err    error
	stderr string
}

func (f *actionFailureExec) Run(_ context.Context, _ []string, _ string, _ []byte, _ map[string]string, stderr func([]byte)) ([]byte, error) {
	if stderr != nil {
		stderr([]byte(f.stderr))
	}
	return nil, f.err
}

func TestActionFailureRetainsDistinctiveCauseAfterLongBuildOutput(t *testing.T) {
	const cause = "compiler rejected configured entrypoint"
	var progress string
	r := ActionRunner{Executor: &chunkedActionFailureExec{err: errors.New("exit status 1"), stderr: strings.Repeat("build output ", 900) + cause}, OnStderr: func(b []byte) { progress += string(b) }}
	_, err := r.Run(context.Background(), actionPackage(), ActionRequest{Action: "prepare"})
	if err == nil || !strings.Contains(err.Error(), cause) {
		t.Fatalf("failure lost distinctive cause after long stderr: %v", err)
	}
	if !strings.Contains(progress, cause) {
		t.Fatalf("streaming output lost distinctive cause: %q", progress[len(progress)-min(100, len(progress)):])
	}
}

type chunkedActionFailureExec struct {
	err    error
	stderr string
}

func (f *chunkedActionFailureExec) Run(_ context.Context, _ []string, _ string, _ []byte, _ map[string]string, stderr func([]byte)) ([]byte, error) {
	if stderr != nil {
		for start := 0; start < len(f.stderr); start += 256 {
			end := min(start+256, len(f.stderr))
			stderr([]byte(f.stderr[start:end]))
		}
	}
	return nil, f.err
}

func TestActionResultDecodesOptionalProviderDiagnostic(t *testing.T) {
	var result ActionResult
	if err := json.Unmarshal([]byte(`{"auth_required":true,"diagnostic":"Kubernetes API rejected credentials (HTTP 401)."}`), &result); err != nil {
		t.Fatal(err)
	}
	if result.Diagnostic != "Kubernetes API rejected credentials (HTTP 401)." {
		t.Fatalf("diagnostic = %q", result.Diagnostic)
	}
	var legacy ActionResult
	if err := json.Unmarshal([]byte(`{"auth_required":true}`), &legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.Diagnostic != "" {
		t.Fatalf("legacy diagnostic = %q", legacy.Diagnostic)
	}
}
