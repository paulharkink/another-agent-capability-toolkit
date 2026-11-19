package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/process"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/releasecatalog"
)

func runInstallerLifecycle(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) < 2 {
		fmt.Fprintln(errOut, "usage: aact __installer-lifecycle --run SCRIPT [--install-dir ROOT] -- INSTALLER-ARGS...")
		return 2
	}
	script := args[0]
	rootOverride := ""
	separator := 1
	for separator < len(args) && args[separator] != "--" {
		if separator+1 >= len(args) {
			fmt.Fprintf(errOut, "%s needs a path\n", args[separator])
			return 2
		}
		switch args[separator] {
		case "--install-dir":
			rootOverride = args[separator+1]
		default:
			fmt.Fprintf(errOut, "unknown lifecycle option: %s\n", args[separator])
			return 2
		}
		separator += 2
	}
	if separator >= len(args) || args[separator] != "--" {
		fmt.Fprintln(errOut, "usage: aact __installer-lifecycle --run SCRIPT [--install-dir ROOT] -- INSTALLER-ARGS...")
		return 2
	}
	installerArgs := args[separator+1:]
	version, err := installerVersion(installerArgs)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 2
	}
	if _, ok := daemonLifecycleVersionBase(version); !ok {
		fmt.Fprintln(errOut, "installer lifecycle requires a stable release or pipeline snapshot version")
		return 2
	}
	err = installWithDaemonLifecycleAtRoot(ctx, rootOverride, version, errOut, func() error {
		var command *exec.Cmd
		if runtime.GOOS == "windows" {
			powershell := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
			command = exec.CommandContext(ctx, powershell, append([]string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-File", script}, installerArgs...)...)
		} else {
			command = exec.CommandContext(ctx, "/bin/sh", append([]string{script}, installerArgs...)...)
		}
		command.Env = installerLifecycleEnvironment()
		command.Stdout = out
		command.Stderr = errOut
		if err := command.Run(); err != nil {
			return fmt.Errorf("run AACT installer: %w", err)
		}
		return nil
	})
	if err != nil {
		fmt.Fprintln(errOut, err)
		if errors.Is(err, errDaemonCredentialRequiresTerminal) {
			return 2
		}
		return 1
	}
	if targetDaemonKind(version) == daemonTargetNone {
		fmt.Fprintln(out, "The target release has no daemon; Vault-dependent running MCPs remain disconnected until a daemon-capable release is installed.")
	} else if targetDaemonKind(version) == daemonTargetLegacy || version == "0.6.0" || version == "0.6.1" {
		fmt.Fprintln(out, "This historical release has no browser API; browser-backed MCPs may not work.")
	}
	return 0
}

var pipelineSnapshotVersion = regexp.MustCompile(`^((0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*))-SNAPSHOT-[a-z0-9]+(-[a-z0-9]+)*$`)

func daemonLifecycleVersionBase(version string) (string, bool) {
	if releasecatalog.StableVersion("v" + version) {
		return version, true
	}
	match := pipelineSnapshotVersion.FindStringSubmatch(version)
	if len(match) == 0 {
		return "", false
	}
	return match[1], true
}

func installerVersion(args []string) (string, error) {
	version := strings.TrimPrefix(os.Getenv("AACT_VERSION"), "v")
	archive := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-v", "--version", "-Version":
			if i+1 == len(args) {
				return "", fmt.Errorf("%s needs a value", args[i])
			}
			version = strings.TrimPrefix(args[i+1], "v")
			i++
		case "--local-archive", "-LocalArchive":
			if i+1 == len(args) {
				return "", fmt.Errorf("%s needs a path", args[i])
			}
			archive = args[i+1]
			i++
		}
	}
	if version == "" && archive != "" {
		target, err := installerTarget()
		if err != nil {
			return "", err
		}
		extension := ".tar.gz"
		if runtime.GOOS == "windows" {
			extension = ".zip"
		}
		name := filepath.Base(archive)
		suffix := "_" + target + extension
		if !strings.HasPrefix(name, "aact_") || !strings.HasSuffix(name, suffix) {
			return "", errors.New("local archive name does not identify this platform and architecture")
		}
		version = strings.TrimPrefix(strings.TrimSuffix(name, suffix), "aact_")
	}
	if version == "" {
		return "", errors.New("installer lifecycle could not determine the release version")
	}
	return version, nil
}

func installerLifecycleEnvironment() []string {
	filtered := process.EnvironmentWithoutVaultPassword(os.Environ())
	environment := make([]string, 0, len(filtered)+1)
	for _, entry := range filtered {
		name, _, _ := strings.Cut(entry, "=")
		if strings.EqualFold(name, "AACT_INSTALLER_LIFECYCLE") {
			continue
		}
		environment = append(environment, entry)
	}
	return append(environment, "AACT_INSTALLER_LIFECYCLE=1")
}
