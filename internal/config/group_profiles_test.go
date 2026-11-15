package config

import (
	"path/filepath"
	"testing"
)

func TestPackDiscoveryLoadsCrossCapabilityProfilesAndMembers(t *testing.T) {
	root := checkout(t)
	put(t, filepath.Join(root, "inspect", "package.toml"), "schema_version=1\nid='inspect'\nname='Inspector'\n[mcp]\nname='inspect'\n")
	put(t, filepath.Join(root, "instances", "inspect", "prod.toml"), "[inputs]\nurl='https://grafana.example.test'\n")
	put(t, filepath.Join(root, "aact.toml"), `schema_version=1
pack_id="team"
[[catalog]]
id="inspect"
source="./inspect"
[[profiles]]
name="operations"
description="Production services"
[[profiles.members]]
capability="inspect"
instance="prod"
`)

	pack, err := DiscoverPack(root, "", "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(pack.GroupProfiles) != 1 {
		t.Fatalf("group profiles = %#v", pack.GroupProfiles)
	}
	profile := pack.GroupProfiles[0]
	if profile.Name != "operations" || profile.Description != "Production services" || profile.Error != "" || len(profile.Members) != 1 {
		t.Fatalf("profile declaration = %#v", profile)
	}
	want := InstanceRef{PackID: "team", CapabilityID: "inspect", Name: "prod"}
	if profile.Members[0] != want {
		t.Fatalf("profile member = %#v, want %#v", profile.Members[0], want)
	}
}

func TestInvalidPackGroupProfileIsReportedWithoutHidingValidProfiles(t *testing.T) {
	root := checkout(t)
	put(t, filepath.Join(root, "inspect", "package.toml"), "schema_version=1\nid='inspect'\nname='Inspector'\n[mcp]\nname='inspect'\n")
	put(t, filepath.Join(root, "instances", "inspect", "prod.toml"), "[inputs]\nurl='https://grafana.example.test'\n")
	put(t, filepath.Join(root, "aact.toml"), `schema_version=1
pack_id="team"
[[catalog]]
id="inspect"
source="./inspect"
[[profiles]]
name="bad"
[[profiles.members]]
capability="missing"
instance="prod"
[[profiles]]
name="good"
[[profiles.members]]
capability="inspect"
instance="prod"
`)

	pack, err := DiscoverPack(root, "", "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(pack.GroupProfiles) != 2 || pack.GroupProfiles[0].Error != "" || len(pack.GroupProfiles[0].Members) != 1 || pack.GroupProfiles[1].Error != "" {
		t.Fatalf("missing member should remain visible without blocking valid members: %#v", pack.GroupProfiles)
	}
}
