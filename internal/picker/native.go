package picker

import (
	"context"
	"errors"
	"fmt"
	"github.com/ncruces/zenity"
)

// Native uses OS dialogs; Linux may fall back when its optional GUI backend is absent.
func Native(ctx context.Context, kind, initial string) (string, error) {
	path, e := zenity.SelectFile(nativeOptions(ctx, kind, initial)...)
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

func nativeOptions(ctx context.Context, kind, initial string) []zenity.Option {
	options := []zenity.Option{zenity.Context(ctx), zenity.Title("Select " + kind), zenity.Filename(initial), zenity.ShowHidden()}
	if kind == "directory" {
		options = append(options, zenity.Directory())
	}
	return options
}
