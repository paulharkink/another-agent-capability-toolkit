package config

import "github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"

// Pack is the configured catalog and the profile storage supplied by its author.
// Source remains a compatibility facade while existing app consumers migrate.
type Pack struct {
	ID, Root, ManifestPath, InstanceRoot, ProfileRoot string
	Catalog                                           []catalog.Package
	GroupProfiles                                     []GroupProfile
	PackageDefaults                                   map[string]map[string]any
	PackageUI                                         map[string]PackageUI
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
	return Pack{ID: s.ID, Root: s.Root, ManifestPath: s.ManifestPath, InstanceRoot: s.instanceRoot(), ProfileRoot: s.instanceRoot(), Catalog: s.Catalog, GroupProfiles: s.GroupProfiles, PackageDefaults: s.PackageDefaults, PackageUI: s.PackageUI}
}
func (p Pack) LegacySource() Source {
	return Source{ID: p.ID, Root: p.Root, ManifestPath: p.ManifestPath, InstanceRoot: p.instanceRoot(), ProfileRoot: p.instanceRoot(), EnvironmentRoot: p.instanceRoot(), Catalog: p.Catalog, GroupProfiles: p.GroupProfiles, PackageDefaults: p.PackageDefaults, PackageUI: p.PackageUI}
}

func (p Pack) instanceRoot() string {
	if p.InstanceRoot != "" {
		return p.InstanceRoot
	}
	return p.ProfileRoot
}

func (s Source) instanceRoot() string {
	if s.InstanceRoot != "" {
		return s.InstanceRoot
	}
	if s.EnvironmentRoot != "" {
		return s.EnvironmentRoot
	}
	return s.ProfileRoot
}
