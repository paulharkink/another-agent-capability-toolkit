package versioninfo

import "testing"

func TestResolveImageUsesPinnedAACTReleaseVersion(t *testing.T) {
	old := Version
	Version = "1.2.3"
	t.Cleanup(func() { Version = old })
	got, err := ResolveImage("ghcr.io/example/server:v{aact_version}")
	if err != nil || got != "ghcr.io/example/server:v1.2.3" {
		t.Fatalf("resolved image %q, %v", got, err)
	}
}

func TestResolveImageRejectsDevelopmentVersion(t *testing.T) {
	old := Version
	Version = "dev"
	t.Cleanup(func() { Version = old })
	if _, err := ResolveImage("ghcr.io/example/server:v{aact_version}"); err == nil {
		t.Fatal("development build resolved a mutable release image")
	}
}
