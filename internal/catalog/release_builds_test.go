package catalog

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDiscoverMCPReleaseBuildsIncludesLegacyAndEveryProfileDeterministically(t *testing.T) {
	root := t.TempDir()
	write := func(id, manifest string) {
		t.Helper()
		dir := filepath.Join(root, id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "package.toml"), []byte(manifest), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("legacy", `schema_version=1
id="legacy"
name="Legacy"
[mcp]
name="server"
build_context="./docker"
release_image="ghcr.io/example/legacy:v{aact_version}"
`)
	write("multi", `schema_version=1
id="multi"
name="Multi"
[[mcps]]
name="alpha"
build_context="./alpha"
release_image="ghcr.io/example/multi-alpha:v{aact_version}"
[[mcps]]
name="beta"
build_context="./beta"
release_image="ghcr.io/example/multi-beta:v{aact_version}"
[[mcps]]
name="external"
image="vendor/server:1"
`)

	builds, err := DiscoverMCPReleaseBuilds(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []MCPReleaseBuild{
		{PackageID: "legacy", Profile: "server", BuildContext: "./docker", ReleaseImage: "ghcr.io/example/legacy:v{aact_version}"},
		{PackageID: "multi", Profile: "alpha", BuildContext: "./alpha", ReleaseImage: "ghcr.io/example/multi-alpha:v{aact_version}"},
		{PackageID: "multi", Profile: "beta", BuildContext: "./beta", ReleaseImage: "ghcr.io/example/multi-beta:v{aact_version}"},
	}
	if !reflect.DeepEqual(builds, want) {
		t.Fatalf("discovered builds = %#v, want %#v", builds, want)
	}
}

func TestBundledReleaseDiscoveryFindsAllSevenMCPImages(t *testing.T) {
	builds, err := DiscoverMCPReleaseBuilds("../../packages")
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(builds))
	for _, build := range builds {
		got = append(got, build.PackageID+"/"+build.Profile)
		contextDir := filepath.Join("../../packages", build.PackageID, build.BuildContext)
		if _, err := os.Stat(filepath.Join(contextDir, "Dockerfile")); err != nil {
			t.Errorf("%s Dockerfile missing at %s: %v", build.PackageID+"/"+build.Profile, contextDir, err)
		}
	}
	want := []string{
		"argocd/argocd",
		"azure-inspector/azure-inspector",
		"cluster-inspector/cluster-inspector",
		"git-provider/github",
		"git-provider/gitlab",
		"git-provider/bitbucket",
		"grafana-inspector/grafana-inspector",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("bundled release profiles = %#v, want %#v", got, want)
	}
}
