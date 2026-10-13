package mcp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImageVariantKeyChangesWithBaseVersionRecipeAndPlatform(t *testing.T) {
	base := variantIdentity("sha256:base", "v2.10.1", "recipe-a", "linux/amd64")
	for _, changed := range []string{
		variantIdentity("sha256:other", "v2.10.1", "recipe-a", "linux/amd64"),
		variantIdentity("sha256:base", "v2.11.0", "recipe-a", "linux/amd64"),
		variantIdentity("sha256:base", "v2.10.1", "recipe-b", "linux/amd64"),
		variantIdentity("sha256:base", "v2.10.1", "recipe-a", "linux/arm64"),
	} {
		if base == changed {
			t.Fatal("variant identity did not change")
		}
	}
}

func TestStartBuildsLocalBaseOnceThenSelectedVariant(t *testing.T) {
	r, f, k := testRuntime(t)
	dir := t.TempDir()
	dockerfile := filepath.Join(dir, "Dockerfile.variant")
	if err := os.WriteFile(dockerfile, []byte("FROM scratch\nARG CLI_VERSION\nARG AACT_BASE_IMAGE\n"), 0600); err != nil {
		t.Fatal(err)
	}
	f.f = func(a []string) ([]byte, error) {
		if len(a) > 1 && a[1] == "inspect" {
			return nil, errors.New("No such object: runtime")
		}
		if len(a) > 2 && a[1] == "image" && a[2] == "inspect" {
			if strings.Contains(a[len(a)-1], "variant-") {
				return nil, errors.New("No such image")
			}
			return []byte("sha256:base\n"), nil
		}
		if len(a) > 1 && a[1] == "info" {
			return []byte("linux/amd64\n"), nil
		}
		return []byte("ok"), nil
	}
	_, err := r.Start(context.Background(), k, RunSpec{BuildContext: dir, HostPort: 8765, ContainerPort: 80, ImageVariant: &ImageVariantSpec{PackageRoot: dir, Dockerfile: dockerfile, Context: dir, Version: "v2.10.1", VersionArg: "CLI_VERSION", BaseImageArg: "AACT_BASE_IMAGE"}})
	if err != nil {
		t.Fatal(err)
	}
	var baseBuilds, variantBuilds int
	for _, call := range f.calls {
		if len(call) > 1 && call[1] == "build" {
			if strings.Contains(strings.Join(call, " "), "Dockerfile.variant") {
				variantBuilds++
			} else {
				baseBuilds++
			}
		}
	}
	if baseBuilds != 1 || variantBuilds != 1 {
		t.Fatalf("build calls base=%d variant=%d: %v", baseBuilds, variantBuilds, f.calls)
	}
	for _, call := range f.calls {
		if len(call) > 1 && call[1] == "build" && strings.Contains(strings.Join(call, " "), "Dockerfile.variant") {
			if !containsSequence(call, []string{"--build-arg", "AACT_BASE_IMAGE=sha256:base", "--build-arg", "CLI_VERSION=v2.10.1"}) {
				t.Fatalf("build args not passed as separate argv elements: %v", call)
			}
		}
	}
}

