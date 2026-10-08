package agents

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestRegistryAdapterContractAndReadOnlyDetection(t *testing.T) {
	root := t.TempDir()
	registry := NewRegistry(Dependencies{Probe: DiscoveryProbe{GOOS: "linux", Home: root, Getenv: func(string) string { return "" }, LookPath: func(name string) (string, error) { return filepath.Join(root, "bin", name), nil }}})
	if len(registry.Adapters()) < 7 {
		t.Fatal("existing agent adapters missing")
	}
	for _, adapter := range registry.Adapters() {
		features := adapter.Features()
		_, skills := adapter.(SkillManager)
		_, mcps := adapter.(MCPManager)
		_, plugins := adapter.(PluginManager)
		if skills != features.Skills || mcps != features.MCPs || plugins != (len(features.PluginFormats) > 0) {
			t.Fatalf("%s advertises unimplemented feature", adapter.ID())
		}
		detection, err := adapter.Detect(context.Background(), Scope{Home: root, ExplicitHome: true})
		if err != nil || detection.State == "" {
			t.Fatalf("%s detection: %#v %v", adapter.ID(), detection, err)
		}
		for _, file := range detection.ConfigFiles {
			if file.Exists {
				t.Fatalf("fixture unexpectedly has config: %s", file.Path)
			}
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("detection wrote files: %#v %v", entries, err)
	}
}

func TestRegistryInstalledMissingConfigOverridesAndUnknownAgent(t *testing.T) {
	for _, goos := range []string{"darwin", "linux", "windows"} {
		t.Run(goos, func(t *testing.T) {
			root := t.TempDir()
			override := filepath.Join(root, "custom.jsonc")
			r := NewRegistry(Dependencies{Probe: DiscoveryProbe{GOOS: goos, Home: root, Getenv: func(string) string { return "" }, LookPath: func(name string) (string, error) {
				if name == "opencode" {
					return "fixture-opencode", nil
				}
				return "", fmt.Errorf("not installed")
			}}})
			a, err := r.Adapter("opencode")
			if err != nil {
				t.Fatal(err)
			}
			d, err := a.Detect(context.Background(), Scope{Home: root, ConfigPathOverride: override, ExplicitHome: true})
			if err != nil || !d.Installed || !d.CanCreateConfig || len(d.ConfigFiles) != 1 || d.ConfigFiles[0].Path != override || d.ConfigFiles[0].Exists {
				t.Fatalf("installed/missing config override: %#v %v", d, err)
			}
			absent, _ := r.Adapter("claude")
			d, err = absent.Detect(context.Background(), Scope{Home: root, ExplicitHome: true})
			if err != nil || d.Installed || d.State != "not-detected" {
				t.Fatalf("absent agent: %#v %v", d, err)
			}
			if _, err := r.Adapter("unimplemented-agent"); err == nil {
				t.Fatal("unknown agent accepted")
			}
		})
	}
}

func TestRegistryNativeOverridesWSLAndHermesDetection(t *testing.T) {
	root := t.TempDir()
	override := filepath.Join(root, "codex-context")
	probe := DiscoveryProbe{GOOS: "linux", Home: root, LookPath: func(name string) (string, error) { return "fixture-" + name, nil }, Getenv: func(name string) string {
		if name == "CODEX_HOME" {
			return override
		}
		if name == "WSL_DISTRO_NAME" {
			return "test-wsl"
		}
		return ""
	}}
	r := NewRegistry(Dependencies{Probe: probe})
	codex, _ := r.Adapter("codex")
	d, err := codex.Detect(context.Background(), Scope{})
	if err != nil || len(d.ConfigFiles) != 1 || d.ConfigFiles[0].Path != filepath.Join(override, "config.toml") || !d.Installed {
		t.Fatalf("native override: %#v %v", d, err)
	}
	if d.Reason == "" {
		t.Fatal("WSL scope explanation missing")
	}
	hermes, _ := r.Adapter("hermes")
	d, err = hermes.Detect(context.Background(), Scope{Home: root, ExplicitHome: true})
	if err != nil || !d.Installed {
		t.Fatalf("Hermes CLI was not detected: %#v %v", d, err)
	}
}

func TestAdapterDetectionReportsResolvedSkillDestination(t *testing.T) {
	home := t.TempDir()
	r := NewRegistry(Dependencies{Probe: DiscoveryProbe{GOOS: "linux", Home: home, Getenv: func(string) string { return "" }}})
	a, _ := r.Adapter("generic")
	d, err := a.Detect(context.Background(), Scope{ID: "generic", Home: home, ExplicitHome: true})
	if err != nil || d.Home != home || d.SkillsPath != filepath.Join(home, ".agents", "skills") {
		t.Fatalf("adapter layout missing: %+v %v", d, err)
	}
}

func TestAdapterOwnsCodexDesktopMCPReadiness(t *testing.T) {
	home := t.TempDir()
	apps := t.TempDir()
	os.MkdirAll(filepath.Join(apps, "Codex.app"), 0700)
	r := NewRegistry(Dependencies{Probe: DiscoveryProbe{GOOS: "darwin", Home: home, AppRoots: []string{apps}, LookPath: func(string) (string, error) { return "", fmt.Errorf("missing") }}})
	a, _ := r.Adapter("codex")
	d, err := a.Detect(context.Background(), Scope{})
	if err != nil || !d.Installed || d.CanCreateConfig || d.MCPDisabledReason == "" {
		t.Fatalf("desktop mistaken for usable CLI adapter: %+v %v", d, err)
	}
}
