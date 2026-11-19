//go:build windows

package integration

import "testing"

func credentialPTYSupported() bool { return false }

func runPublicCLIOnPTY(t *testing.T, binary string, enroll, expectWarning bool) (int, string) {
	t.Helper()
	t.Skip("public terminal interaction requires a Unix PTY")
	return 1, ""
}
