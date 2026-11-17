//go:build !windows

package process

import (
	"context"
	"errors"
)

// RunWindowsCommandLine is available only to callers selecting a native
// Windows command interpreter.
func RunWindowsCommandLine(context.Context, string, string, string, []byte, map[string]string, func([]byte)) ([]byte, error) {
	return nil, errors.New("raw Windows command lines are unavailable on this platform")
}
