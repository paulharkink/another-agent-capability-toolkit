//go:build windows

package cli

import (
	"os"

	"golang.org/x/sys/windows"
)

func stdinIsPipe() bool {
	kind, err := windows.GetFileType(windows.Handle(os.Stdin.Fd()))
	return err == nil && kind == windows.FILE_TYPE_PIPE
}
