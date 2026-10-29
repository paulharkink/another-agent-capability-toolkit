//go:build !windows

package install

func readLXReparseLink(string) (string, bool, error) { return "", false, nil }
