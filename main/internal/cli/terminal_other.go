//go:build !windows

package cli

func stdinIsPipe() bool { return false }
