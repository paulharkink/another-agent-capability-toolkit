package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/vault"
	"golang.org/x/term"
)

var errDaemonCredentialRequiresTerminal = errors.New("interactive terminal required to unlock the vault")

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
		unlockManager, lockErr := vault.LockDaemonManager(root)
		if lockErr != nil {
			fmt.Fprintln(errOut, "lock AACT daemon lifecycle:", lockErr)
			return 1
		}
		defer unlockManager()
		info, err := vault.ReadDaemonInfo(root)
		if os.IsNotExist(err) {
			fmt.Fprintln(out, name+" is not running")
			return 0
		}
		if err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
		password, controlToken, manualUnlock, err := daemonControlCredentials(root, errOut, "stopping the AACT daemon")
		if err != nil {
			fmt.Fprintln(errOut, err)
			if errors.Is(err, errDaemonCredentialRequiresTerminal) {
				return 2
			}
			return 1
		}
		defer clear(password)
		defer clear(controlToken)
		if manualUnlock && term.IsTerminal(int(os.Stdin.Fd())) && confirmVaultCredentialSave(os.Stdin, errOut) {
			if err := setVaultCredential(root, password); err != nil {
				fmt.Fprintln(errOut, "Could not save the vault password to the OS credential store; the daemon remains available for this session.")
			}
		}
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

func daemonControlCredentials(root string, errOut io.Writer, action string) (password, controlToken []byte, manualUnlock bool, err error) {
	password, configured, err := configuredVaultPassword()
	if err != nil {
		return nil, nil, false, err
	}
	manualUnlock = !configured
	fromCredentialStore := false
	if !configured {
		if saved := savedVaultCredential(root); len(saved) > 0 {
			password = saved
			configured = true
			manualUnlock = false
			fromCredentialStore = true
		}
	}
	if configured {
		controlToken, err = vault.ControlTokenForVault(filepath.Join(root, "credentials.kdbx"), password)
		if err != nil && fromCredentialStore {
			clear(password)
			clear(controlToken)
			password = nil
			controlToken = nil
			configured = false
			manualUnlock = true
		} else if err != nil {
			clear(password)
			clear(controlToken)
			return nil, nil, false, err
		}
	}
	var terminal *os.File
	terminalFD := int(os.Stdin.Fd())
	if !configured && !term.IsTerminal(terminalFD) {
		name := "/dev/tty"
		if runtime.GOOS == "windows" {
			name = "CONIN$"
		}
		terminal, err = os.OpenFile(name, os.O_RDWR, 0)
		if err != nil || !term.IsTerminal(int(terminal.Fd())) {
			if terminal != nil {
				_ = terminal.Close()
			}
			clear(password)
			return nil, nil, false, fmt.Errorf("%w: %s requires an interactive terminal to unlock the vault", errDaemonCredentialRequiresTerminal, action)
		}
		defer terminal.Close()
		terminalFD = int(terminal.Fd())
	}
	if !configured {
		prompt := io.Writer(errOut)
		if terminal != nil {
			prompt = terminal
		}
		if _, err := fmt.Fprint(prompt, "Vault master password: "); err != nil {
			clear(password)
			clear(controlToken)
			if terminal != nil {
				return nil, nil, false, fmt.Errorf("%w: %s requires an interactive terminal to unlock the vault", errDaemonCredentialRequiresTerminal, action)
			}
			return nil, nil, false, err
		}
		password, err = term.ReadPassword(terminalFD)
		if terminal != nil {
			_, _ = fmt.Fprintln(terminal)
		} else {
			_, _ = fmt.Fprintln(errOut)
		}
		if err != nil {
			clear(password)
			clear(controlToken)
			return nil, nil, false, err
		}
	}
	if len(password) == 0 {
		clear(password)
		clear(controlToken)
		return nil, nil, false, errors.New("vault master password must not be empty")
	}
	if !configured {
		controlToken, err = vault.ControlTokenForVault(filepath.Join(root, "credentials.kdbx"), password)
		if err != nil {
			clear(password)
			clear(controlToken)
			return nil, nil, false, err
		}
	}
	return password, controlToken, manualUnlock, nil
}

func homeDirectory() string { home, _ := os.UserHomeDir(); return home }
