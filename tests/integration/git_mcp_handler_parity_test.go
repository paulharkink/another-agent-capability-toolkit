package integration_test

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

func TestGitProviderImageHandlersMatchSharedHealthProxy(t *testing.T) {
	root := filepath.Join("..", "..")
	shared, err := os.ReadFile(filepath.Join(root, "packages", "git-provider", "mcp", "healthproxy", "handler.go"))
	if err != nil {
		t.Fatal(err)
	}
	packageDeclaration := regexp.MustCompile(`(?m)^package [a-zA-Z0-9_]+$`)
	shared = packageDeclaration.ReplaceAll(shared, []byte("package main"))
	for _, profile := range []string{"github", "gitlab", "bitbucket"} {
		profile := profile
		t.Run(profile, func(t *testing.T) {
			got, err := os.ReadFile(filepath.Join(root, "packages", "git-provider", "mcp", profile, "handler.go"))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, shared) {
				t.Errorf("%s image handler differs from shared healthproxy after package normalization", profile)
			}
		})
	}
}
