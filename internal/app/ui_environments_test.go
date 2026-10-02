package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUIEnvironmentSnapshotListsActualTargetsAndInvalidTOML(t *testing.T) {
	svc, _, _ := fixture(t)
	root := filepath.Join(t.TempDir(), "environments")
	svc.Source.EnvironmentRoot = root
	valid := filepath.Join(root, "company", "demo", "production.toml")
	invalid := filepath.Join(root, "company", "demo", "broken.toml")
	for path, contents := range map[string]string{valid: "[inputs]\nlabel = 'prod'\n", invalid: "[inputs\n"} {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := svc.UIEnvironmentSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Targets) != 2 {
		t.Fatalf("targets: %#v", got.Targets)
	}
	if got.Targets[0].Name != "broken" || got.Targets[0].Error == "" || got.Targets[0].Path != invalid {
		t.Fatalf("invalid TOML row: %#v", got.Targets[0])
	}
	if got.Targets[1].SourceID != svc.Source.ID || got.Targets[1].Environment != "company" || got.Targets[1].PackageID != "demo" || got.Targets[1].Name != "production" || got.Targets[1].Error != "" {
		t.Fatalf("valid row: %#v", got.Targets[1])
	}
	content, err := svc.UIEnvironmentTarget(context.Background(), valid)
	if err != nil || content != "[inputs]\nlabel = 'prod'\n" {
		t.Fatalf("exact TOML: %q, %v", content, err)
	}
	if _, err := svc.UIEnvironmentTarget(context.Background(), filepath.Join(t.TempDir(), "other.toml")); err == nil {
		t.Fatal("arbitrary path accepted")
	}
}

func TestUIEnvironmentSnapshotWithoutRootDoesNotInventFiles(t *testing.T) {
	svc, _, _ := fixture(t)
	svc.Source.EnvironmentRoot = ""
	got, err := svc.UIEnvironmentSnapshot(context.Background())
	if err != nil || len(got.Targets) != 0 {
		t.Fatalf("invented environment targets: %#v, %v", got, err)
	}
}

func TestUIEnvironmentSnapshotKeepsEmptyEnvironmentVisible(t *testing.T) {
	svc, _, _ := fixture(t)
	svc.Source.EnvironmentRoot = t.TempDir()
	if err := os.Mkdir(filepath.Join(svc.Source.EnvironmentRoot, "empty"), 0755); err != nil {
		t.Fatal(err)
	}
	got, err := svc.UIEnvironmentSnapshot(context.Background())
	if err != nil || len(got.Environments) != 1 || got.Environments[0] != "empty" || len(got.Targets) != 0 {
		t.Fatalf("empty environment disappeared: %#v, %v", got, err)
	}
}

func TestUIEnvironmentTargetRejectsEscapingSymlink(t *testing.T) {
	svc, _, _ := fixture(t)
	root := t.TempDir()
	svc.Source.EnvironmentRoot = root
	outside := filepath.Join(t.TempDir(), "secret.toml")
	if err := os.WriteFile(outside, []byte("secret = 'outside'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "company", "demo")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "escape.toml")
	if err := os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UIEnvironmentTarget(context.Background(), path); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("escaping target: %v", err)
	}
}
