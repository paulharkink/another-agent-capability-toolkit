//go:build !windows

package state

func authDirPrivacyDiagnosticForTest(path string) (string, error) {
	return authDirPrivacyState(path)
}
