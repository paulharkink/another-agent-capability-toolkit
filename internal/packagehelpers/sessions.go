package packagehelpers

import (
	"context"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/sessionsearch"
)

func RunSessionSearch(ctx context.Context, args []string, home string, interactive bool) error {
	return sessionsearch.Run(ctx, args, home, interactive)
}
