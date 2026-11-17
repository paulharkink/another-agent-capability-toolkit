//go:build windows

package integration

import "testing"

func credentialPTYSupported() bool { return false }

func buildPublicCLIBinary(t *testing.T) string {
	t.Helper()
	t.Skip("public terminal interaction requires a Unix PTY")
	return ""
}

func runPublicCLIOnPTY(t *testing.T, binary string, enroll bool) (int, string) {
	t.Helper()
	t.Skip("public terminal interaction requires a Unix PTY")
	return 1, ""
}
