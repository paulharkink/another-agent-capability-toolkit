package packagehelpers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/mcp"
)

type ActionHandler func(context.Context, string, mcp.ActionRequest) (mcp.ActionResult, error)

// ExecuteAction validates and runs one package helper request from stdin.
func ExecuteAction(ctx context.Context, args []string, in io.Reader, out io.Writer, handler ActionHandler) error {
	if len(args) != 2 || (args[1] != "prepare" && args[1] != "authenticate") {
		return errors.New("usage: inspector-helper <package-id> prepare|authenticate")
	}
	switch args[0] {
	case "cluster-inspector", "grafana-inspector", "azure-inspector":
	default:
		return errors.New("unsupported inspector package")
	}
	reader := io.LimitReader(in, (16<<20)+1)
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	var request mcp.ActionRequest
	if err := decoder.Decode(&request); err != nil {
		return fmt.Errorf("invalid action request: %w", err)
	}
	if request.ProtocolVersion != 1 {
		return errors.New("unsupported action protocol version")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("action stdin must contain exactly one JSON object")
	}
	if request.Action != "" && request.Action != args[1] {
		return errors.New("action command/request mismatch")
	}
	request.Action = args[1]
	result, err := handler(ctx, args[0], request)
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(result)
}
