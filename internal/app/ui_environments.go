package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

var environmentPathID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

// UIEnvironmentSnapshot scans the current Capability Pack's environment directory without
// changing state. It never infers TOML files from saved installation profiles.
func (s *Service) UIEnvironmentSnapshot(ctx context.Context) (viewmodel.EnvironmentSnapshot, error) {
	out := viewmodel.EnvironmentSnapshot{SourceID: s.Source.ID, Root: s.Source.EnvironmentRoot}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if out.Root == "" {
		return out, nil
	}
	environments, err := os.ReadDir(out.Root)
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return out, fmt.Errorf("read environment directory %s: %w", out.Root, err)
	}
	for _, environment := range environments {
		if !environment.IsDir() || !validEnvironmentID(environment.Name()) {
			continue
		}
		out.Environments = append(out.Environments, environment.Name())
		packages, err := os.ReadDir(filepath.Join(out.Root, environment.Name()))
		if err != nil {
			return out, err
		}
		for _, pkg := range packages {
			if !pkg.IsDir() || !validEnvironmentID(pkg.Name()) {
				continue
			}
			dir := filepath.Join(out.Root, environment.Name(), pkg.Name())
			files, err := os.ReadDir(dir)
			if err != nil {
				return out, err
			}
			for _, file := range files {
				if file.IsDir() || filepath.Ext(file.Name()) != ".toml" {
					continue
				}
				name := strings.TrimSuffix(file.Name(), ".toml")
				if !validEnvironmentID(name) {
					continue
				}
				row := viewmodel.EnvironmentTarget{SourceID: out.SourceID, Environment: environment.Name(), PackageID: pkg.Name(), Name: name, Path: filepath.Join(dir, file.Name())}
				if _, err := config.LoadTarget(s.Source, row.PackageID, row.Environment, row.Name); err != nil {
					row.Error = err.Error()
				}
				out.Targets = append(out.Targets, row)
			}
		}
	}
	sort.Slice(out.Targets, func(i, j int) bool {
		a, b := out.Targets[i], out.Targets[j]
		if a.Environment != b.Environment {
			return a.Environment < b.Environment
		}
		if a.PackageID != b.PackageID {
			return a.PackageID < b.PackageID
		}
		return a.Name < b.Name
	})
	return out, nil
}

func validEnvironmentID(s string) bool {
	return environmentPathID.MatchString(s) && s != "." && s != ".."
}

// UIEnvironmentTarget returns exact TOML for a path in the current browser
// snapshot, including malformed TOML. The real path must stay in its root.
func (s *Service) UIEnvironmentTarget(ctx context.Context, path string) (string, error) {
	snapshot, err := s.UIEnvironmentSnapshot(ctx)
	if err != nil {
		return "", err
	}
	allowed := false
	for _, row := range snapshot.Targets {
		if row.Path == path {
			allowed = true
			break
		}
	}
	if !allowed {
		return "", fmt.Errorf("%s is not a discovered environment target", path)
	}
	root, err := filepath.EvalSymlinks(snapshot.Root)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("target %s resolves outside the environment directory", path)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(contents), nil
}
