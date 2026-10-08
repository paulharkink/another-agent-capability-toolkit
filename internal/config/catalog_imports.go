package config

import (
	"fmt"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"os"
	"path/filepath"
	"strings"
)

type catalogEntry struct {
	ID     string                 `toml:"id"`
	Source string                 `toml:"source"`
	Sets   []catalog.ComponentSet `toml:"sets"`
}
type catalogOverride struct {
	ID   string                 `toml:"id"`
	Sets []catalog.ComponentSet `toml:"sets"`
}
type catalogImport struct {
	Catalog   string                     `toml:"catalog"`
	Include   []string                   `toml:"include"`
	Overrides map[string]catalogOverride `toml:"overrides"`
}
type catalogDeclaration struct {
	Package  catalog.Package
	Manifest string
}

func resolveCatalog(manifest string, m sourceManifest, bundledRoot string, stack map[string]bool) ([]catalogDeclaration, error) {
	canonical, err := filepath.EvalSymlinks(manifest)
	if err != nil {
		return nil, fmt.Errorf("catalog %s: %w", manifest, err)
	}
	if stack[canonical] {
		return nil, fmt.Errorf("catalog import cycle at %s", manifest)
	}
	stack[canonical] = true
	defer delete(stack, canonical)
	resolved := []catalogDeclaration{}
	for _, im := range m.Imports {
		if strings.Contains(im.Catalog, "://") {
			return nil, fmt.Errorf("%s: initialize external catalogs locally before importing %q", manifest, im.Catalog)
		}
		path, err := resolvePath(im.Catalog, manifest)
		if err != nil {
			return nil, fmt.Errorf("%s: import: %w", manifest, err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("%s: imported catalog %s: %w", manifest, path, err)
		}
		imported, err := decodeSourceManifest(data, path)
		if err != nil {
			return nil, err
		}
		if imported.SchemaVersion != 1 {
			return nil, fmt.Errorf("%s: unsupported schema_version %d", path, imported.SchemaVersion)
		}
		entries, err := resolveCatalog(path, imported, bundledRoot, stack)
		if err != nil {
			return nil, err
		}
		known := map[string]bool{}
		for _, entry := range entries {
			known[entry.Package.ID] = true
		}
		selected := map[string]bool{}
		for _, id := range im.Include {
			if !known[id] {
				return nil, fmt.Errorf("%s: imported catalog %s has no capability %q", manifest, path, id)
			}
			selected[id] = true
		}
		for id := range im.Overrides {
			if !known[id] {
				return nil, fmt.Errorf("%s: override names unknown capability %q in %s", manifest, id, path)
			}
		}
		for _, entry := range entries {
			if len(im.Include) > 0 && !selected[entry.Package.ID] {
				continue
			}
			if override, ok := im.Overrides[entry.Package.ID]; ok {
				if override.ID != "" {
					entry.Package.ID = override.ID
				}
				if override.Sets != nil {
					entry.Package.Sets = override.Sets
				}
				if err := validateConfiguredEntry(entry.Package); err != nil {
					return nil, fmt.Errorf("%s: imported override: %w", manifest, err)
				}
			}
			resolved = append(resolved, entry)
		}
	}
	for _, entry := range m.Catalog {
		var dir string
		if strings.HasPrefix(entry.Source, "bundled:") {
			name := strings.TrimPrefix(entry.Source, "bundled:")
			if !safeID.MatchString(name) {
				return nil, fmt.Errorf("%s: invalid bundled source %q", manifest, entry.Source)
			}
			if bundledRoot == "" {
				return nil, fmt.Errorf("%s: bundled catalog unavailable for %s", manifest, name)
			}
			dir = filepath.Join(bundledRoot, name)
			if err := contained(bundledRoot, dir); err != nil {
				return nil, err
			}
		} else {
			if strings.Contains(entry.Source, "://") {
				return nil, fmt.Errorf("%s: initialize URL capabilities locally before adding them to the catalog", manifest)
			}
			dir, err = resolvePath(entry.Source, manifest)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", manifest, err)
			}
		}
		pkg, err := catalog.Load(dir)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", manifest, err)
		}
		if entry.ID != "" {
			pkg.ID = entry.ID
		}
		pkg.Sets = entry.Sets
		if err := validateConfiguredEntry(pkg); err != nil {
			return nil, fmt.Errorf("%s: %w", manifest, err)
		}
		resolved = append(resolved, catalogDeclaration{Package: pkg, Manifest: manifest})
	}
	seen := map[string]string{}
	for _, entry := range resolved {
		if previous, ok := seen[entry.Package.ID]; ok {
			return nil, fmt.Errorf("duplicate catalog capability %q declared in %s and %s; configure distinct names", entry.Package.ID, previous, entry.Manifest)
		}
		seen[entry.Package.ID] = entry.Manifest
	}
	return resolved, nil
}

func validateConfiguredEntry(pkg catalog.Package) error {
	if !safeID.MatchString(pkg.ID) {
		return fmt.Errorf("invalid catalog id %q", pkg.ID)
	}
	_, err := catalog.InstallationItems(pkg, pkg.Sets)
	return err
}
