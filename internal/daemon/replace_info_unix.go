//go:build !windows

package daemon

import "os"

func replaceInfoFile(source, destination string) error { return os.Rename(source, destination) }
