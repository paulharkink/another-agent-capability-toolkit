package integration_test

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

func normalizeCRLF(data []byte) []byte {
	return bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
}

func TestNormalizeCRLFForHandlerComparison(t *testing.T) {
	got := normalizeCRLF([]byte("package main\r\nfunc handler() {}\n"))
	want := []byte("package main\nfunc handler() {}\n")
	if !bytes.Equal(got, want) {
		t.Fatalf("normalized source = %q, want %q", got, want)
	}
}

func TestGitProviderImageHandlersMatchSharedHealthProxy(t *testing.T) {
	root := filepath.Join("..", "..")
	shared, err := os.ReadFile(filepath.Join(root, "packages", "git-provider", "mcp", "healthproxy", "handler.go"))
	if err != nil {
		t.Fatal(err)
	}
	shared = normalizeCRLF(shared)
	packageDeclaration := regexp.MustCompile(`(?m)^package [a-zA-Z0-9_]+$`)
	shared = packageDeclaration.ReplaceAll(shared, []byte("package main"))
	for _, profile := range []string{"github", "gitlab", "bitbucket"} {
		profile := profile
		t.Run(profile, func(t *testing.T) {
			got, err := os.ReadFile(filepath.Join(root, "packages", "git-provider", "mcp", profile, "handler.go"))
			if err != nil {
				t.Fatal(err)
			}
			got = normalizeCRLF(got)
			if !bytes.Equal(got, shared) {
				t.Errorf("%s image handler differs from shared healthproxy after package normalization", profile)
			}
		})
	}
}
