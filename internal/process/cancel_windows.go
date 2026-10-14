//go:build windows

package process

import (
	"os"
	"os/exec"
	"strconv"
)

func ShutdownSignals() []os.Signal { return []os.Signal{os.Interrupt} }

func configureCancellation(cmd *exec.Cmd) {
	configureChildProcessTree(cmd)
	cmd.Cancel = func() error {
		return signalChildProcessTree(cmd, true)
	}
}

func configureChildProcessTree(cmd *exec.Cmd) {}

func signalChildProcessTree(cmd *exec.Cmd, force bool) error {
	if cmd == nil || cmd.Process == nil || cmd.ProcessState != nil {
		return os.ErrProcessDone
	}
	args := []string{"/T"}
	if force {
		args = append(args, "/F")
	}
	args = append(args, "/PID", strconv.Itoa(cmd.Process.Pid))
	// taskkill /T targets descendants as well as the wrapper process. Keep the
	// error if it cannot run; falling back to Process.Kill would orphan children.
	return exec.Command("taskkill", args...).Run()
}
