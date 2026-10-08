package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
)

func rememberedSkillSource(t *testing.T, svc *Service) (string, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "remembered")
	pkg := filepath.Join(root, "demo")
	if err := os.MkdirAll(pkg, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "SKILL.md"), []byte("---\nname: demo\ndescription: demo\n---\nRemembered"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "aact.toml"), []byte("schema_version = 1\npack_id = 'remembered'\n[[catalog]]\nid = 'demo'\nsource = './demo'\n"), 0644); err != nil {
		t.Fatal(err)
	}
	packageManifest := "schema_version = 1\nid = 'demo'\nname = 'Remembered Demo'\n[skill]\nname = 'demo'\nsource = './'\n"
	if err := os.WriteFile(filepath.Join(pkg, "package.toml"), []byte(packageManifest), 0644); err != nil {
		t.Fatal(err)
	}
	ref := sourceRef{ID: "remembered", Root: root, ManifestPath: filepath.Join(root, "aact.toml"), EnvironmentRoot: filepath.Join(root, "environments")}
	if err := os.MkdirAll(filepath.Dir(filepath.Join(svc.Store.Root(), "manager", "sources.json")), 0700); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteJSON(filepath.Join(svc.Store.Root(), "manager", "sources.json"), []sourceRef{ref}); err != nil {
		t.Fatal(err)
	}
	return root, pkg
}

func TestUXRememberedSourcePreviewAndInstallIgnoreCurrentCheckout(t *testing.T) {
	svc, _, store := fixture(t)
	root, _ := rememberedSkillSource(t, svc)
	remembered, err := svc.forSource("remembered")
	if err != nil {
		t.Fatal(err)
	}
	ref := writeProfileForTest(t, remembered, "demo", "default", "")
	preview, err := remembered.PreviewProfile(context.Background(), ProfileRequest{Ref: ref})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Key.Source != "remembered" || preview.PackRoot != root {
		t.Fatalf("preview used current checkout identity: key=%#v root=%q", preview.Key, preview.PackRoot)
	}
	isolateUXUserHome(t, t.TempDir())
	result, err := remembered.ApplyProfile(context.Background(), ProfileRequest{Ref: ref, DestinationIDs: []string{"all"}})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Saved {
		t.Fatalf("remembered source install was not saved: %#v", result)
	}
	rows, err := store.Installations()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 || rows[0].Key.Source != "remembered" {
		t.Fatalf("install did not retain remembered source identity: %#v", rows)
	}
}

func TestUXProfileInformationShowsRawAndResolvedPath(t *testing.T) {
	svc, _, _ := fixture(t)
	svc.Source.Catalog[0].Inputs = []catalog.Input{{Name: "repository", Label: "Repository", Type: "directory"}}
	root := filepath.Join(t.TempDir(), "profiles")
	svc.Source.ProfileRoot = root
	path := filepath.Join(root, "demo", "production.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	contents := "[inputs]\nrepository = '../src'\n"
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	ref := config.ProfileRef{PackID: svc.Source.ID, CapabilityID: "demo", Name: "production"}
	preview, err := svc.PreviewProfile(context.Background(), ProfileRequest{Ref: ref})
	if err != nil {
		t.Fatal(err)
	}
	if preview.ProfileTOML != contents || preview.ProfilePath != path {
		t.Fatalf("profile information lacks exact TOML/path: TOML=%q path=%q", preview.ProfileTOML, preview.ProfilePath)
	}
	foundResolved := false
	for _, input := range preview.Inputs {
		if input.Definition.Name == "repository" && input.Value == filepath.Join(root, "src") && input.ProvenancePath == path {
			foundResolved = true
		}
	}
	if !foundResolved {
		t.Fatalf("resolved input/origin path missing: %#v", preview.Inputs)
	}
}

func TestUXLocateSourceRejectsDifferentIdentity(t *testing.T) {
	svc, _, _ := fixture(t)
	rememberedSkillSource(t, svc)
	other := filepath.Join(t.TempDir(), "other")
	if err := os.MkdirAll(other, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "aact.toml"), []byte("schema_version = 1\nsource_id = 'different'\n"), 0644); err != nil {
		t.Fatal(err)
	}
	locator, ok := any(svc).(interface {
		UILocateSource(context.Context, string, string) error
	})
	if !ok {
		t.Fatal("Capability Pack recovery has no explicit validated Locate Capability Pack action")
	}
	err := locator.UILocateSource(context.Background(), "remembered", other)
	if err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("different source identity was accepted: %v", err)
	}
	refs, err := svc.sourceRefs()
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range refs {
		if ref.ID == "remembered" && ref.Root == other {
			t.Fatal("unrelated source replaced remembered identity")
		}
	}
}

func TestUXLocateSourceUpdatesPathOnlyForTheSameIdentity(t *testing.T) {
	svc, _, _ := fixture(t)
	rememberedSkillSource(t, svc)
	moved := filepath.Join(t.TempDir(), "moved")
	pkg := filepath.Join(moved, "demo")
	if err := os.MkdirAll(pkg, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "SKILL.md"), []byte("---\nname: demo\ndescription: demo\n---\nMoved"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(moved, "aact.toml"), []byte("schema_version = 1\nsource_id = 'remembered'\ncatalog = [{ id = 'demo', source = './demo' }]\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := svc.UILocateSource(context.Background(), "remembered", moved); err != nil {
		t.Fatal(err)
	}
	reopened, err := svc.forSource("remembered")
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Source.Root != moved || reopened.Source.ID != "remembered" {
		t.Fatalf("source identity/path was not updated: %#v", reopened.Source)
	}
}
