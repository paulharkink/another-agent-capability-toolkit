//go:build !windows

package process

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func ShutdownSignals() []os.Signal { return []os.Signal{syscall.SIGTERM, os.Interrupt} }

func configureCancellation(cmd *exec.Cmd) {
	configureChildProcessTree(cmd)
	cmd.Cancel = func() error { return signalChildProcessTree(cmd, true) }
}

func configureChildProcessTree(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func signalChildProcessTree(cmd *exec.Cmd, force bool) error {
	if cmd == nil || cmd.Process == nil || cmd.ProcessState != nil {
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
