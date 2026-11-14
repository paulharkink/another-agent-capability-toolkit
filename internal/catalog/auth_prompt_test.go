package catalog

import (
	"path/filepath"
	"testing"
)

func TestGrafanaDeclaresOnlyConditionalMinimalAuthPromptInputs(t *testing.T) {
	packageInfo, err := Load(filepath.Join("..", "..", "packages", "grafana-inspector"))
	if err != nil {
		t.Fatal(err)
	}
	inputs := make(map[string]Input, len(packageInfo.Inputs))
	for _, input := range packageInfo.Inputs {
		inputs[input.Name] = input
	}
	for _, name := range []string{"token", "grafana_session"} {
		input := inputs[name]
		if !input.AuthRequired || len(input.VisibleWhen) != 1 {
			t.Errorf("%s should be an auth-required, conditionally visible input: %+v", name, input)
		}
	}
	if input := inputs["session_expiry"]; !input.AuthPrompt || input.AuthRequired || len(input.VisibleWhen) != 1 {
		t.Errorf("session_expiry should be an optional, conditionally prompted auth field: %+v", input)
	}
	if inputs["oauth_refresh"].AuthRequired || inputs["oauth_refresh"].AuthPrompt {
		t.Errorf("optional OAuth refresh must not be included in the minimal prompt by default: %+v", inputs["oauth_refresh"])
	}
}
