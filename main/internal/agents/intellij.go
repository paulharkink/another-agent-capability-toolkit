package agents

import (
	"context"
	"strings"
)

// intellijAdapter keeps the existing skill destination while withholding MCP
// registration until JetBrains documents or exposes a supported external
// configuration mechanism.
type intellijAdapter struct{ *skillAdapter }

func (a *intellijAdapter) Features() FeatureSet { return FeatureSet{Skills: true} }

func (a *intellijAdapter) Detect(ctx context.Context, scope Scope) (Detection, error) {
	d, err := a.skillAdapter.Detect(ctx, scope)
	d.ConfigPath = ""
	d.CanCreateConfig = false
	d.MCPDisabledReason = "JetBrains AI Assistant MCP registration is unavailable: no supported external config file path or import mechanism is verified"
	if d.Reason != "" && strings.Contains(d.Reason, "JetBrains AI Assistant MCP definitions") {
		d.Reason = d.MCPDisabledReason
	}
	return d, err
}
