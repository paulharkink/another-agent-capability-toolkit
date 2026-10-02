package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/go-git/go-billy/v5/osfs"
	git "github.com/go-git/go-git/v5"
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
