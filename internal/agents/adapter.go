package agents

import (
	"context"
	"errors"
	"fmt"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/process"
	"net/url"
	"strings"
)

type Adapter interface {
	Register(context.Context, Environment, Registration) error
	Unregister(context.Context, Environment, string) error
}

func For(kind string, runner process.Executor) (Adapter, error) {
	if runner == nil {
		runner = process.OSExecutor{}
	}
	switch kind {
	case "codex":
		return cliAdapter{kind: "codex", runner: runner}, nil
	case "copilot", "copilot-cli":
		return cliAdapter{kind: "copilot-cli", runner: runner}, nil
	case "opencode":
		return jsonAdapter{kind: kind, parent: "mcp"}, nil
	case "claude":
		return jsonAdapter{kind: kind, parent: "mcpServers"}, nil
	case "intellij", "intellij-ai-assistant":
		return jsonAdapter{kind: "intellij", parent: "mcpServers"}, nil
	case "copilot-intellij":
		return jsonAdapter{kind: kind, parent: "servers"}, nil
	case "generic", "generic-mcp":
		return jsonAdapter{kind: kind, parent: "servers"}, nil
	default:
		return nil, fmt.Errorf("unsupported MCP agent kind %q", kind)
	}
}
func IsManual(kind string) bool { return kind == "generic" || kind == "generic-mcp" }
func validate(r Registration) error {
	if r.Name == "" || strings.HasPrefix(r.Name, "-") || strings.ContainsAny(r.Name, "\r\n\x00") {
		return errors.New("invalid MCP registration name")
	}
	u, err := url.Parse(r.URL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("MCP registration requires an HTTP(S) URL")
	}
	if r.TimeoutMS < 0 {
		return errors.New("MCP timeout must not be negative")
	}
	return nil
}
