package versioninfo

import "testing"

func TestResolveImageUsesPublishedImageVersionForSnapshotBuild(t *testing.T) {
	oldVersion, oldImageVersion := Version, ImageVersion
	Version = "0.5.1-SNAPSHOT-feature-test"
	ImageVersion = "0.5.0"
	t.Cleanup(func() { Version, ImageVersion = oldVersion, oldImageVersion })
	got, err := ResolveImage("ghcr.io/example/server:v{aact_version}")
	if err != nil || got != "ghcr.io/example/server:v0.5.0" {
		t.Fatalf("resolved image %q, %v", got, err)
	}
}

func TestResolveImageRejectsMissingPublishedImageVersion(t *testing.T) {
	oldVersion, oldImageVersion := Version, ImageVersion
	Version = "0.5.1-SNAPSHOT-feature-test"
	ImageVersion = "dev"
	t.Cleanup(func() { Version, ImageVersion = oldVersion, oldImageVersion })
	if _, err := ResolveImage("ghcr.io/example/server:v{aact_version}"); err == nil {
		t.Fatal("snapshot without a published image pin resolved a mutable image")
	}
}
