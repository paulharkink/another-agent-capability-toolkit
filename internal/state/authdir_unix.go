//go:build !windows

package state

import (
	"fmt"
	"os"
)

func secureAuthDir(path string) error { return os.Chmod(path, 0700) }

func authDirPrivacyState(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%04o", info.Mode().Perm()), nil
}

func authDirIsPrivate(path string) (bool, error) {
	state, err := authDirPrivacyState(path)
	return state == "0700", err
}
