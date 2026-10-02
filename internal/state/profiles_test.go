package state

import (
	"path/filepath"
	"testing"
)

func TestProfileRecordsPersistWithoutRuntimeOrRegistration(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	want := ProfileRecord{Key: Key{Source: "company", Package: "cluster", Environment: "prod", Target: "east"}}
	if err := store.RecordProfile(want); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	profiles, err := reopened.Profiles()
	if err != nil || len(profiles) != 1 || profiles[0] != want {
		t.Fatalf("saved profile disappeared: %+v, %v", profiles, err)
	}
	installations, err := reopened.Installations()
	if err != nil || len(installations) != 0 {
		t.Fatalf("profile incorrectly required an installation: %+v, %v", installations, err)
	}
}

func TestRecordProfileUpdatesNameWithoutDuplicatingIdentity(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	key := Key{Source: "company", Package: "cluster", Target: "east"}
	for _, name := range []string{"Old name", "New name"} {
		if err := store.RecordProfile(ProfileRecord{Key: key, Name: name}); err != nil {
			t.Fatal(err)
		}
	}
	profiles, err := store.Profiles()
	if err != nil || len(profiles) != 1 || profiles[0].Name != "New name" {
		t.Fatalf("profile identity duplicated on rename: %+v, %v", profiles, err)
	}
}
