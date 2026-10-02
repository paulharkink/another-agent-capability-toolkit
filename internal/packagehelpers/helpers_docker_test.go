//go:build docker_integration

package packagehelpers

import (
	"context"
	"testing"
)

func TestPrepareMissingAuthInActualContainers(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  map[string]any
	}{
		{"cluster-inspector", map[string]any{"cluster": map[string]any{"api_server": "https://unreachable.example.test"}}},
		{"grafana-inspector", map[string]any{"grafana": map[string]any{"url": "https://unreachable.example.test", "auth_mode": "api_token"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := helperRequest(t, tc.name, tc.raw)
			result, err := (&Helper{}).Run(context.Background(), tc.name, q)
			if err != nil {
				t.Fatal(err)
			}
			if !result.AuthRequired || result.Runtime != nil {
				t.Fatal(result)
			}
		})
	}
}
