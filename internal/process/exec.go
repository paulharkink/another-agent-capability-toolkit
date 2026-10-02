package process

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const MaxStdout = 16 << 20

var ErrOutputLimit = errors.New("command stdout exceeds 16 MiB limit")

type Executor interface {
	Run(context.Context, []string, string, []byte, map[string]string, func([]byte)) ([]byte, error)
}
type OSExecutor struct{}

func (OSExecutor) Run(ctx context.Context, argv []string, cwd string, stdin []byte, env map[string]string, onStderr func([]byte)) ([]byte, error) {
	return Run(ctx, argv, cwd, stdin, env, onStderr)
}

type boundedOutput struct {
	buffer   bytes.Buffer
	exceeded bool
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	if b.buffer.Len()+len(p) > MaxStdout {
		b.exceeded = true
		return 0, ErrOutputLimit
	}
	return b.buffer.Write(p)
}

type callbackWriter struct{ callback func([]byte) }

func (w callbackWriter) Write(p []byte) (int, error) {
	if w.callback != nil {
		w.callback(p)
	}
	return len(p), nil
}

// Run executes argv directly, retaining process-native path and argument semantics.
// Only stderr is streamed; stdin and environment values are never logged here.
func Run(ctx context.Context, argv []string, cwd string, stdin []byte, env map[string]string, onStderr func([]byte)) ([]byte, error) {
	if len(argv) == 0 || argv[0] == "" {
		return nil, errors.New("command argv is empty")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = cwd
	cmd.Stdin = bytes.NewReader(stdin)
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.WaitDelay = 2 * time.Second
	configureCancellation(cmd)
	var out boundedOutput
	cmd.Stdout = &out
	cmd.Stderr = callbackWriter{onStderr}
	err := cmd.Run()
	if out.exceeded {
		return nil, ErrOutputLimit
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, fmt.Errorf("command %s failed: %w", filepath.Base(argv[0]), err)
	}
	return out.buffer.Bytes(), nil
}

var _ io.Writer = (*boundedOutput)(nil)
