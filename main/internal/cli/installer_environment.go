package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func withWindowsPowerShellModulePath(environment []string) []string {
	if runtime.GOOS != "windows" {
		return environment
	}
	modulePath := ""
	clean := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		name, value, _ := strings.Cut(entry, "=")
		if strings.EqualFold(name, "PSModulePath") {
			modulePath = value
			continue
		}
		clean = append(clean, entry)
	}
	if systemRoot := os.Getenv("SystemRoot"); systemRoot != "" {
		// AACT launches Windows PowerShell 5.1 even when its parent uses PowerShell 7.
		windowsPowerShellModules := filepath.Join(systemRoot, "System32", "WindowsPowerShell", "v1.0", "Modules")
		if modulePath != "" {
			windowsPowerShellModules += string(os.PathListSeparator) + modulePath
		}
		modulePath = windowsPowerShellModules
	}
	if modulePath != "" {
		clean = append(clean, "PSModulePath="+modulePath)
	}
	return clean
}
