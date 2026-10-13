package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProfileAuthStateDirectoryIsPrivateAndIsolated(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	first := Key{Source: "company", Package: "connector", Target: "east", Profile: "sso"}
	second := Key{Source: "company", Package: "connector", Target: "east", Profile: "token"}
	firstExistingDir := store.AuthDir(first)
	if err := os.MkdirAll(firstExistingDir, 0755); err != nil {
		t.Fatal(err)
	}
	firstDir, err := store.EnsureAuthDir(first)
	if err != nil {
		t.Fatal(err)
	}
	secondDir, err := store.EnsureAuthDir(second)
	if err != nil {
		t.Fatal(err)
	}
	if firstDir == secondDir {
		t.Fatalf("distinct profiles share auth state directory %q", firstDir)
	}
	info, err := os.Stat(firstDir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0700 {
		t.Fatalf("auth state directory permissions = %04o, want 0700", info.Mode().Perm())
	}
	if err := os.WriteFile(filepath.Join(firstDir, "session"), []byte("persisted"), 0600); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	reopenedDir, err := reopened.EnsureAuthDir(first)
	if err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(filepath.Join(reopenedDir, "session"))
	if err != nil || string(contents) != "persisted" {
		t.Fatalf("auth state not preserved across store recreation: %q, %v", contents, err)
	}
}

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

func TestRemoveProfileClearsOnlyThatProfile(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	github := ProfileRecord{Key: Key{Source: "company", Package: "git-provider", Environment: "sample-env", Target: "work", Profile: "github"}}
	bitbucket := ProfileRecord{Key: Key{Source: "company", Package: "git-provider", Environment: "sample-env", Target: "work", Profile: "bitbucket"}}
	if err := store.RecordProfile(github); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordProfile(bitbucket); err != nil {
		t.Fatal(err)
	}
	if err := store.RemoveProfile(github.Key); err != nil {
		t.Fatal(err)
	}
	profiles, err := store.Profiles()
	if err != nil || len(profiles) != 1 || profiles[0].Key != bitbucket.Key {
		t.Fatalf("wrong profile records after removal: %#v, %v", profiles, err)
	}
}
