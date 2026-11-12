package state

import (
	"reflect"
	"testing"
)

func TestGroupProfilesPersistEmptyAndEditedMembershipSeparatelyFromInstances(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	instance := InstanceRecord{Key: Key{Source: "pack", Package: "grafana", Target: "prod"}, Name: "Production"}
	if err := store.RecordInstance(instance); err != nil {
		t.Fatal(err)
	}

	profile := GroupProfile{PackID: "pack", Name: "empty"}
	if err := store.SaveGroupProfile(profile); err != nil {
		t.Fatal(err)
	}
	profiles, err := store.GroupProfiles("pack")
	if err != nil || len(profiles) != 1 || profiles[0].Name != "empty" || len(profiles[0].Members) != 0 {
		t.Fatalf("empty profile was not persisted: %#v, %v", profiles, err)
	}

	profile.Name = "team"
	profile.Members = []GroupProfileMember{{CapabilityID: "grafana", InstanceName: "prod"}, {CapabilityID: "argocd", InstanceName: "staging"}}
	if err := store.SaveGroupProfile(profile); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(store.Root())
	if err != nil {
		t.Fatal(err)
	}
	profiles, err = reopened.GroupProfiles("pack")
	want := []GroupProfile{{PackID: "pack", Name: "empty", Members: []GroupProfileMember{}}, profile}
	if err != nil || !reflect.DeepEqual(profiles, want) {
		t.Fatalf("edited memberships did not survive reopen: got %#v, want %#v (err %v)", profiles, want, err)
	}
	instances, err := reopened.Instances()
	if err != nil || len(instances) != 1 || instances[0] != instance {
		t.Fatalf("group profile edits changed instance records: %#v, %v", instances, err)
	}
}

func TestGroupProfileStorageValidatesIdentityAndMembers(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range []GroupProfile{
		{PackID: "pack", Name: "../escape"},
		{PackID: "", Name: "team"},
		{PackID: "pack", Name: "team", Members: []GroupProfileMember{{CapabilityID: "", InstanceName: "prod"}}},
		{PackID: "pack", Name: "team", Members: []GroupProfileMember{{CapabilityID: "grafana", InstanceName: "../prod"}}},
	} {
		if err := store.SaveGroupProfile(profile); err == nil {
			t.Errorf("accepted invalid group profile: %#v", profile)
		}
	}
	duplicate := GroupProfile{PackID: "pack", Name: "team", Members: []GroupProfileMember{{CapabilityID: "grafana", InstanceName: "prod"}, {CapabilityID: "grafana", InstanceName: "prod"}}}
	if err := store.SaveGroupProfile(duplicate); err == nil {
		t.Fatal("accepted duplicate member reference")
	}
}

func TestGroupProfileNamesCanContainSpacesAndUnicode(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	profile := GroupProfile{PackID: "pack", Name: "Équipe de production"}
	if err := store.SaveGroupProfile(profile); err != nil {
		t.Fatalf("valid display name rejected: %v", err)
	}
	if err := store.RemoveGroupProfile("pack", profile.Name); err != nil {
		t.Fatalf("valid display name could not be removed: %v", err)
	}
}

func TestRemovingGroupProfileDoesNotRemoveCapabilityInstances(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	instance := InstanceRecord{Key: Key{Source: "pack", Package: "grafana", Target: "prod"}}
	if err := store.RecordInstance(instance); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveGroupProfile(GroupProfile{PackID: "pack", Name: "team", Members: []GroupProfileMember{{CapabilityID: "grafana", InstanceName: "prod"}}}); err != nil {
		t.Fatal(err)
	}
	if err := store.RemoveGroupProfile("pack", "team"); err != nil {
		t.Fatal(err)
	}
	instances, err := store.Instances()
	if err != nil || len(instances) != 1 || instances[0] != instance {
		t.Fatalf("removing group profile changed capability instances: %#v, %v", instances, err)
	}
}
