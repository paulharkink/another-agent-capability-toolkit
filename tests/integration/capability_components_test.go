package integration_test

import (
	"context"
	"fmt"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/app"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCapabilityChildFailure(t *testing.T) {
	if os.Getenv("AACT_FIXTURE_CHILD") != "1" {
		return
	}
	fmt.Fprintln(os.Stderr, "fixture preparation refused: missing example certificate")
	os.Exit(3)
}

func TestCapabilityComponentsActualChildErrorAndLocalProfile(t *testing.T) {
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	p := catalog.Package{ID: "neutral", Name: "Neutral", Dir: t.TempDir(), MCP: &catalog.MCP{Name: "api", Image: "placeholder", Transport: "streamable-http", Actions: map[string]catalog.Command{"prepare": {Argv: []string{exe, "-test.run=^TestCapabilityChildFailure$"}}}}}
	root := t.TempDir()
	t.Setenv("AACT_FIXTURE_CHILD", "1")
	var stderr string
	svc := app.New(config.Pack{ID: "source-only", Root: root, ProfileRoot: filepath.Join(root, "environments"), Catalog: []catalog.Package{p}}.LegacySource(), store, app.Options{OnStderr: func(b []byte) { stderr += string(b) }})
	ref := config.ProfileRef{PackID: "source-only", CapabilityID: p.ID, Name: "local"}
	snapshot, err := svc.ProfileSnapshot(context.Background(), p.ID)
	if err != nil || len(snapshot.Profiles) != 0 {
		t.Fatalf("invented profile: %+v %v", snapshot, err)
	}
	if err := svc.CreateProfile(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
	// Runtime action executes the real generic manifest command; it fails before Docker.
	_, err = svc.RunProfileMCP(context.Background(), "prepare", app.ProfileRequest{Ref: ref}, "api")
	if err == nil || !strings.Contains(err.Error(), "missing example certificate") || !strings.Contains(stderr, "missing example certificate") {
		t.Fatalf("actual stderr lost: %v stderr=%q", err, stderr)
	}
	// A plugins-only artifact must be rejected by the skills-only generic adapter.
	p.MCP = nil
	p.Plugins = []catalog.Plugin{{Name: "native", Format: "claude-code", Source: root}}
	svc.Source.Catalog = []catalog.Package{p}
	svc.Options.AgentScopes = nil
	out, err := svc.ApplyProfile(context.Background(), app.ProfileRequest{Ref: ref, ItemIDs: []string{"plugin:native"}, DestinationIDs: []string{"all"}})
	if err == nil || !strings.Contains(err.Error(), "does not support plugin") || out.Saved {
		t.Fatalf("unsupported plugin reached effects: %+v %v", out, err)
	}
}
