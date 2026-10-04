package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Repository struct {
	Host string `json:"host"`
	Name string `json:"name"`
	Path string `json:"path"`
}

var prunedDirectories = map[string]bool{
	".git": true, ".cache": true, ".npm": true, ".cargo": true, ".rustup": true,
	".gradle": true, ".venv": true, "venv": true, "node_modules": true, "vendor": true,
}

// Scan discovers working checkouts under each root using only filesystem access
// and go-git. It does not follow directory symlinks within a root, which prevents
// cycles and scans escaping the selected trees. Overlapping roots do not duplicate
// a checkout, while different paths for the same remote remain separate rows.
// Partial results accompany errors; callers must show the errors before publishing.
func Scan(ctx context.Context, roots []string, hosts []string) ([]Repository, error) {
	rows := []Repository{}
	if len(roots) == 0 {
		return rows, errors.New("at least one scan root is required")
	}
	allowed := map[string]bool{}
	for _, host := range hosts {
		host = strings.ToLower(strings.TrimSpace(host))
		if host != "" {
			allowed[host] = true
		}
	}
	seen := map[string]bool{}
	rowSeen := map[Repository]bool{}
	var problems []error
	for _, root := range roots {
		if err := ctx.Err(); err != nil {
			return rows, err
		}
		if strings.TrimSpace(root) == "" {
			problems = append(problems, errors.New("scan root is empty"))
			continue
		}
		absolute, err := filepath.Abs(root)
		if err != nil {
			problems = append(problems, fmt.Errorf("scan root %q: %w", root, err))
			continue
		}
		info, err := os.Lstat(absolute)
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			absolute, err = filepath.EvalSymlinks(absolute)
			if err == nil {
				info, err = os.Stat(absolute)
			}
		}
		if err != nil {
			problems = append(problems, fmt.Errorf("scan root %q: %w", root, err))
			continue
		}
		if !info.IsDir() {
			problems = append(problems, fmt.Errorf("scan root %q is not a directory", root))
			continue
		}
		err = filepath.WalkDir(absolute, func(path string, entry fs.DirEntry, walkErr error) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if walkErr != nil {
				problems = append(problems, fmt.Errorf("scan %q: %w", path, walkErr))
				return nil
			}
			if !entry.IsDir() {
				return nil
			}
			runtimeState := entry.Name() == "share" && filepath.Base(filepath.Dir(path)) == ".local"
			if path != absolute && (prunedDirectories[entry.Name()] || runtimeState) {
				return filepath.SkipDir
			}
			if seen[path] {
				return filepath.SkipDir
			}
			seen[path] = true
			if _, err := os.Lstat(filepath.Join(path, ".git")); err != nil {
				if !errors.Is(err, fs.ErrNotExist) {
					problems = append(problems, fmt.Errorf("inspect %q: %w", path, err))
				}
				return nil
			}
			origins, err := checkoutOriginURLs(path)
			if err != nil {
				problems = append(problems, fmt.Errorf("open checkout %q: %w", path, err))
				return nil
			}
			for _, origin := range origins {
				host, name, err := NormalizeRemote(origin)
				if err != nil {
					continue
				}
				if len(allowed) > 0 && !allowed[host] {
					continue
				}
				row := Repository{host, name, path}
				if !rowSeen[row] {
					rowSeen[row] = true
					rows = append(rows, row)
				}
			}
			return nil
		})
		if err != nil {
			problems = append(problems, err)
			if ctx.Err() != nil {
				break
			}
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Host != rows[j].Host {
			return rows[i].Host < rows[j].Host
		}
		if rows[i].Name != rows[j].Name {
			return rows[i].Name < rows[j].Name
		}
		return rows[i].Path < rows[j].Path
	})
	return rows, errors.Join(problems...)
}
