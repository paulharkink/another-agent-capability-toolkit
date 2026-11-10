package catalog

import (
	"path/filepath"
	"testing"
)

func TestAtlassianWrapperPackageDeclaresLocalOAuthAndLegacyAuthModes(t *testing.T) {
	pkg, err := Load(filepath.Join("..", "..", "packages", "atlassian-mcp"))
	if err != nil {
		t.Fatal(err)
	}
	if pkg.ID != "atlassian-mcp" {
		t.Fatalf("package ID = %q, want atlassian-mcp", pkg.ID)
	}
	inputs := map[string]Input{}
	for _, input := range pkg.Inputs {
		inputs[input.Name] = input
	}
	mode, ok := inputs["auth_method"]
	if !ok || mode.Type != "choice" || mode.Default != "oauth" {
		t.Fatalf("auth_method = %#v, want OAuth as the default mode", mode)
	}
	choices := map[string]bool{}
	for _, choice := range mode.Options {
		choices[choice.Value] = true
	}
	for _, expected := range []string{"oauth", "service_account", "personal"} {
		if !choices[expected] {
			t.Errorf("auth_method is missing %q option", expected)
		}
	}
	if inputs["api_token"].Type != "secret" {
		t.Errorf("api_token input = %#v, want secret input", inputs["api_token"])
	}
	if inputs["email"].VisibleWhen["auth_method"] != "personal" {
		t.Errorf("email visible_when = %#v, want personal mode", inputs["email"].VisibleWhen)
	}
	if inputs["client_metadata_url"].Default != "https://paulharkink.github.io/another-agent-capability-toolkit/oauth/client.json" {
		t.Errorf("client metadata URL default = %#v", inputs["client_metadata_url"].Default)
	}

	if pkg.MCP == nil {
		t.Fatal("Atlassian MCP declaration is missing")
	}
	mcp := pkg.MCP
	if mcp.Runtime != "docker" || mcp.ExternalURL != "" {
		t.Fatalf("MCP runtime/external_url = %q/%q, want local Docker wrapper", mcp.Runtime, mcp.ExternalURL)
	}
	if mcp.EndpointPath != "/mcp" || mcp.HealthPath != "/health" || mcp.ContainerPort != 8765 {
		t.Errorf("MCP endpoint/health/port = %q/%q/%d", mcp.EndpointPath, mcp.HealthPath, mcp.ContainerPort)
	}
	if mcp.AuthenticateOnApply == nil || *mcp.AuthenticateOnApply {
		t.Errorf("authenticate_on_apply = %#v, want explicit false for Atlassian OAuth", mcp.AuthenticateOnApply)
	}
	if got := mcp.Env["AACT_ATLASSIAN_AUTH_MODE"]; got != "${ inputs.auth_method }" {
		t.Errorf("auth mode env = %q", got)
	}
	if got := mcp.SecretEnvInputs["AACT_ATLASSIAN_API_TOKEN"]; got != "api_token" {
		t.Errorf("API token secret env = %q", got)
	}
	if got := mcp.EnvInputs["AACT_ATLASSIAN_EMAIL"]; got != "email" {
		t.Errorf("personal email env = %q", got)
	}
	if got := mcp.Env["AACT_ATLASSIAN_LOCAL_PORT"]; got != "8765" {
		t.Errorf("container listen port env = %q", got)
	}
	if got := mcp.Env["AACT_ATLASSIAN_CALLBACK_PORT"]; got != "${ tostring(inputs.local_port) }" {
		t.Errorf("published host callback port env = %q", got)
	}
	if len(mcp.CredentialFiles) != 1 || mcp.CredentialFiles[0] != "oauth-state.json" {
		t.Errorf("persistent OAuth credential files = %#v", mcp.CredentialFiles)
	}
	action, ok := mcp.Actions["authenticate"]
	if !ok || action.Target != "container" || len(action.Argv) < 3 || action.Argv[0] != "python" || action.Argv[1] != "/app/server.py" {
		t.Errorf("authenticate action = %#v, want container-owned server action", action)
	}
	if mcp.ReleaseImage == "" || mcp.BuildContext != "./mcp" {
		t.Errorf("release/build configuration = %q/%q", mcp.ReleaseImage, mcp.BuildContext)
	}
	resolved, err := ResolveExpressions(pkg, map[string]any{
		"auth_method":         "oauth",
		"local_port":          23456,
		"client_metadata_url": "https://example.invalid/client.json",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := resolved.MCP.Env["AACT_ATLASSIAN_LOCAL_PORT"]; got != "8765" {
		t.Errorf("resolved container listen port = %q, want fixed 8765", got)
	}
	if got := resolved.MCP.Env["AACT_ATLASSIAN_CALLBACK_PORT"]; got != "23456" {
		t.Errorf("resolved callback port = %q, want published host port", got)
	}
}
