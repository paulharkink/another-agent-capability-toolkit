package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

func TestUXGrafanaSessionCookieInputsAllInAuthenticationAndIrrelevantCredentialsConditional(t *testing.T) {
	m := NewContext(t.Context(), &setupBackendFixture{})
	m.catalog = []catalog.Package{{ID: "grafana-inspector", MCP: &catalog.MCP{}}}
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 38})
	preview := viewmodel.SetupPreview{
		Key:         state.Key{Source: "team", Package: "grafana-inspector", Environment: "home", Target: "production"},
		PackageName: "Grafana Inspector",
		Inputs: []viewmodel.SetupInput{
			{Definition: catalog.Input{Name: "auth_mode", Label: "Auth mode", Type: "choice", Options: []catalog.Choice{{Value: "api_token", Label: "API token"}, {Value: "session_cookie", Label: "Session cookie"}}}, Value: "session_cookie", HasValue: true, Editable: true},
			{Definition: catalog.Input{Name: "token", Label: "Token", Type: "secret"}, Editable: true},
			{Definition: catalog.Input{Name: "grafana_session", Label: "Grafana session", Type: "secret"}, Value: "session-fixture", HasValue: true, Editable: true},
			{Definition: catalog.Input{Name: "session_expiry", Label: "Session expiry", Type: "string"}, Value: "expiry-fixture", HasValue: true, Editable: true},
			{Definition: catalog.Input{Name: "oauth_refresh", Label: "OAuth refresh", Type: "secret"}, Value: "refresh-fixture", HasValue: true, Editable: true},
			{Definition: catalog.Input{Name: "refresh_cookie_name", Label: "Refresh cookie name", Type: "string"}, Value: "refresh_cookie", HasValue: true, Editable: true},
			{Definition: catalog.Input{Name: "vault_addr", Label: "Vault address", Type: "string"}, Value: "https://vault.example.test", HasValue: true, Editable: true},
			{Definition: catalog.Input{Name: "vault_path", Label: "Vault path", Type: "string"}, Value: "secret/grafana", HasValue: true, Editable: true},
			{Definition: catalog.Input{Name: "vault_key", Label: "Vault key", Type: "string"}, Value: "token", HasValue: true, Editable: true},
		},
	}
	m.openSetupForm(preview)

	m.form.SelectSection("Authentication")
	view := ansi.Strip(m.View().Content)
	for _, label := range []string{"Grafana session", "Session expiry", "OAuth refresh", "Refresh cookie name"} {
		if !strings.Contains(view, label) {
			t.Errorf("session-cookie Authentication is missing %q:\n%s", label, view)
		}
	}
	if strings.Contains(view, "Token:") {
		t.Errorf("API-token-only control should be inactive for session-cookie mode:\n%s", view)
	}

	apiTokenPreview := preview
	apiTokenPreview.Inputs = append([]viewmodel.SetupInput(nil), preview.Inputs...)
	for i := range apiTokenPreview.Inputs {
		if apiTokenPreview.Inputs[i].Definition.Name == "auth_mode" {
			apiTokenPreview.Inputs[i].Value = "api_token"
		}
	}
	api := NewContext(t.Context(), &setupBackendFixture{})
	api.Update(tea.WindowSizeMsg{Width: 160, Height: 38})
	api.catalog = m.catalog
	api.openSetupForm(apiTokenPreview)
	api.form.SelectSection("Authentication")
	apiView := ansi.Strip(api.View().Content)
	for _, label := range []string{"Token", "Vault address", "Vault path", "Vault key"} {
		if !strings.Contains(apiView, label) {
			t.Errorf("API-token Authentication is missing %q:\n%s", label, apiView)
		}
	}
	if strings.Contains(apiView, "Grafana session") || strings.Contains(apiView, "Session expiry") || strings.Contains(apiView, "Refresh cookie name") {
		t.Errorf("session-cookie controls should be inactive for API-token mode:\n%s", apiView)
	}
}

func TestUXAzureAndForgejoTaskSections(t *testing.T) {
	cases := []struct {
		name, packageID string
		inputs          []viewmodel.SetupInput
		sectionFields   map[string][]string
	}{
		{
			name:      "Azure",
			packageID: "azure-inspector",
			inputs: []viewmodel.SetupInput{
				{Definition: catalog.Input{Name: "url", Label: "Azure URL", Type: "string"}, Editable: true},
				{Definition: catalog.Input{Name: "tenant_id", Label: "Tenant ID", Type: "string"}, Editable: true},
				{Definition: catalog.Input{Name: "subscription_id", Label: "Subscription ID", Type: "string"}, Editable: true},
				{Definition: catalog.Input{Name: "token", Label: "Access token", Type: "secret"}, Editable: true},
			},
			sectionFields: map[string][]string{"Connection": {"Azure URL"}, "Azure": {"Tenant ID", "Subscription ID"}, "Authentication": {"Access token"}},
		},
		{
			name:      "Forgejo",
			packageID: "forgejo-inspector",
			inputs: []viewmodel.SetupInput{
				{Definition: catalog.Input{Name: "url", Label: "Forgejo URL", Type: "string"}, Editable: true},
				{Definition: catalog.Input{Name: "token", Label: "Access token", Type: "secret"}, Editable: true},
			},
			sectionFields: map[string][]string{"Connection": {"Forgejo URL"}, "Authentication": {"Access token"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := NewContext(t.Context(), &setupBackendFixture{})
			m.Update(tea.WindowSizeMsg{Width: 140, Height: 32})
			m.openSetupForm(viewmodel.SetupPreview{Key: state.Key{Package: tc.packageID}, Inputs: tc.inputs})
			for title, labels := range tc.sectionFields {
				m.form.SelectSection(title)
				view := ansi.Strip(m.View().Content)
				if !strings.Contains(view, "> "+title) {
					t.Errorf("%s task section %q missing from navigation:\n%s", tc.name, title, view)
				}
				for _, label := range labels {
					if !strings.Contains(view, label) {
						t.Errorf("%s field %q missing from its %s section:\n%s", tc.name, label, title, view)
					}
				}
			}
		})
	}
}
