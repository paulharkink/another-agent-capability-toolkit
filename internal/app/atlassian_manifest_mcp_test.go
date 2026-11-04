package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
)

func TestManifestAtlassianEndpointAndTokenModesRegisterWithoutExternalURLOverride(t *testing.T) {
	const endpoint = "https://mcp.atlassian.com/v2/mcp"
	tests := []struct {
		name         string
		mode         string
		email        string
		omitToken    bool
		externalURL  string
		wantEndpoint string
		wantAuth     string
		wantError    string
	}{
		{
			name:         "service account bearer",
			mode:         "service_account",
			wantEndpoint: endpoint,
			wantAuth:     "Bearer fixture-token",
		},
		{
			name:         "personal basic",
			mode:         "personal",
			email:        "dev@example.invalid",
			wantEndpoint: endpoint,
			wantAuth:     "Basic ZGV2QGV4YW1wbGUuaW52YWxpZDpmaXh0dXJlLXRva2Vu",
		},
		{
			name:         "explicit url overrides manifest",
			mode:         "service_account",
			externalURL:  "https://override.example.invalid/mcp",
			wantEndpoint: "https://override.example.invalid/mcp",
			wantAuth:     "Bearer fixture-token",
		},
		{
			name:      "personal mode requires email",
			mode:      "personal",
			wantError: "input email is required",
		},
		{
			name:      "service account requires token",
			mode:      "service_account",
			omitToken: true,
			wantError: "input api_token is required",
		},
		{
			name:      "unsupported mode fails clearly",
			mode:      "custom",
			wantError: "value \"custom\" is not an available choice",
		},
		{
			name:        "explicit url cannot embed credentials",
			mode:        "service_account",
			externalURL: "https://user:password@override.example.invalid/mcp",
			wantError:   "MCP endpoint must not contain embedded credentials",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, adapter, request := profileApplyFixture(t)
			pkgDir := s.Source.Catalog[0].Dir
			manifest := `schema_version = 1
id = "demo"
name = "Demo"

[[inputs]]
name = "auth_method"
type = "choice"
required = true
default = "service_account"
options = [{ value = "service_account", label = "Service account" }, { value = "personal", label = "Personal API token" }]

[[inputs]]
name = "api_token"
type = "secret"
required = true

[[inputs]]
name = "email"
type = "string"
required = true
visible_when = { auth_method = "personal" }

[mcp]
name = "atlassian"
transport = "streamable-http"
external_url = ` + `"` + endpoint + `"` + `
token_input = "api_token"
token_header = "Authorization"
token_prefix = "Bearer "
token_auth_mode_input = "auth_method"
token_username_input = "email"
`
			if err := os.WriteFile(filepath.Join(pkgDir, "package.toml"), []byte(manifest), 0600); err != nil {
				t.Fatal(err)
			}
			pkg, err := catalog.Load(pkgDir)
			if err != nil {
				t.Fatal(err)
			}
			s.Source.Catalog[0] = pkg
			request.ItemIDs = nil
			request.Inputs = map[string]any{"auth_method": tt.mode}
			if !tt.omitToken {
				request.Inputs["api_token"] = "fixture-token"
			}
			if tt.email != "" {
				request.Inputs["email"] = tt.email
			}
			request.ExternalURLs = nil
			if tt.externalURL != "" {
				request.ExternalURLs = map[string]string{"atlassian": tt.externalURL}
			}
			profilePath := filepath.Join(s.Source.ProfileRoot, "demo", "ota.toml")
			if err := os.WriteFile(profilePath, []byte("[inputs]\n"), 0600); err != nil {
				t.Fatal(err)
			}
			runtime := &providerTestRuntime{url: "http://127.0.0.1:8765/mcp"}
			s.Options.Runtime = runtime

			_, applyErr := s.ApplyProfile(context.Background(), request)
			if tt.wantError != "" {
				if applyErr == nil || !strings.Contains(applyErr.Error(), tt.wantError) {
					t.Fatalf("ApplyProfile error = %v, want error containing %q", applyErr, tt.wantError)
				}
				if strings.Contains(applyErr.Error(), "fixture-token") {
					t.Fatal("ApplyProfile error exposed the API token")
				}
				if len(adapter.registrations) != 0 {
					t.Fatal("invalid authentication inputs were registered")
				}
				return
			}
			if applyErr != nil {
				t.Fatal(applyErr)
			}
			if len(runtime.keys) != 0 {
				t.Fatal("manifest-declared remote MCP unexpectedly started a local runtime")
			}
			if len(adapter.registrations) != 1 {
				t.Fatalf("registered MCP count = %d, want 1", len(adapter.registrations))
			}
			registration := adapter.registrations[0]
			if registration.URL != tt.wantEndpoint {
				t.Fatal("registration did not use the manifest endpoint or explicit override")
			}
			if registration.Headers["Authorization"] != tt.wantAuth {
				t.Fatal("registration did not generate the selected Authorization scheme")
			}
		})
	}
}
