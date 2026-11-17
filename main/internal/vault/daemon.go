package vault

import (
	"context"
	"net"
	"net/http"

	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/daemon"
)

// Daemon and StartDaemon remain compatibility aliases for the shared AACT
// daemon HTTP server used by the vault API.
type Daemon = daemon.Server

func StartDaemon(ctx context.Context, listener net.Listener, handler http.Handler) (*Daemon, error) {
	return daemon.StartServer(ctx, listener, handler)
}
