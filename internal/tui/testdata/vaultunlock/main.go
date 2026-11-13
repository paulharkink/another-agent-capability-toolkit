package main

import (
	"context"
	"os"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/tui"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/vault"
)

func main() {
	if len(os.Args) < 3 {
		os.Exit(2)
	}
	path, mode := os.Args[1], os.Args[2]
	if mode == "seed" {
		if _, err := vault.Open(path, []byte("existing correct password")); err != nil {
			os.Exit(3)
		}
		return
	}
	if mode == "check" {
		if len(os.Args) < 4 {
			os.Exit(2)
		}
		if _, err := vault.Open(path, []byte(os.Args[3])); err != nil {
			os.Exit(5)
		}
		return
	}
	creating := mode == "create"
	err := tui.RunVaultUnlock(context.Background(), os.Stdin, os.Stdout, creating, func(password []byte) error {
		_, err := vault.Open(path, password)
		return err
	})
	if err == tui.ErrVaultUnlockCanceled {
		return
	}
	if err != nil {
		os.Exit(4)
	}
}
