package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/pelletier/go-toml/v2"
)

type Source struct {
	ID              string                    `json:"id"`
	Root            string                    `json:"root"`
	ManifestPath    string                    `json:"manifest_path"`
	EnvironmentRoot string                    `json:"environment_root"`
	ProfileRoot     string                    `json:"profile_root"`
	Catalog         []catalog.Package         `json:"catalog"`
	PackageDefaults map[string]map[string]any `json:"package_defaults"`
}
type sourceManifest struct {
	SchemaVersion int             `toml:"schema_version"`
	SourceID      string          `toml:"source_id"`
	PackID        string          `toml:"pack_id"`
	Catalog       []catalogEntry  `toml:"catalog"`
	Imports       []catalogImport `toml:"imports"`
	Environments  struct {
		Root string `toml:"root"`
	} `toml:"environments"`
	Packages map[string]struct {
		Inputs map[string]any `toml:"inputs"`
	} `toml:"packages"`
}

var safeID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

func Discover(cwd, explicitConfig, bundledRoot, stateRoot string) (Source, error) {
	return discover(cwd, explicitConfig, bundledRoot, stateRoot, false)
}

// DiscoverPreview does not create a saved local source identity or any other state.
func DiscoverPreview(cwd, explicitConfig, bundledRoot, stateRoot string) (Source, error) {
	return discover(cwd, explicitConfig, bundledRoot, stateRoot, true)
}
func discover(cwd, explicitConfig, bundledRoot, stateRoot string, preview bool) (Source, error) {
	cwd, err := filepath.Abs(cwd)
	if err != nil {
		return Source{}, err
	}
	manifest := ""
	if explicitConfig != "" {
		if !filepath.IsAbs(explicitConfig) {
			explicitConfig = filepath.Join(cwd, explicitConfig)
		}
		manifest = filepath.Clean(explicitConfig)
	} else {
		for dir := cwd; ; dir = filepath.Dir(dir) {
			candidate := filepath.Join(dir, "aact.toml")
			if _, e := os.Stat(candidate); e == nil {
				manifest = candidate
				break
			} else if !os.IsNotExist(e) {
				return Source{}, e
			}
			if _, e := os.Stat(filepath.Join(dir, ".git")); e == nil {
				break
			}
			if filepath.Dir(dir) == dir {
				break
			}
		}
	}
	if manifest == "" {
		return bundledSource(bundledRoot, stateRoot)
	}
	data, err := os.ReadFile(manifest)
	if err != nil {
		return Source{}, err
	}
	var m sourceManifest
	if err = toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields().Decode(&m); err != nil {
		if strict, ok := err.(*toml.StrictMissingError); ok {
			return Source{}, fmt.Errorf("%s: %s", manifest, strict.String())
		}
		return Source{}, fmt.Errorf("%s: %w", manifest, err)
	}
	if m.SchemaVersion != 1 {
		return Source{}, fmt.Errorf("%s: unsupported schema_version %d", manifest, m.SchemaVersion)
	}
	if m.PackID != "" {
		if m.SourceID != "" && m.SourceID != m.PackID {
			return Source{}, fmt.Errorf("%s: pack_id and source_id conflict", manifest)
		}
		m.SourceID = m.PackID
	}
	if m.SourceID != "" && !safeID.MatchString(m.SourceID) {
		return Source{}, fmt.Errorf("invalid source_id %q", m.SourceID)
	}
	root := filepath.Dir(manifest)
	id, err := sourceIdentity(manifest, m.SourceID, stateRoot, preview)
	if err != nil {
		return Source{}, err
	}
	s := Source{ID: id, Root: root, ManifestPath: manifest, ProfileRoot: filepath.Join(root, "environments"), PackageDefaults: map[string]map[string]any{}}
	for name, values := range m.Packages {
		s.PackageDefaults[name] = values.Inputs
	}
	if m.Environments.Root != "" {
		s.EnvironmentRoot, err = resolvePath(m.Environments.Root, manifest)
		if err != nil {
			return Source{}, err
		}
		s.ProfileRoot = s.EnvironmentRoot
	} else {
		s.EnvironmentRoot = legacyRoot(stateRoot)
		if s.EnvironmentRoot == "" {
			s.EnvironmentRoot = filepath.Join(root, "environments")
		}
	}
	entries, err := resolveCatalog(manifest, m, bundledRoot, map[string]bool{})
	if err != nil {
		return Source{}, err
	}
	for _, entry := range entries {
		s.Catalog = append(s.Catalog, entry.Package)
	}
	return s, nil
}
func bundledSource(root, stateRoot string) (Source, error) {
	s := Source{ID: "bundled", EnvironmentRoot: legacyRoot(stateRoot), PackageDefaults: map[string]map[string]any{}}
	if root == "" {
		return s, nil
	}
	abs, e := filepath.Abs(root)
	if e != nil {
		return Source{}, e
	}
	s.Root = abs
	s.ProfileRoot = filepath.Join(abs, "environments")
	entries, e := os.ReadDir(abs)
	if e != nil {
		return Source{}, e
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(abs, entry.Name())
		if _, e := os.Stat(filepath.Join(dir, "package.toml")); os.IsNotExist(e) {
			if _, e = os.Stat(filepath.Join(dir, "SKILL.md")); os.IsNotExist(e) {
				continue
			}
		}
		p, e := catalog.Load(dir)
		if e != nil {
			return Source{}, e
		}
		s.Catalog = append(s.Catalog, p)
	}
	return s, nil
}
func legacyRoot(stateRoot string) string {
	if stateRoot == "" {
		return ""
	}
	data, e := os.ReadFile(filepath.Join(stateRoot, "config-root"))
	if e != nil {
		return ""
	}
	root := strings.TrimSpace(string(data))
	if root == "" {
		return ""
	}
	resolved, e := resolvePath(root, filepath.Join(stateRoot, "config-root"))
	if e != nil {
		return ""
	}
	return resolved
}

// WithEnvironmentRoot applies the explicit CLI root, which has highest precedence.
func WithEnvironmentRoot(s Source, root string) (Source, error) {
	if root == "" {
		return s, nil
	}
	var err error
	s.EnvironmentRoot, err = resolvePath(root, filepath.Join(s.Root, "aact.toml"))
	s.ProfileRoot = s.EnvironmentRoot
	return s, err
}
func resolvePath(path, declaringFile string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path is empty")
	}
	if path == "~" || strings.HasPrefix(path, "~/") || strings.HasPrefix(path, "~\\") {
		home, e := os.UserHomeDir()
		if e != nil {
			return "", e
		}
		path = filepath.Join(home, strings.TrimLeft(path[1:], "/\\"))
	} else if strings.HasPrefix(path, "~") {
		return "", fmt.Errorf("named-user home expansion is unsupported: %s", path)
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(filepath.Dir(declaringFile), path)
	}
	return filepath.Abs(path)
}
