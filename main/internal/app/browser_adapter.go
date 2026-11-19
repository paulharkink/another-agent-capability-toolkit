package app

import (
	"errors"
	"io"
	"os"
	"path/filepath"
)

func readBrowserAdapter(directory, name string) (string, error) {
	if name == "" {
		return "", nil
	}
	if filepath.IsAbs(name) {
		return "", errors.New("browser adapter path must be relative to the capability")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return "", errors.New("cannot open capability directory")
	}
	defer root.Close()
	f, err := root.Open(name)
	if err != nil {
		return "", errors.New("cannot open browser adapter inside capability")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("browser adapter must be a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, 64*1024+1))
	if err != nil || len(data) == 0 || len(data) > 64*1024 {
		return "", errors.New("browser adapter must be nonempty and at most 64 KiB")
	}
	return string(data), nil
}
