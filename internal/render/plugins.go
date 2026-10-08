package render

import (
	"context"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"os"
	"path/filepath"
)

func StagePlugin(ctx context.Context, pkg catalog.Package, plugin catalog.Plugin, parent string) (string, error) {
	source := plugin.Source
	if filepath.IsAbs(source) {
		relative, err := filepath.Rel(pkg.Dir, source)
		if err != nil {
			return "", err
		}
		source = relative
	}
	var err error
	source, err = contained(pkg.Dir, source, true)
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := os.MkdirAll(parent, 0700); err != nil {
		return "", err
	}
	stage, err := os.MkdirTemp(parent, ".aact-plugin-*")
	if err != nil {
		return "", err
	}
	if err := CopyTree(ctx, source, stage); err != nil {
		os.RemoveAll(stage)
		return "", err
	}
	return stage, nil
}
