package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/agents"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

func TestUISetupPreviewKeepsUnknownSavedAgentVisibleAndUnavailable(t *testing.T) {
	svc, _, store := fixture(t)
	svc.Source.Catalog[0].Skill = nil
	svc.Source.Catalog[0].MCP = &catalog.MCP{Name: "demo", Transport: "streamable-http"}
	ref := profileRefForFixture(svc, svc.Source.ID, "demo", "default")
	key, err := store.ResolveProfileKey(ref.PackID, ref.CapabilityID, ref.Name)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordProfile(state.ProfileRecord{
		Key: key, Name: ref.Name,
		Selection: &state.ProfileSelection{DestinationIDs: []string{"retired-agent"}},
	}); err != nil {
		t.Fatal(err)
	}

	preview, err := svc.PreviewProfile(context.Background(), ProfileRequest{Ref: ref})
	if err != nil {
		t.Fatal(err)
	}
	for _, destination := range preview.Destinations {
		if destination.ID != "retired-agent" {
			continue
		}
		if destination.Selected {
			t.Fatalf("unknown saved agent must not appear as an achieved/eligible destination: %+v", destination)
		}
		if destination.DisabledReason == "" {
			t.Fatalf("unknown historical agent must explain why it cannot be selected: %+v", destination)
		}
		return
	}
	t.Fatal("unknown saved agent was omitted from setup destinations")
}

func TestUISetupPreviewShowsUnsupportedMCPAgentAsUnavailable(t *testing.T) {
	home := t.TempDir()
	isolateUXUserHome(t, home)
	svc, _, _ := fixture(t)
	svc.Source.Catalog[0].Skill = nil
	svc.Source.Catalog[0].MCP = &catalog.MCP{Name: "demo", Transport: "streamable-http"}
	adapter := &uiBoundaryAdapter{
		features:  agents.FeatureSet{Skills: true},
		detection: agents.Detection{Home: filepath.Join(home, "codex"), State: "installed", Installed: true},
	}
	svc.Options.Adapters = uiBoundaryRegistry{fallback: svc.adapterRegistry(), adapter: adapter}

	preview, err := svc.previewProfileFixture(context.Background(), viewmodel.SetupRequest{PackageID: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	for _, destination := range preview.Destinations {
		if destination.ID != "codex" {
			continue
		}
		if destination.DisabledReason == "" || !strings.Contains(destination.DisabledReason, "MCP") {
			t.Fatalf("agent without MCP support should remain visible with its unsupported-operation reason: %+v", destination)
		}
		return
	}
	t.Fatal("unsupported MCP-capable adapter was hidden instead of shown disabled")
}

func TestUISetupPreviewDisablesDetectedMCPAgentWithoutConfigCreationSupport(t *testing.T) {
	home := t.TempDir()
	isolateUXUserHome(t, home)
	svc, _, _ := fixture(t)
	svc.Source.Catalog[0].Skill = nil
	svc.Source.Catalog[0].MCP = &catalog.MCP{Name: "demo", Transport: "streamable-http"}
	adapter := &uiBoundaryAdapter{
		features: agents.FeatureSet{Skills: true, MCPs: true},
		detection: agents.Detection{
			Home: filepath.Join(home, "codex"), State: "installed", Installed: true,
			ConfigPath:  filepath.Join(home, "codex", "config.toml"),
			ConfigFiles: []agents.ConfigFile{{Path: filepath.Join(home, "codex", "config.toml"), Exists: false}},
		},
	}
	svc.Options.Adapters = uiBoundaryRegistry{fallback: svc.adapterRegistry(), adapter: adapter}

	preview, err := svc.previewProfileFixture(context.Background(), viewmodel.SetupRequest{PackageID: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	for _, destination := range preview.Destinations {
		if destination.ID != "codex" {
			continue
		}
		if destination.DisabledReason == "" {
			t.Fatalf("detected agent with no config and no config-creation support remained eligible: %+v", destination)
		}
		return
	}
	t.Fatal("detected adapter missing from setup destinations")
}

func TestUISetupPreviewExplainsUndetectedAndUnverifiedAgents(t *testing.T) {
	for _, tc := range []struct {
		name, state, reason string
	}{
		{name: "not detected", state: "not-detected", reason: "CLI executable was not found"},
		{name: "unverified", state: "unverified", reason: "Installation could not be confirmed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			isolateUXUserHome(t, home)
			svc, _, _ := fixture(t)
			svc.Source.Catalog[0].Skill = nil
			svc.Source.Catalog[0].MCP = &catalog.MCP{Name: "demo", Transport: "streamable-http"}
			adapter := &uiBoundaryAdapter{
				features:  agents.FeatureSet{MCPs: true},
				detection: agents.Detection{Home: filepath.Join(home, "codex"), State: tc.state, Reason: tc.reason},
			}
			svc.Options.Adapters = uiBoundaryRegistry{fallback: svc.adapterRegistry(), adapter: adapter}

			preview, err := svc.previewProfileFixture(context.Background(), viewmodel.SetupRequest{PackageID: "demo"})
			if err != nil {
				t.Fatal(err)
			}
			for _, destination := range preview.Destinations {
				if destination.ID != "codex" {
					continue
				}
				if destination.Detection != tc.state || !strings.Contains(destination.DisabledReason, tc.reason) {
					t.Fatalf("%s detection should be visible with its reason: %+v", tc.name, destination)
				}
				return
			}
			t.Fatal("undetected agent was hidden from setup destinations")
		})
	}
}
