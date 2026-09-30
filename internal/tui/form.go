package tui

import (
	"context"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/forms"
)

// Edit is the interactive callback passed to app.Options.Editor by the CLI.
func Edit(ctx context.Context, defs []catalog.Input, prefill map[string]any) (map[string]any, error) {
	return forms.RunEditor(ctx, defs, prefill)
}
