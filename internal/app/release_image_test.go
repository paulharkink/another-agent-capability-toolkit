package app

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/versioninfo"
)

func TestStartConfigurationProfileResolvesReleaseImageOrLocalDevelopmentBuild(t *testing.T) {
	old := versioninfo.ImageVersion
	t.Cleanup(func() { versioninfo.ImageVersion = old })
	for _, tc := range []struct{ name, version, wantImage, wantContext string }{
		{"release", "1.2.3", "ghcr.io/example/provider:v1.2.3", ""},
		{"development", "dev", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			versioninfo.ImageVersion = tc.version
			svc, _, _ := fixture(t)
			if tc.name == "development" {
				if err := svc.UISetMCPImageSource(context.Background(), "local"); err != nil {
					t.Fatal(err)
				}
			}
			runtime := &providerTestRuntime{}
			svc.Options.Runtime = runtime
			pkg := catalog.Package{ID: "fixture", Dir: t.TempDir(), MCP: &catalog.MCP{Name: "provider", Image: "upstream", BuildContext: "./mcp", ReleaseImage: "ghcr.io/example/provider:v{aact_version}", Transport: "streamable-http", HostPortInput: "port", ContainerPort: 80}}
			parent := state.Key{Source: "fixture-pack", Package: "fixture", Target: "default"}
			profile := config.Profile{Ref: config.ProfileRef{PackID: parent.Source, CapabilityID: parent.Package, Name: parent.Target}}
			_, err := svc.startConfigurationProfile(context.Background(), pkg, profile, parent, map[string]any{"port": int64(8765)}, false)
			if err != nil {
				t.Fatal(err)
			}
			got := runtime.specs[0]
			if tc.name == "development" {
				if got.Image != "" || got.BuildContext != filepath.Join(pkg.Dir, "mcp") {
					t.Fatalf("development image/context = %q/%q", got.Image, got.BuildContext)
				}
			} else if got.Image != tc.wantImage || got.BuildContext != tc.wantContext {
				t.Fatalf("release image/context = %q/%q, want %q/%q", got.Image, got.BuildContext, tc.wantImage, tc.wantContext)
			}
		})
	}
}
