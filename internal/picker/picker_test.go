package picker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeUnavailableFallsBackToTerminal(t *testing.T) {
	root := t.TempDir()
	p := Picker{Native: func(context.Context, string, string) (string, error) { return "", ErrUnavailable }, Reader: strings.NewReader(".\n")}
	got, e := p.Select(context.Background(), "directory", root)
	if e != nil || got != root {
		t.Fatalf("%s %v", got, e)
	}
}
func TestNativeCancel(t *testing.T) {
	p := Picker{Native: func(context.Context, string, string) (string, error) { return "", ErrCancelled }, Reader: strings.NewReader("should not be consumed\n")}
	if _, e := p.Select(context.Background(), "directory", t.TempDir()); !errors.Is(e, ErrCancelled) {
		t.Fatal(e)
	}
}
func TestPlatformNativePaths(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "one")
	if e := os.Mkdir(path, 0700); e != nil {
		t.Fatal(e)
	}
	p := Picker{Native: func(context.Context, string, string) (string, error) { return filepath.Join(root, ".", "one"), nil }}
	got, e := p.Select(context.Background(), "directory", root)
	if e != nil || got != path {
		t.Fatalf("%s %v", got, e)
	}
}
