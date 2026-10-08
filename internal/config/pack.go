package config

import "github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"

// Pack is the configured catalog and the profile storage supplied by its author.
// Source remains a compatibility facade while existing app consumers migrate.
type Pack struct {
	ID, Root, ManifestPath, ProfileRoot string
	Catalog                             []catalog.Package
	PackageDefaults                     map[string]map[string]any
}

func DiscoverPack(cwd, explicitConfig, bundledRoot, stateRoot string) (Pack, error) {
	s, err := Discover(cwd, explicitConfig, bundledRoot, stateRoot)
	return s.CapabilityPack(), err
}
func DiscoverPackPreview(cwd, explicitConfig, bundledRoot, stateRoot string) (Pack, error) {
	s, err := DiscoverPreview(cwd, explicitConfig, bundledRoot, stateRoot)
	return s.CapabilityPack(), err
}
func (s Source) CapabilityPack() Pack {
	return Pack{ID: s.ID, Root: s.Root, ManifestPath: s.ManifestPath, ProfileRoot: s.ProfileRoot, Catalog: s.Catalog, PackageDefaults: s.PackageDefaults}
}
func (p Pack) LegacySource() Source {
	return Source{ID: p.ID, Root: p.Root, ManifestPath: p.ManifestPath, ProfileRoot: p.ProfileRoot, EnvironmentRoot: p.ProfileRoot, Catalog: p.Catalog, PackageDefaults: p.PackageDefaults}
}
