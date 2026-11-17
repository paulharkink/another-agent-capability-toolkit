package catalog

import (
	"fmt"
	"os"
	"path/filepath"
)

// MCPReleaseBuild is one bundled MCP image recipe discovered from package
// metadata. Profile declaration order is retained within each package.
type MCPReleaseBuild struct {
	PackageID    string `json:"package_id"`
	Profile      string `json:"profile"`
	BuildContext string `json:"build_context"`
	ReleaseImage string `json:"release_image"`
}

// DiscoverMCPReleaseBuilds loads package manifests under packagesDir and
// returns every MCP profile with both a Docker context and release image.
// Legacy [mcp] declarations are included through MCPProfiles.
func DiscoverMCPReleaseBuilds(packagesDir string) ([]MCPReleaseBuild, error) {
	entries, err := os.ReadDir(packagesDir)
	if err != nil {
		return nil, fmt.Errorf("read package directory: %w", err)
	}
	builds := make([]MCPReleaseBuild, 0)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		packageDir := filepath.Join(packagesDir, entry.Name())
		if _, err := os.Stat(filepath.Join(packageDir, "package.toml")); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return nil, fmt.Errorf("inspect package %s: %w", entry.Name(), err)
		}
		pkg, err := Load(packageDir)
		if err != nil {
			return nil, fmt.Errorf("load package %s: %w", entry.Name(), err)
		}
		for _, profile := range pkg.MCPProfiles() {
			if profile.BuildContext == "" || profile.ReleaseImage == "" {
				continue
			}
			builds = append(builds, MCPReleaseBuild{
				PackageID: pkg.ID, Profile: profile.Name,
				BuildContext: profile.BuildContext, ReleaseImage: profile.ReleaseImage,
			})
		}
	}
	return builds, nil
}
