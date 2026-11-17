package integration

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/browsercontroller"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/daemon"
	vault "github.com/paulharkink/another-agent-capability-toolkit/main/internal/vault"
)

type browserRegistration struct {
	Instance    string                          `json:"instance"`
	ContainerID string                          `json:"container_id"`
	Config      browsercontroller.ServiceConfig `json:"config"`
}

func testDaemonControlRequest(info vault.DaemonInfo, token []byte, method, path string, body, result any) error {
	return daemon.ControlRequest(info, token, method, path, body, result)
}

func wipeTestBytes(value []byte) {
	for i := range value {
		value[i] = 0
	}
}

func TestVaultDaemonChildHelper(t *testing.T) {
	if os.Getenv("AACT_TEST_DAEMON_CHILD") != "1" {
		return
	}
	if err := daemon.Run(context.Background(), os.Stdin, daemon.Registry{"vault": vault.RunService}); err != nil {
		os.Exit(3)
	}
	os.Exit(0)
}
