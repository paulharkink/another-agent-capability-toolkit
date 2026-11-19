//go:build !windows

package main

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func wrapperShutdownSignals() []os.Signal { return []os.Signal{syscall.SIGTERM, os.Interrupt} }

func configureWrapperChildProcessTree(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func signalWrapperChildProcessTree(cmd *exec.Cmd, force bool) error {
	if cmd == nil || cmd.Process == nil {
		return os.ErrProcessDone
	}
	signal := syscall.SIGTERM
	if force {
		signal = syscall.SIGKILL
	}
	err := syscall.Kill(-cmd.Process.Pid, signal)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	return err
}
