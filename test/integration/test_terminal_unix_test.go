//go:build darwin || linux

package integration

import (
	"os/exec"
	"syscall"
)

func detachTestProcessFromControllingTerminal(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
