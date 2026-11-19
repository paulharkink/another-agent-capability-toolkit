//go:build windows

package integration

import "os/exec"

func detachTestProcessFromControllingTerminal(*exec.Cmd) {}
