//go:build windows

package main

import (
	"os"
	"os/exec"
	"strconv"
)

func wrapperShutdownSignals() []os.Signal { return []os.Signal{os.Interrupt} }

func configureWrapperChildProcessTree(cmd *exec.Cmd) {}

func signalWrapperChildProcessTree(cmd *exec.Cmd, force bool) error {
	if cmd == nil || cmd.Process == nil {
		return os.ErrProcessDone
	}
	args := []string{"/T"}
	if force {
		args = append(args, "/F")
	}
	args = append(args, "/PID", strconv.Itoa(cmd.Process.Pid))
	return exec.Command("taskkill", args...).Run()
}
