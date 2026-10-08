package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
)

type diagnosticRedactionExec struct{ secret string }

func (e diagnosticRedactionExec) Run(_ context.Context, _ []string, _ string, _ []byte, _ map[string]string, stderr func([]byte)) ([]byte, error) {
	if stderr != nil {
		stderr([]byte("secondary progress contains " + e.secret))
	}
	result, err := json.Marshal(ActionResult{AuthRequired: true, Diagnostic: "Kubernetes API rejected credential " + e.secret + " (HTTP 401)."})
	return result, err
}

func TestActionResultRedactsSecretFromProviderDiagnosticAndStderr(t *testing.T) {
	const secret = "fixture-credential-secret"
	pkg := catalog.Package{
		ID: "fixture", Dir: t.TempDir(),
		Inputs: []catalog.Input{{Name: "token", Type: "secret"}},
		MCP:    &catalog.MCP{Actions: map[string]catalog.Command{"authenticate": {Argv: []string{"fixture-auth"}}}},
	}
	var progress string
	result, err := (&ActionRunner{Executor: diagnosticRedactionExec{secret: secret}, OnStderr: func(output []byte) { progress += string(output) }}).Run(context.Background(), pkg, ActionRequest{Action: "authenticate", Inputs: map[string]any{"token": secret}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result.Diagnostic, secret) || strings.Contains(progress, secret) {
		t.Fatal("declared secret escaped redaction")
	}
	if !result.AuthRequired || !strings.Contains(result.Diagnostic, "Kubernetes API rejected credential") || !strings.Contains(result.Diagnostic, "HTTP 401") {
		t.Fatalf("redaction lost the provider's actionable authentication cause: %+v", result)
	}
	if !strings.Contains(result.Diagnostic, "[redacted]") {
		t.Fatalf("diagnostic did not use the existing redaction marker: %q", result.Diagnostic)
	}
}
