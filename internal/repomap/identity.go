package repomap

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CanonicalGitCommonDir resolves a checkout's common Git directory. Linked
// worktrees therefore share the same value while independent clones do not.
func CanonicalGitCommonDir(checkout string) (string, error) {
	gitPath := filepath.Join(checkout, ".git")
	info, err := os.Stat(gitPath)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		data, err := os.ReadFile(gitPath)
		if err != nil {
			return "", err
		}
		line := strings.TrimSpace(string(data))
		if !strings.HasPrefix(line, "gitdir:") {
			return "", fmt.Errorf("invalid .git file")
		}
		gitPath = strings.TrimSpace(strings.TrimPrefix(line, "gitdir:"))
		if !filepath.IsAbs(gitPath) {
			gitPath = filepath.Join(checkout, gitPath)
		}
	}
	if data, err := os.ReadFile(filepath.Join(gitPath, "commondir")); err == nil {
		common := strings.TrimSpace(string(data))
		if common == "" {
			return "", fmt.Errorf("empty git commondir")
		}
		if !filepath.IsAbs(common) {
			common = filepath.Join(gitPath, common)
		}
		gitPath = common
	} else if !os.IsNotExist(err) {
		return "", err
	}
	canonical, err := filepath.EvalSymlinks(gitPath)
	if err != nil {
		return "", err
	}
	return filepath.Clean(canonical), nil
}
