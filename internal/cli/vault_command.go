package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/vault"
	"golang.org/x/term"
)

func runVaultCommand(args []string, out, errOut io.Writer) int {
	if len(args) != 1 || (args[0] != "status" && args[0] != "stop") {
		fmt.Fprintln(errOut, "usage: aact vault status|stop")
		return 2
	}
	root := state.DefaultRoot(runtime.GOOS, homeDirectory(), os.Getenv)
	if args[0] == "stop" {
		info, err := vault.ReadDaemonInfo(root)
		if os.IsNotExist(err) {
			fmt.Fprintln(out, "Vault daemon is not running")
			return 0
		}
		if err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			fmt.Fprintln(errOut, "stopping the vault daemon requires an interactive terminal to unlock the vault")
			return 2
		}
		fmt.Fprint(errOut, "Vault master password: ")
		password, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(errOut)
		if err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
		defer clear(password)
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
		fmt.Fprintln(out, "Vault daemon stopped")
		return 0
	}
	info, err := vault.ReadDaemonInfo(root)
	if err != nil {
		fmt.Fprintln(errOut, "Vault daemon is not running")
		return 1
	}
	if !vault.DaemonRunning(info) {
		fmt.Fprintln(errOut, "Vault daemon is not running")
		return 1
	}
	fmt.Fprintf(out, "Vault daemon running at %s\n", info.APIAddress)
	return 0
}

func homeDirectory() string { home, _ := os.UserHomeDir(); return home }
