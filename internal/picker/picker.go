package picker

import (
	"context"
	"errors"
	"fmt"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"io"
	"os"
	"path/filepath"
)

var ErrCancelled = errors.New("selection cancelled")
var ErrUnavailable = errors.New("native picker unavailable")
var ErrNotSubmitted = errors.New("picker has not been submitted")

type NativeDialog func(context.Context, string, string) (string, error)
type Picker struct {
	Native NativeDialog
	Reader io.Reader
	Writer io.Writer
}

var dialogSlot = make(chan struct{}, 1)

// TryNative opens only the operating-system dialog. It never starts a terminal UI.
func TryNative(ctx context.Context, kind, initial string) (string, error) {
	return tryNative(ctx, kind, initial, Native)
}

func tryNative(ctx context.Context, kind, initial string, native NativeDialog) (string, error) {
	if kind != "file" && kind != "directory" {
		return "", fmt.Errorf("unsupported picker kind %q", kind)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	select {
	case dialogSlot <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	defer func() { <-dialogSlot }()
	path, err := native(ctx, kind, initial)
	if errors.Is(err, ErrCancelled) {
		return "", ErrCancelled
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		return "", err
	}
	return selectedPath(path, kind, initial)
}

func Select(ctx context.Context, kind, initial string) (string, error) {
	return (Picker{Native: Native, Reader: os.Stdin, Writer: os.Stdout}).Select(ctx, kind, initial)
}
func (p Picker) Select(ctx context.Context, kind, initial string) (string, error) {
	if kind != "file" && kind != "directory" {
		return "", fmt.Errorf("unsupported picker kind %q", kind)
	}
	if e := ctx.Err(); e != nil {
		return "", e
	}
	if p.Native != nil {
		select {
		case dialogSlot <- struct{}{}:
		case <-ctx.Done():
			return "", ctx.Err()
		}
		path, e := p.Native(ctx, kind, initial)
		<-dialogSlot
		if errors.Is(e, ErrCancelled) {
			return "", ErrCancelled
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if e == nil {
			return selectedPath(path, kind, initial)
		}
		if !errors.Is(e, ErrUnavailable) {
			return "", e
		}
	}
	reader := p.Reader
	if reader == nil {
		reader = os.Stdin
	}
	return Terminal(ctx, kind, initial, reader, p.Writer)
}
func selectedPath(path, kind, base string) (string, error) {
	if path == "" {
		return "", ErrCancelled
	}
	cwd, e := os.Getwd()
	if e != nil {
		return "", e
	}
	if base != "" {
		if info, e := os.Stat(base); e == nil && info.IsDir() {
			cwd = base
		} else {
			cwd = filepath.Dir(base)
		}
	}
	abs, e := config.ResolvePath(path, filepath.Join(cwd, "picker"))
	if e != nil {
		return "", e
	}
	info, e := os.Stat(abs)
	if e != nil {
		return "", e
	}
	if kind == "directory" && !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", abs)
	}
	if kind == "file" && !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a regular file", abs)
	}
	return abs, nil
}
