package tui

import (
	"reflect"
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

func TestMinimalGroupProfileAuthInputsPromptsMissingAndFailedSavedCredentialsOnly(t *testing.T) {
	sections := []catalog.Section{
		{ID: "connection", Title: "Connection", Fields: []string{"url", "required_connection"}},
		{ID: "authentication", Title: "Authentication", Fields: []string{"auth_mode", "token", "session", "expiry", "oauth_refresh", "refresh_name", "saved_token", "not_editable"}},
	}
	inputs := []viewmodel.SetupInput{
		{Definition: catalog.Input{Name: "url", Required: true}, Editable: true},
		{Definition: catalog.Input{Name: "required_connection", Required: true}, Editable: true},
		{Definition: catalog.Input{Name: "auth_mode", Required: true}, Value: "api_token", HasValue: true, Editable: true},
		{Definition: catalog.Input{Name: "token", Type: "secret", AuthRequired: true, VisibleWhen: map[string]any{"auth_mode": "api_token"}}, Editable: true},
		{Definition: catalog.Input{Name: "session", Type: "secret", AuthRequired: true, VisibleWhen: map[string]any{"auth_mode": "session_cookie"}}, Editable: true},
		{Definition: catalog.Input{Name: "expiry", Type: "secret", AuthPrompt: true, VisibleWhen: map[string]any{"auth_mode": "session_cookie"}}, Editable: true},
		{Definition: catalog.Input{Name: "oauth_refresh", Type: "secret", VisibleWhen: map[string]any{"auth_mode": "session_cookie"}}, Editable: true},
		{Definition: catalog.Input{Name: "refresh_name", VisibleWhen: map[string]any{"auth_mode": "session_cookie"}}, Editable: true},
		{Definition: catalog.Input{Name: "saved_token", Type: "secret", AuthRequired: true}, SecretSaved: true, Editable: true},
		{Definition: catalog.Input{Name: "not_editable", AuthRequired: true}, Editable: false},
	}

	got := minimalGroupProfileAuthInputs(sections, inputs, nil)
	var names []string
	for _, input := range got {
		names = append(names, input.Definition.Name)
	}
	if want := []string{"token", "saved_token"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("API token mode prompt fields = %v, want %v", names, want)
	}

	inputs[2].Value = "session_cookie"
	got = minimalGroupProfileAuthInputs(sections, inputs, nil)
	names = names[:0]
	for _, input := range got {
		names = append(names, input.Definition.Name)
	}
	if want := []string{"session", "expiry", "saved_token"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("session-cookie mode prompt fields = %v, want %v", names, want)
	}

	inputs[6].Definition.Required = true
	got = minimalGroupProfileAuthInputs(sections, inputs, nil)
	names = names[:0]
	for _, input := range got {
		names = append(names, input.Definition.Name)
	}
	if want := []string{"session", "expiry", "oauth_refresh", "saved_token"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("explicitly required OAuth refresh prompt fields = %v, want %v", names, want)
	}
}
