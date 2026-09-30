package config

import (
	"path/filepath"
	"testing"
)

func gitOrigin(t *testing.T, root, origin string) {
	put(t, filepath.Join(root, ".git", "HEAD"), "ref: refs/heads/main\n")
	put(t, filepath.Join(root, ".git", "config"), "[core]\nrepositoryformatversion = 0\nbare = false\n[remote \"origin\"]\nurl = "+origin+"\n")
}
func TestIdentitySurvivesCheckoutMove(t *testing.T) {
	a, b := checkout(t), checkout(t)
	gitOrigin(t, a, "git@example.com:team/skills.git")
	gitOrigin(t, b, "ssh://git@example.com/team/skills.git")
	for _, root := range []string{a, b} {
		put(t, filepath.Join(root, "nested", "aact.toml"), "schema_version=1\n")
	}
	state := t.TempDir()
	sa, ea := Discover(filepath.Join(a, "nested"), "", "", state)
	sb, eb := Discover(filepath.Join(b, "nested"), "", "", state)
	if ea != nil || eb != nil || sa.ID != sb.ID {
		t.Fatalf("%#v %#v %v %v", sa, sb, ea, eb)
	}
}
func TestCopiedSourceDoesNotRepointLinks(t *testing.T) {
	a, b := checkout(t), checkout(t)
	for _, root := range []string{a, b} {
		put(t, filepath.Join(root, "aact.toml"), "schema_version=1\n")
	}
	state := t.TempDir()
	sa, ea := Discover(a, "", "", state)
	again, er := Discover(a, "", "", state)
	sb, eb := Discover(b, "", "", state)
	if ea != nil || er != nil || eb != nil || sa.ID != again.ID || sa.ID == sb.ID {
		t.Fatalf("%#v %#v %#v %v %v %v", sa, again, sb, ea, er, eb)
	}
}
