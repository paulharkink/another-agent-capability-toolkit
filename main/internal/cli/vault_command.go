package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/vault"
	"golang.org/x/term"
)

func runDaemonCommand(args []string, legacyVault bool, out, errOut io.Writer) int {
	if len(args) != 1 || (args[0] != "status" && args[0] != "stop") {
		if legacyVault {
			fmt.Fprintln(errOut, "usage: aact vault status|stop")
		} else {
			fmt.Fprintln(errOut, "usage: aact daemon status|stop")
		}
		return 2
	}
	name := "AACT daemon"
	if legacyVault {
		name = "Vault daemon"
	}
	root := state.DefaultRoot(runtime.GOOS, homeDirectory(), os.Getenv)
	if args[0] == "stop" {
		info, err := vault.ReadDaemonInfo(root)
		if os.IsNotExist(err) {
			fmt.Fprintln(out, name+" is not running")
			return 0
		}
		if err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
		password, configured, err := configuredVaultPassword()
		if err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
		if !configured && !term.IsTerminal(int(os.Stdin.Fd())) {
			fmt.Fprintln(errOut, "stopping the AACT daemon requires an interactive terminal to unlock the vault")
			return 2
		}
		if !configured {
			fmt.Fprint(errOut, "Vault master password: ")
			password, err = term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Fprintln(errOut)
			if err != nil {
				fmt.Fprintln(errOut, err)
				return 1
			}
		}
		defer clear(password)
		if len(password) == 0 {
			fmt.Fprintln(errOut, "vault master password must not be empty")
			return 1
		}
		controlToken, err := vault.ControlTokenForVault(filepath.Join(root, "credentials.kdbx"), password)
		if err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
		defer clear(controlToken)
		if err := vault.StopDetached(info, controlToken); err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
		fmt.Fprintln(out, name+" stopped")
		return 0
	}
	info, err := vault.ReadDaemonInfo(root)
	if err != nil {
		fmt.Fprintln(errOut, name+" is not running")
		return 1
	}
	if !vault.DaemonRunning(info) {
		fmt.Fprintln(errOut, name+" is not running")
		return 1
	}
	fmt.Fprintf(out, "%s running at %s\n", name, info.APIAddress)
	return 0
}

func homeDirectory() string { home, _ := os.UserHomeDir(); return home }
