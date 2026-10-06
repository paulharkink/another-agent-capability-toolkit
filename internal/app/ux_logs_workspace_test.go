package app

import (
	"context"
	"strings"
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/mcp"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
)

func TestUXNoContainerLogsExplainsNextStepWithoutCallingDockerLogs(t *testing.T) {
	svc, _, _ := fixture(t)
	key := state.Key{Source: "fixture", Package: "demo", Target: "production"}
	runtime := &profileRuntime{}
	svc.Options.Runtime = runtime
	_, err := svc.UIProfileLogs(context.Background(), key)
	if err == nil || !strings.Contains(err.Error(), "No MCP container has been created for this target") {
		t.Fatalf("missing explanatory no-container error: %v", err)
	}
	if runtime.logsCalls != 0 {
		t.Fatalf("runtime logs called without a running container: %d calls", runtime.logsCalls)
	}
}

func TestUXProfileLogsRejectsForeignOwnershipBeforeReadingLogs(t *testing.T) {
	svc, _, _ := fixture(t)
	key := state.Key{Source: "fixture", Package: "demo", Target: "production"}
	runtime := &profileRuntime{instances: []mcp.Instance{{Key: key, Status: "running", Ownership: "unknown"}}}
	svc.Options.Runtime = runtime
	_, err := svc.UIProfileLogs(context.Background(), key)
	if err == nil || !strings.Contains(err.Error(), "ownership is unknown") || runtime.logsCalls != 0 {
		t.Fatalf("foreign/unknown logs were not blocked: err=%v calls=%d", err, runtime.logsCalls)
	}
}
