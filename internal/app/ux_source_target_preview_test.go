package app

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
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
	if err := os.WriteFile(filepath.Join(root, "aact.toml"), []byte("schema_version = 1\nsource_id = 'remembered'\n"), 0644); err != nil {
		t.Fatal(err)
	}
	ref := sourceRef{ID: "remembered", Root: root, EnvironmentRoot: filepath.Join(root, "env"), PackageDirs: []string{pkg}}
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
	preview, err := svc.UISetupPreview(context.Background(), viewmodel.SetupRequest{SourceID: "remembered", PackageID: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Key.Source != "remembered" || preview.SourceRoot != root {
		t.Fatalf("preview used current checkout identity: key=%#v root=%q", preview.Key, preview.SourceRoot)
	}
	t.Setenv("HOME", t.TempDir())
	result, err := svc.UIInstall(context.Background(), viewmodel.SetupInstallRequest{
		SetupRequest:   viewmodel.SetupRequest{SourceID: "remembered", PackageID: "demo"},
		DestinationIDs: []string{"all"}, Inputs: map[string]any{},
	})
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

func TestUXTargetInformationShowsRawAndResolvedPath(t *testing.T) {
	svc, _, _ := fixture(t)
	svc.Source.Catalog[0].Inputs = []catalog.Input{{Name: "repository", Label: "Repository", Type: "directory"}}
	root := filepath.Join(t.TempDir(), "env")
	svc.Source.EnvironmentRoot = root
	path := filepath.Join(root, "company", "demo", "prod.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("[inputs]\nrepository = '../src'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	preview, err := svc.UISetupPreview(context.Background(), viewmodel.SetupRequest{SourceID: svc.Source.ID, PackageID: "demo", Environment: "company", Target: "prod"})
	if err != nil {
		t.Fatal(err)
	}
	field := reflect.ValueOf(preview).FieldByName("TargetTOML")
	if !field.IsValid() || field.String() != "[inputs]\nrepository = '../src'\n" || preview.TargetPath != path {
		t.Fatalf("target information lacks exact TOML/path: field=%v path=%q", field, preview.TargetPath)
	}
	foundResolved := false
	for _, input := range preview.Inputs {
		if input.Definition.Name == "repository" && input.Value == filepath.Join(root, "company", "src") && input.ProvenancePath == path {
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
		t.Fatal("source recovery has no explicit validated Locate source action")
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
