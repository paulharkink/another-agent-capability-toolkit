//go:build windows

package process

import (
	"context"
	"os/exec"
	"syscall"
)

// RunWindowsCommandLine runs one Windows program with a caller-supplied raw
// command tail. The executable is quoted with Windows argument rules; the tail
// is passed verbatim for command interpreters such as cmd.exe.
func RunWindowsCommandLine(ctx context.Context, executable, commandTail, cwd string, stdin []byte, env map[string]string, onStderr func([]byte)) ([]byte, error) {
	cmd := exec.CommandContext(ctx, executable)
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: syscall.EscapeArg(executable) + " " + commandTail}
	return runCommand(ctx, cmd, cwd, stdin, env, onStderr)
}
