package render

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Publish swaps a whole directory, restoring the old tree if the second rename
// fails (including Windows sharing violations). Staging must be on the same
// filesystem as destination. The caller owns destination provenance/locking.
func Publish(staging, destination string) error {
	return publishWithRename(staging, destination, os.Rename)
}
func publishWithRename(staging, destination string, rename func(string, string) error) error {
	info, err := os.Lstat(staging)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("staging must be a directory")
	}
	a, _ := filepath.Abs(staging)
	b, _ := filepath.Abs(destination)
	if a == b {
		return errors.New("staging and destination must differ")
	}
	if err = os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return err
	}
	backup, err := os.MkdirTemp(filepath.Dir(destination), ".aact-backup-*")
	if err != nil {
		return err
	}
	os.Remove(backup)
	keepBackup := false
	defer func() {
		if !keepBackup {
			os.RemoveAll(backup)
		}
	}()
	exists := false
	if _, err = os.Lstat(destination); err == nil {
		exists = true
		if err = rename(destination, backup); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err = rename(staging, destination); err != nil {
		if exists {
			if restore := rename(backup, destination); restore != nil {
				keepBackup = true
				return fmt.Errorf("publish failed: %v; restore failed: %w; backup retained at %s", err, restore, backup)
			}
		}
		return err
	}
	return nil
}
