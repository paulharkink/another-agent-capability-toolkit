package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

// This black-box contract deliberately uses only RegistrationRequest fields
// that existed before Task 9. It can therefore run unchanged on a pre-Task 9
// source archive to demonstrate that the old UI boundary accepted generic MCP
// choices instead of enforcing named-agent-only policy.
func TestRegistrationPolicyBaselineRejectsNonNamedDestinations(t *testing.T) {
	isolateUXUserHome(t, t.TempDir())
	svc, _, _ := fixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05"}}`))
	}))
	defer server.Close()
	key := state.Key{Source: svc.Source.ID, Package: "demo", Target: "default"}
	for _, agentID := range []string{"all", "generic", "generic:work", "generic-mcp:work"} {
		t.Run(agentID, func(t *testing.T) {
			_, err := configureUIRegistrationsTest(context.Background(), svc, viewmodel.RegistrationRequest{
				Key: key, URL: server.URL, Transport: "streamable-http", AgentIDs: []string{agentID},
			})
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), "named") {
				t.Fatalf("UI boundary did not reject non-named destination %q with named-only policy error: %v", agentID, err)
			}
		})
	}
}
