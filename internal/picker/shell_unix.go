//go:build !windows

package picker

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

func discoverPickerShell() (shellSpec, error) {
	if explicit := os.Getenv("AACT_PICKER_SHELL"); explicit != "" {
		return shellFromPath(explicit)
	}
	pid := os.Getpid()
	for depth := 0; depth < 12; depth++ {
		parent, name, err := unixParentProcess(pid)
		if err != nil || parent <= 1 {
			break
		}
		if spec, ok := shellForName(name); ok {
			return spec, nil
		}
		pid = parent
	}
	if fallback := os.Getenv("SHELL"); fallback != "" {
		if spec, err := shellFromPath(fallback); err == nil {
			return spec, nil
		}
	}
	return shellSpec{}, fmt.Errorf("could not identify the invoking shell from the process ancestry; set AACT_PICKER_SHELL to an explicit supported shell path")
}

func unixParentProcess(pid int) (int, string, error) {
	command := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "ppid=", "-o", "comm=")
	output, err := command.Output()
	if err != nil {
		return 0, "", err
	}
	fields := strings.Fields(string(output))
	if len(fields) < 2 {
		return 0, "", fmt.Errorf("unexpected process metadata")
	}
	parent, err := strconv.Atoi(fields[0])
	if err != nil {
		return 0, "", err
	}
	return parent, fields[1], nil
}

func shellFromPath(path string) (shellSpec, error) {
	resolved, err := exec.LookPath(path)
	if err != nil {
		return shellSpec{}, fmt.Errorf("explicit picker shell %q is unavailable: %w", path, err)
	}
	name := filepath.Base(resolved)
	if spec, ok := shellForName(name); ok {
		spec.path = resolved
		return spec, nil
	}
	return shellSpec{}, fmt.Errorf("unsupported picker shell %q; set AACT_PICKER_SHELL to a supported shell", path)
}

func shellForName(name string) (shellSpec, bool) {
	base := strings.ToLower(filepath.Base(name))
	switch base {
	case "zsh", "bash", "sh", "dash", "ash", "ksh", "mksh", "bash.exe":
		path, err := exec.LookPath(name)
		return shellSpec{path: path, kind: shellPOSIX}, err == nil
	case "fish":
		path, err := exec.LookPath(name)
		return shellSpec{path: path, kind: shellFish}, err == nil
	case "pwsh", "pwsh.exe", "powershell", "powershell.exe":
		path, err := exec.LookPath(name)
		return shellSpec{path: path, kind: shellPowerShell}, err == nil
	case "cmd", "cmd.exe":
		path, err := exec.LookPath(name)
		return shellSpec{path: path, kind: shellCMD}, err == nil
	default:
		return shellSpec{}, false
	}
}
