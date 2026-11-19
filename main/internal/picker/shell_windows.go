//go:build windows

package picker

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

func discoverPickerShell() (shellSpec, error) {
	if explicit := os.Getenv("AACT_PICKER_SHELL"); explicit != "" {
		return shellFromPath(explicit)
	}
	parent := uint32(syscall.Getppid())
	processes, err := windowsProcesses()
	if err != nil {
		return shellSpec{}, fmt.Errorf("inspect invoking process ancestry: %w", err)
	}
	for depth := 0; depth < 12 && parent > 1; depth++ {
		process, ok := processes[parent]
		if !ok {
			break
		}
		if spec, ok := shellForName(process.name); ok {
			return spec, nil
		}
		parent = process.parent
	}
	return shellSpec{}, fmt.Errorf("could not identify the invoking Windows shell from process ancestry; set AACT_PICKER_SHELL to an explicit supported shell path")
}

func windowsProcesses() (map[uint32]struct {
	name   string
	parent uint32
}, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	if err := windows.Process32First(snapshot, &entry); err != nil {
		return nil, err
	}
	processes := make(map[uint32]struct {
		name   string
		parent uint32
	})
	for {
		processes[entry.ProcessID] = struct {
			name   string
			parent uint32
		}{windows.UTF16ToString(entry.ExeFile[:]), entry.ParentProcessID}
		if err := windows.Process32Next(snapshot, &entry); err != nil {
			if err == syscall.ERROR_NO_MORE_FILES {
				break
			}
			return nil, err
		}
	}
	return processes, nil
}

func shellFromPath(path string) (shellSpec, error) {
	resolved, err := exec.LookPath(path)
	if err != nil {
		return shellSpec{}, fmt.Errorf("explicit picker shell %q is unavailable: %w", path, err)
	}
	spec, ok := shellForName(filepath.Base(resolved))
	if !ok {
		return shellSpec{}, fmt.Errorf("unsupported picker shell %q; set AACT_PICKER_SHELL to a supported shell", path)
	}
	spec.path = resolved
	return spec, nil
}

func shellForName(name string) (shellSpec, bool) {
	base := strings.ToLower(filepath.Base(name))
	switch base {
	case "powershell.exe", "pwsh.exe", "pwsh", "powershell":
		path, err := exec.LookPath(name)
		return shellSpec{path: path, kind: shellPowerShell}, err == nil
	case "cmd.exe", "cmd":
		path, err := exec.LookPath(name)
		return shellSpec{path: path, kind: shellCMD}, err == nil
	case "bash.exe", "bash", "zsh.exe", "zsh", "sh.exe", "sh", "fish.exe", "fish":
		path, err := exec.LookPath(name)
		kind := shellPOSIX
		if strings.HasPrefix(base, "fish") {
			kind = shellFish
		}
		return shellSpec{path: path, kind: kind}, err == nil
	default:
		return shellSpec{}, false
	}
}
