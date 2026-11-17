//go:build windows

package vault

import "golang.org/x/sys/windows"

func replaceFile(from, to string) error {
	from16, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	to16, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(from16, to16, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
