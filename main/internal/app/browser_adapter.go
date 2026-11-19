package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/state"
)

func (s *Service) browserAuthenticationAttention(ctx context.Context, key state.Key, cause error) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	starter, ok := s.Options.Runtime.(interface {
		StartBrowser(context.Context, state.Key) (string, error)
	})
	if !ok {
		return "", fmt.Errorf("browser authentication needs attention, but the viewer is unavailable: %w", cause)
	}
	viewer, err := starter.StartBrowser(ctx, key)
	if err != nil {
		return "", fmt.Errorf("browser authentication needs attention, but the viewer could not be opened: %w", err)
	}
	if viewer == "" {
		return "", fmt.Errorf("browser authentication needs attention, but the viewer URL is unavailable: %w", cause)
	}
	return viewer, fmt.Errorf("browser authentication needs attention: %w", cause)
}

func readBrowserAdapter(directory, name string) (string, error) {
	if name == "" {
		return "", nil
	}
	if filepath.IsAbs(name) {
		return "", errors.New("browser adapter path must be relative to the capability")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return "", errors.New("cannot open capability directory")
	}
	defer root.Close()
	f, err := root.Open(name)
	if err != nil {
		return "", errors.New("cannot open browser adapter inside capability")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("browser adapter must be a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, 64*1024+1))
	if err != nil || len(data) == 0 || len(data) > 64*1024 {
		return "", errors.New("browser adapter must be nonempty and at most 64 KiB")
	}
	return string(data), nil
}
