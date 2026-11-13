// Command aact-vault-handoff is built for Linux container architectures and
// invoked by AACT through docker exec with the handoff JSON on stdin.
package main

import (
	"fmt"
	"os"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/vault"
)

func main() {
	if err := vault.ReceiveContainerHandoff(os.Stdin, vault.ContainerHandoffPath); err != nil {
		fmt.Fprintln(os.Stderr, "AACT vault handoff could not be installed")
		os.Exit(1)
	}
}
