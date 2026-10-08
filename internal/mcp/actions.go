package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/process"
	"io"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type ActionRequest struct {
	ProtocolVersion int            `json:"protocol_version"`
	Action          string         `json:"action"`
	Target          config.Target  `json:"target,omitzero"`
	Profile         config.Profile `json:"profile"`
	Inputs          map[string]any `json:"inputs"`
	PackageDir      string         `json:"package_dir"`
	StateDir        string         `json:"state_dir"`
	Interactive     bool           `json:"interactive"`
}
type ActionResult struct {
	AuthRequired bool                        `json:"auth_required"`
	Choices      map[string][]catalog.Choice `json:"choices,omitempty"`
	Runtime      *RunSpec                    `json:"runtime,omitempty"`
}
type ActionRunner struct {
	Executor process.Executor
	GOOS     string
	OnStderr func([]byte)
}

type actionFailure struct {
	message string
	cause   error
}

func (e actionFailure) Error() string { return e.message }
func (e actionFailure) Unwrap() error { return e.cause }

func RunAction(ctx context.Context, p catalog.Package, q ActionRequest) (ActionResult, error) {
	return (&ActionRunner{Executor: process.OSExecutor{}}).Run(ctx, p, q)
}
func (r *ActionRunner) Run(ctx context.Context, p catalog.Package, q ActionRequest) (ActionResult, error) {
	if p.MCP == nil {
		return ActionResult{}, errors.New("package has no MCP")
	}
	cmd, ok := p.MCP.Actions[q.Action]
	if !ok {
		return ActionResult{}, fmt.Errorf("package %s has no %s action", p.ID, q.Action)
	}
	goos := r.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	if goos == "windows" && cmd.Windows != nil {
		cmd = *cmd.Windows
	}
	argv := append([]string{}, cmd.Argv...)
	if len(argv) == 0 {
		return ActionResult{}, errors.New("empty action command")
	}
	workingDir := p.Dir
	if strings.ContainsAny(argv[0], "/\\") && !filepath.IsAbs(argv[0]) {
		executable := filepath.Join(p.Dir, argv[0])
		argv[0] = executable
	}
	q.ProtocolVersion = 1
	q.PackageDir = p.Dir
	b, e := json.Marshal(q)
	if e != nil {
		return ActionResult{}, e
	}
	seconds := cmd.TimeoutSeconds
	if seconds == 0 {
		seconds = 300
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(seconds)*time.Second)
	defer cancel()
	executor := r.Executor
	if executor == nil {
		executor = process.OSExecutor{}
	}
	secrets := []string{}
	for _, d := range p.Inputs {
		if d.Type == "secret" {
			if s, ok := q.Inputs[d.Name].(string); ok && s != "" {
				secrets = append(secrets, s)
			}
		}
	}
	scrub := func(s string) string {
		for _, secret := range secrets {
			s = strings.ReplaceAll(s, secret, "[redacted]")
		}
		return s
	}
	var diagnostics []byte
	redactor := process.NewRedactor(secrets, func(b []byte) {
		diagnostics = appendDiagnosticTail(diagnostics, b, 8192)
		if r.OnStderr != nil {
			r.OnStderr(b)
		}
	})
	out, e := executor.Run(ctx, argv, workingDir, b, nil, redactor.Write)
	redactor.Flush()
	if e != nil {
		message := fmt.Sprintf("%s %s failed: %s: %s", p.ID, q.Action, scrub(e.Error()), strings.TrimSpace(string(diagnostics)))
		return ActionResult{}, actionFailure{message: message, cause: e}
	}
	var result ActionResult
	if len(out) > process.MaxStdout {
		return result, process.ErrOutputLimit
	}
	trimmed := bytes.TrimSpace(out)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return result, errors.New("action must return a JSON object")
	}
	d := json.NewDecoder(bytes.NewReader(out))
	d.UseNumber()
	d.DisallowUnknownFields()
	if e = d.Decode(&result); e != nil {
		return result, fmt.Errorf("%s returned invalid action JSON: %w", p.ID, e)
	}
	if e = d.Decode(new(any)); e != io.EOF {
		return result, errors.New("action must output exactly one JSON object")
	}
	declared := map[string]bool{}
	for _, v := range p.Inputs {
		declared[v.Name] = true
	}
	for name := range result.Choices {
		if !declared[name] {
			return ActionResult{}, fmt.Errorf("prepare returned choices for undeclared input %s", name)
		}
	}
	return result, nil
}

func appendDiagnosticTail(previous, chunk []byte, limit int) []byte {
	if limit <= 0 {
		return nil
	}
	if len(chunk) >= limit {
		return append(previous[:0], chunk[len(chunk)-limit:]...)
	}
	if overflow := len(previous) + len(chunk) - limit; overflow > 0 {
		previous = previous[overflow:]
	}
	return append(previous, chunk...)
}
