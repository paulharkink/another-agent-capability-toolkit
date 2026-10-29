//go:build windows

package install

import (
	"errors"
	"fmt"
	"golang.org/x/sys/windows"
)

func readLXReparseLink(path string) (string, bool, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", false, err
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", false, err
	}
	defer windows.CloseHandle(handle)

	buf := make([]byte, 16*1024)
	var returned uint32
	err = windows.DeviceIoControl(handle, windows.FSCTL_GET_REPARSE_POINT, nil, 0, &buf[0], uint32(len(buf)), &returned, nil)
	if err != nil {
		if errors.Is(err, windows.ERROR_NOT_A_REPARSE_POINT) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("read raw Windows reparse point: %w", err)
	}
	if returned > uint32(len(buf)) {
		return "", false, errors.New("Windows returned an oversized reparse payload")
	}
	return parseLXReparseTarget(buf[:returned])
}