func TestStartBuildsVariantFromReleaseBase(t *testing.T) {
	r, f, k := testRuntime(t)
	dir := t.TempDir()
	dockerfile := filepath.Join(dir, "Dockerfile.variant")
	if err := os.WriteFile(dockerfile, []byte("FROM scratch\nARG CLI_VERSION\nARG AACT_BASE_IMAGE\n"), 0600); err != nil {
		t.Fatal(err)
	}
	f.f = func(a []string) ([]byte, error) {
		if len(a) > 1 && a[1] == "inspect" {
			return nil, errors.New("No such object: runtime")
		}
		if len(a) > 2 && a[1] == "image" && a[2] == "inspect" {
			if strings.Contains(a[len(a)-1], "variant-") {
				return nil, errors.New("No such image")
			}
			return []byte("sha256:release\n"), nil
		}
		if len(a) > 1 && a[1] == "info" {
			return []byte("linux/amd64\n"), nil
		}
		return []byte("cid"), nil
	}
	_, err := r.Start(context.Background(), k, RunSpec{Image: "registry.example/base:v1", HostPort: 8765, ContainerPort: 80, ImageVariant: &ImageVariantSpec{PackageRoot: dir, Dockerfile: dockerfile, Context: dir, Version: "v2.10.1", VersionArg: "CLI_VERSION", BaseImageArg: "AACT_BASE_IMAGE"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, call := range f.calls {
		if len(call) > 1 && call[1] == "build" && strings.Contains(strings.Join(call, " "), "/base:") {
			t.Fatalf("release source unexpectedly built as local base: %v", call)
		}
	}
}

func TestImageVariantRejectsPackagePathEscape(t *testing.T) {
	root := t.TempDir()
	outside := root + "-outside"
	dockerfile := filepath.Join(outside, "Dockerfile.variant")
	if err := os.MkdirAll(outside, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dockerfile, []byte("FROM scratch"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := validateVariantPaths(root, dockerfile, root); err == nil {
		t.Fatal("escaped recipe path accepted")
	}
}
func TestVariantBuildFailurePreservesExistingRuntime(t *testing.T) {
	r, f, k := testRuntime(t)
	dir := t.TempDir()
	dockerfile := filepath.Join(dir, "Dockerfile.variant")
	if err := os.WriteFile(dockerfile, []byte("FROM scratch\n"), 0600); err != nil {
		t.Fatal(err)
	}
	f.f = func(a []string) ([]byte, error) {
		if len(a) > 1 && a[1] == "inspect" {
			return nil, errors.New("No such object: existing")
		}
		if len(a) > 1 && a[1] == "info" {
			return []byte("linux/amd64"), nil
		}
		if len(a) > 2 && a[1] == "image" && a[2] == "inspect" {
			if strings.Contains(a[len(a)-1], "variant-") {
				return nil, errors.New("No such image")
			}
			return []byte("sha256:base"), nil
		}
		if len(a) > 1 && a[1] == "build" && strings.Contains(strings.Join(a, " "), "Dockerfile.variant") {
			return nil, errors.New("unavailable release")
		}
		return []byte("ok"), nil
	}
	_, err := r.Start(context.Background(), k, RunSpec{Image: "fixture", HostPort: 8765, ContainerPort: 80, ImageVariant: &ImageVariantSpec{PackageRoot: dir, Dockerfile: dockerfile, Context: dir, Version: "v2.10.1", VersionArg: "CLI_VERSION", BaseImageArg: "AACT_BASE_IMAGE"}})
	if err == nil {
		t.Fatal("expected variant build failure")
	}
	for _, call := range f.calls {
		if len(call) > 1 && (call[1] == "stop" || call[1] == "rm" || call[1] == "run") {
			t.Fatalf("failed build changed runtime: %v", call)
		}
	}
}
func TestVariantInvalidOrUnavailableReleaseFailsWithoutFallback(t *testing.T) {
	r, f, k := testRuntime(t)
	dir := t.TempDir()
	dockerfile := filepath.Join(dir, "Dockerfile.variant")
	if err := os.WriteFile(dockerfile, []byte("FROM scratch"), 0600); err != nil {
		t.Fatal(err)
	}
	base := RunSpec{Image: "registry.example/base:v1", HostPort: 8765, ContainerPort: 80, ImageVariant: &ImageVariantSpec{PackageRoot: dir, Dockerfile: dockerfile, Context: dir, Version: "latest", VersionArg: "CLI_VERSION", BaseImageArg: "AACT_BASE_IMAGE"}}
	if _, err := r.Start(context.Background(), k, base); err == nil || !strings.Contains(err.Error(), "invalid selected") {
		t.Fatalf("invalid version accepted: %v", err)
	}
	if len(f.calls) != 0 {
		t.Fatalf("invalid version caused Docker effects: %v", f.calls)
	}
	base.ImageVariant.Version = "v2.10.1"
	f.f = func(a []string) ([]byte, error) {
		if len(a) > 1 && a[1] == "pull" {
			return nil, errors.New("manifest unknown")
		}
		return []byte("unexpected"), nil
	}
	if _, err := r.Start(context.Background(), k, base); err == nil {
		t.Fatal("unavailable release unexpectedly succeeded")
	}
	for _, call := range f.calls {
		if len(call) > 1 && (call[1] == "build" || call[1] == "run") {
			t.Fatalf("unavailable release fell back or started runtime: %v", call)
		}
	}
}

func variantIdentity(base, version, recipe, platform string) string {
	return variantKey(base, version, recipe, platform)
}
func containsSequence(haystack, needle []string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		match := true
		for j := range needle {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func TestImageVariantKeyChangesWhenDockerfileOutsideContextChanges(t *testing.T) {
	r, f, k := testRuntime(t)
	root := t.TempDir()
	contextDir := filepath.Join(root, "context")
	if err := os.MkdirAll(contextDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(contextDir, "payload.txt"), []byte("stable context"), 0600); err != nil {
		t.Fatal(err)
	}
	dockerfile := filepath.Join(root, "Dockerfile.variant")
	if err := os.WriteFile(dockerfile, []byte("FROM ${AACT_BASE_IMAGE}\nARG CLI_VERSION\n"), 0600); err != nil {
		t.Fatal(err)
	}
	f.f = func(a []string) ([]byte, error) {
		if len(a) > 2 && a[1] == "image" && a[2] == "inspect" {
			if strings.Contains(a[len(a)-1], "variant-") {
				return nil, errors.New("No such image")
			}
			return []byte("sha256:base"), nil
		}
		if len(a) > 1 && a[1] == "info" {
			return []byte("linux/amd64"), nil
		}
		return []byte("ok"), nil
	}
	base := RunSpec{Image: "fixture", ImageVariant: &ImageVariantSpec{PackageRoot: root, Dockerfile: dockerfile, Context: contextDir, Version: "v2.10.1", VersionArg: "CLI_VERSION", BaseImageArg: "AACT_BASE_IMAGE"}}
	first := base
	if err := r.prepareImageVariant(context.Background(), k, &first); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dockerfile, []byte("FROM ${AACT_BASE_IMAGE}\nARG CLI_VERSION\n# changed recipe\n"), 0600); err != nil {
		t.Fatal(err)
	}
	second := base
	if err := r.prepareImageVariant(context.Background(), k, &second); err != nil {
		t.Fatal(err)
	}
	if first.ImageVariantKey == second.ImageVariantKey {
		t.Fatal("Dockerfile content outside context did not change variant identity")
	}
	if first.Image == second.Image {
		t.Fatal("Dockerfile content outside context reused the derived image tag")
	}
}
