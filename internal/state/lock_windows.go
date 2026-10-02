//go:build windows

package state

import (
	"errors"
	"golang.org/x/sys/windows"
	"os"
)

func tryLock(f *os.File) (bool, error) {
	e := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{})
	if errors.Is(e, windows.ERROR_LOCK_VIOLATION) {
		return false, nil
	}
	return e == nil, e
}
func unlock(f *os.File) {
	_ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &windows.Overlapped{})
}
