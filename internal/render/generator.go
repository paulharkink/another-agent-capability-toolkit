package render

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

type Generator struct {
	Executor process.Executor
	GOOS     string
	OnStderr func([]byte)
}

func Generate(ctx context.Context, p catalog.Package, inputs map[string]any, target config.Target, staging string) (map[string]any, error) {
	return (Generator{}).Generate(ctx, p, inputs, target, staging)
}
func (g Generator) Generate(ctx context.Context, p catalog.Package, inputs map[string]any, target config.Target, staging string) (map[string]any, error) {
	if p.Generator == nil {
		return map[string]any{}, nil
	}
	c := *p.Generator
	goos := g.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	if goos == "windows" && c.Windows != nil {
		c = *c.Windows
	}
	if len(c.Argv) == 0 || c.Argv[0] == "" {
		return nil, errors.New("empty generator command")
	}
	argv := append([]string{}, c.Argv...)
	if !filepath.IsAbs(argv[0]) {
		path, err := contained(p.Dir, argv[0], false)
		if err != nil {
			return nil, err
		}
		argv[0] = path
	}
	seconds := c.TimeoutSeconds
	if seconds == 0 {
		seconds = p.Generator.TimeoutSeconds
	}
	if seconds == 0 {
		seconds = 300
	}
	if seconds < 0 {
		return nil, errors.New("generator timeout must be positive")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(seconds)*time.Second)
	defer cancel()
	request := map[string]any{"protocol_version": 1, "inputs": inputs, "context": map[string]any{"target": target, "package_dir": p.Dir, "staging_dir": staging}}
	body, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("encode generator inputs: %w", err)
	}
	runner := g.Executor
	if runner == nil {
		runner = process.OSExecutor{}
	}
	secrets := secretValues(inputs)
	var pending bytes.Buffer
	emit := func(line []byte) {
		if g.OnStderr == nil {
			return
		}
		s := string(line)
		for _, secret := range secrets {
			if secret != "" {
				s = strings.ReplaceAll(s, secret, "[REDACTED]")
			}
		}
		g.OnStderr([]byte(s))
	}
	callback := func(chunk []byte) {
		if g.OnStderr == nil {
			return
		}
		pending.Write(chunk)
		for {
			all := pending.Bytes()
			n := bytes.IndexByte(all, '\n')
			if n < 0 {
				break
			}
			line := append([]byte{}, all[:n+1]...)
			pending.Next(n + 1)
			emit(line)
		}
	}
	output, err := runner.Run(ctx, argv, p.Dir, body, nil, callback)
	if pending.Len() > 0 {
		emit(pending.Bytes())
	}
	if err != nil {
		return nil, err
	}
	if len(output) > process.MaxStdout {
		return nil, process.ErrOutputLimit
	}
	d := json.NewDecoder(bytes.NewReader(output))
	d.UseNumber()
	var result map[string]any
	if err = d.Decode(&result); err != nil {
		return nil, fmt.Errorf("generator returned invalid JSON object: %w", err)
	}
	if result == nil {
		return nil, errors.New("generator must return a JSON object")
	}
	if err = d.Decode(new(any)); err != io.EOF {
		return nil, errors.New("generator must return exactly one JSON object")
	}
	return result, nil
}
func secretValues(inputs map[string]any) []string {
	var out []string
	for k, v := range inputs {
		lower := strings.ToLower(k)
		if nested, ok := v.(map[string]any); ok {
			out = append(out, secretValues(nested)...)
		}
		if strings.Contains(lower, "secret") || strings.Contains(lower, "token") || strings.Contains(lower, "password") || strings.Contains(lower, "credential") {
			if s, ok := v.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}
