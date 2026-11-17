//go:build windows

package vault

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func lockVaultFile(path string) (func(), error) {
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("open vault lock: %w", err)
	}
	var overlapped windows.Overlapped
	err = windows.LockFileEx(windows.Handle(lock.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlapped)
	if err != nil {
		_ = lock.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, errors.New("another AACT process is using this vault")
		}
		return nil, fmt.Errorf("lock vault: %w", err)
	}
	return func() { _ = windows.UnlockFileEx(windows.Handle(lock.Fd()), 0, 1, 0, &overlapped); _ = lock.Close() }, nil
}
