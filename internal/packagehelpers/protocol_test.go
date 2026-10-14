package packagehelpers

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/mcp"
)

func TestExecuteActionPreservesProfilePackInLegacyTargetContext(t *testing.T) {
	profile := config.Profile{
		Ref:         config.ProfileRef{PackID: "dso-skills", CapabilityID: "grafana-inspector", Name: "ap"},
		Path:        "/packs/dso-skills/profiles/grafana-inspector/ap.toml",
		Raw:         map[string]any{"grafana": map[string]any{"auth_mode": "session_cookie"}},
		InputPolicy: map[string]string{"auth_mode": "default"},
	}

	tests := []struct {
		name              string
		targetEnvironment string
		wantEnvironment   string
	}{
		{name: "preserves projected pack id", targetEnvironment: "dso-skills", wantEnvironment: "dso-skills"},
		{name: "preserves existing target environment", targetEnvironment: "explicit-pack", wantEnvironment: "explicit-pack"},
		{name: "fills missing environment from profile pack", wantEnvironment: "dso-skills"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			request := mcp.ActionRequest{
				ProtocolVersion: 1,
				Action:          "authenticate",
				Instance:        profile,
				Target: config.Target{
					Environment: tc.targetEnvironment,
					Name:        "old-target",
					Path:        "/old/target.toml",
					Raw:         map[string]any{"old": true},
					InputPolicy: map[string]string{"old": "fixed"},
				},
			}
			payload, err := json.Marshal(request)
			if err != nil {
				t.Fatal(err)
			}

			var received mcp.ActionRequest
			var output bytes.Buffer
			err = ExecuteAction(context.Background(), []string{"grafana-inspector", "authenticate"}, bytes.NewReader(payload), &output,
				func(_ context.Context, _ string, got mcp.ActionRequest) (mcp.ActionResult, error) {
					received = got
					return mcp.ActionResult{}, nil
				})
			if err != nil {
				t.Fatal(err)
			}

			if received.Target.Environment != tc.wantEnvironment {
				t.Fatalf("target environment = %q, want %q", received.Target.Environment, tc.wantEnvironment)
			}
			if received.Target.Name != "ap" || received.Target.Path != profile.Path {
				t.Fatalf("target identity = %q at %q, want profile ap at %q", received.Target.Name, received.Target.Path, profile.Path)
			}
			if received.Target.Raw["grafana"] == nil || received.Target.InputPolicy["auth_mode"] != "default" {
				t.Fatalf("profile values or policy lost in target context: %+v", received.Target)
			}
			if received.Action != "authenticate" {
				t.Fatalf("action = %q, want authenticate", received.Action)
			}
		})
	}
}
