package browsercontroller

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestApplyDefaultPopupAllowedURLsOnlyWhenUserDidNotSetPopupPolicy(t *testing.T) {
	defaults := []string{"https://app.example"}
	cases := []struct {
		name string
		raw  string
		want map[string]any
	}{
		{
			name: "missing value",
			raw:  "",
			want: map[string]any{"popup_allowed_urls": []any{"https://app.example"}},
		},
		{
			name: "empty object",
			raw:  "{}",
			want: map[string]any{"popup_allowed_urls": []any{"https://app.example"}},
		},
		{
			name: "preserves navigation limits",
			raw:  `{"allowed_urls":["https://login.example"],"tab_limits":{"max_tabs":2,"hard_max_tabs":4}}`,
			want: map[string]any{
				"allowed_urls":       []any{"https://login.example"},
				"popup_allowed_urls": []any{"https://app.example"},
				"tab_limits":         map[string]any{"max_tabs": float64(2), "hard_max_tabs": float64(4)},
			},
		},
		{
			name: "preserves explicit popup policy",
			raw:  `{"popup_allowed_urls":["https://other.example"]}`,
			want: map[string]any{"popup_allowed_urls": []any{"https://other.example"}},
		},
		{
			name: "preserves explicit empty popup policy",
			raw:  `{"popup_allowed_urls":[]}`,
			want: map[string]any{"popup_allowed_urls": []any{}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotJSON, err := ApplyDefaultPopupAllowedURLs(tc.raw, defaults)
			if err != nil {
				t.Fatalf("ApplyDefaultPopupAllowedURLs() error = %v", err)
			}
			var got map[string]any
			if err := json.Unmarshal([]byte(gotJSON), &got); err != nil {
				t.Fatalf("merged policy is invalid JSON: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("merged policy = %#v, want %#v", got, tc.want)
			}
			if err := ValidateAccessConfig(gotJSON); err != nil {
				t.Fatalf("merged policy is invalid: %v", err)
			}
		})
	}
}

func TestApplyDefaultPopupAllowedURLsRejectsInvalidDefaultsAndInput(t *testing.T) {
	for _, tc := range []struct {
		name     string
		raw      string
		defaults []string
	}{
		{name: "invalid default", raw: `{}`, defaults: []string{"https://example.test/*"}},
		{name: "invalid input", raw: `not-json`, defaults: []string{"https://app.example"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ApplyDefaultPopupAllowedURLs(tc.raw, tc.defaults); err == nil {
				t.Fatal("invalid policy was accepted")
			}
		})
	}
}

func TestValidateAccessConfigAcceptsDocumentedGenericPolicy(t *testing.T) {
	config := `{"allowed_urls":["https://example.test:8443/private"],"popup_allowed_urls":["https://login.example.test"],"tab_limits":{"max_tabs":8,"hard_max_tabs":12,"per_pattern":[{"pattern":"https://example.test:8443","max_tabs":4}],"popup_exemptions":[{"pattern":"https://.login.example.test","max_tabs":3,"grace_seconds":10}]}}`
	if err := ValidateAccessConfig(config); err != nil {
		t.Fatalf("valid browser access policy rejected: %v", err)
	}
}

func TestAccessConfigDigestIgnoresWhitespaceAndObjectKeyOrder(t *testing.T) {
	first := `{"allowed_urls":["https://example.test"],"popup_allowed_urls":[]}`
	second := ` { "popup_allowed_urls": [ ], "allowed_urls": [ "https://example.test" ] } `
	if accessConfigDigest(first) != accessConfigDigest(second) {
		t.Fatal("equivalent browser access policy produced different container identity")
	}
}

func TestValidateAccessConfigRejectsMalformedOrUnsupportedPolicyBeforeDocker(t *testing.T) {
	for _, config := range []string{
		`{"allowed_urls":["https://example.test/*"],"unexpected":true}`,
		`{"allowed_urls":["https://example.test/*"]}`,
		`{"allowed_urls":["file:///*"]}`,
		`{"allowed_urls":["https://*.example.test"]}`,
		`{"allowed_urls":["https://example.test:65536"]}`,
		`{"allowed_urls":["https://user@example.test"]}`,
		`{"allowed_urls":["https://example.test/path?query"]}`,
		" \n ",
		`{"allowed_urls":null}`,
		`{"tab_limits":null}`,
		`{"allowed_urls":["https://example.test/*****************"]}`,
		`{"tab_limits":{"max_tabs":8}}`,
		`{"tab_limits":{"max_tabs":4,"hard_max_tabs":3}}`,
		`{"tab_limits":{"max_tabs":4,"hard_max_tabs":5,"popup_exemptions":[{"pattern":"https://login.example.test","max_tabs":2,"grace_seconds":31}]}}`,
		`{"allowed_urls":["https://example.test"]} trailing`,
	} {
		if err := ValidateAccessConfig(config); err == nil {
			t.Fatalf("invalid browser access policy accepted: %s", config)
		}
	}
}
