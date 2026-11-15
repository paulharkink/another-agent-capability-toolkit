package browsercontroller

import (
	"errors"
	"os"
	"path/filepath"
)

func acquireRuntimeLock(path string) (func(), bool, error) {
	if path == "" {
		return nil, false, errors.New("controller runtime lock path is missing")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, false, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, false, err
	}
	locked, err := tryControllerFileLock(file)
	if err != nil || !locked {
		_ = file.Close()
		return nil, locked, err
	}
	return func() {
		unlockControllerFile(file)
		_ = file.Close()
	}, true, nil
}
