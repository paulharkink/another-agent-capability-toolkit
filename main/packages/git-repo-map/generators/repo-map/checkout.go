package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-git/go-billy/v5/osfs"
	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/cache"
	"github.com/go-git/go-git/v5/storage/filesystem"
	"github.com/go-git/go-git/v5/storage/filesystem/dotgit"
)

func openCheckout(path string) (*git.Repository, error) {
	// PlainOpen resolves .git files and closes their handles. The pinned go-git
	// common-directory option leaves commondir open, so read it with os.ReadFile
	// and provide the same filesystem layout to go-git without that option.
	repo, err := git.PlainOpen(path)
	if err != nil {
		return nil, err
	}
	dot := repo.Storer.(*filesystem.Storage).Filesystem()
	data, err := os.ReadFile(filepath.Join(dot.Root(), "commondir"))
	if os.IsNotExist(err) {
		return repo, nil
	}
	if err != nil {
		return nil, err
	}
	common := strings.TrimSpace(string(data))
	if common == "" {
		return repo, nil
	}
	if !filepath.IsAbs(common) {
		common = filepath.Join(dot.Root(), common)
	}
	if _, err := os.Stat(common); err != nil {
		if os.IsNotExist(err) {
			return nil, git.ErrRepositoryIncomplete
		}
		return nil, err
	}
	storage := filesystem.NewStorage(dotgit.NewRepositoryFilesystem(dot, osfs.New(common)), cache.NewObjectLRUDefault())
	return git.Open(storage, osfs.New(path))
}

func checkoutOriginURLs(path string) ([]string, error) {
	repo, err := openCheckout(path)
	if err == nil {
		remote, err := repo.Remote("origin")
		if errors.Is(err, git.ErrRemoteNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		return remote.Config().URLs, nil
	}
	if !errors.Is(err, git.ErrUnsupportedExtensionRepositoryFormatVersion) && !errors.Is(err, git.ErrUnknownExtension) {
		return nil, err
	}
	return checkoutOriginURLsFromConfig(path)
}

// Mapping remotes only needs Git config. Some valid Git extensions are not
// supported by go-git's repository opener, so read that config directly.
func checkoutOriginURLsFromConfig(path string) ([]string, error) {
	gitPath := filepath.Join(path, ".git")
	info, err := os.Stat(gitPath)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		data, err := os.ReadFile(gitPath)
		if err != nil {
			return nil, err
		}
		line := strings.TrimSpace(string(data))
		if !strings.HasPrefix(line, "gitdir:") {
			return nil, fmt.Errorf("invalid .git file in %q", path)
		}
		gitPath = strings.TrimSpace(strings.TrimPrefix(line, "gitdir:"))
		if gitPath == "" {
			return nil, fmt.Errorf("empty gitdir in %q", path)
		}
		if !filepath.IsAbs(gitPath) {
			gitPath = filepath.Join(path, gitPath)
		}
	}
	if data, err := os.ReadFile(filepath.Join(gitPath, "commondir")); err == nil {
		common := strings.TrimSpace(string(data))
		if common == "" {
			return nil, fmt.Errorf("empty commondir in %q", path)
		}
		if !filepath.IsAbs(common) {
			common = filepath.Join(gitPath, common)
		}
		gitPath = common
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(gitPath, "config"))
	if err != nil {
		return nil, err
	}
	cfg := config.NewConfig()
	if err := cfg.Unmarshal(data); err != nil {
		return nil, err
	}
	if origin := cfg.Remotes["origin"]; origin != nil {
		return origin.URLs, nil
	}
	return nil, nil
}
