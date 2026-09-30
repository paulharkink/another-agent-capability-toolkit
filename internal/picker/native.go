package picker

import (
	"context"
	"errors"
	"fmt"
	"github.com/ncruces/zenity"
)

// Native uses OS dialogs; Linux may fall back when its optional GUI backend is absent.
func Native(ctx context.Context, kind, initial string) (string, error) {
	options := []zenity.Option{zenity.Context(ctx), zenity.Title("Select " + kind), zenity.Filename(initial)}
	if kind == "directory" {
		options = append(options, zenity.Directory())
	}
	path, e := zenity.SelectFile(options...)
	if errors.Is(e, zenity.ErrCanceled) {
		return "", ErrCancelled
	}
	if e != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("%w: %v", ErrUnavailable, e)
	}
	return path, nil
}
