package state

import (
	"strings"
	"testing"
)

func TestLegacyKeyReusesAnswersAndAuthDirectory(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	legacy := Key{Source: "company", Package: "guidance", Environment: "old-home", Target: "ota"}
	if err := s.RecordProfile(ProfileRecord{Key: legacy}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveAnswers(legacy, map[string]any{"enabled": false}); err != nil {
		t.Fatal(err)
	}
	k, err := s.ResolveProfileKey("company", "guidance", "ota")
	if err != nil || k.ID() != legacy.ID() || s.AuthDir(k) != s.AuthDir(legacy) {
		t.Fatalf("lost legacy identity: %#v %v", k, err)
	}
	answers, err := s.Answers(k)
	if err != nil || answers["enabled"] != false {
		t.Fatalf("answers lost: %#v %v", answers, err)
	}
}

func TestLegacyKeyChildIdentityAndAmbiguity(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	child := Key{Source: "company", Package: "guidance", Environment: "old-home", Target: "ota", Profile: "api-child"}
	if err := s.RecordProfile(ProfileRecord{Key: child}); err != nil {
		t.Fatal(err)
	}
	k, err := s.ResolveProfileKey("company", "guidance", "ota")
	if err != nil || k.Profile != "" || k.Target != "ota" || k.Environment != "old-home" {
		t.Fatalf("child repurposed: %#v %v", k, err)
	}
	records, _ := s.Profiles()
	if records[0].Key != child {
		t.Fatal("existing child key mutated")
	}
	other := Key{Source: "company", Package: "guidance", Environment: "different-home", Target: "ota"}
	if err := s.RecordProfile(ProfileRecord{Key: other}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolveProfileKey("company", "guidance", "ota"); err == nil || !strings.Contains(err.Error(), "old-home") || !strings.Contains(err.Error(), "different-home") {
		t.Fatalf("ambiguous records guessed: %v", err)
	}
}

func TestProfileKeyNewIdentityUsesExistingStorageShape(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	k, err := s.ResolveProfileKey("company", "guidance", "ota")
	if err != nil || k != (Key{Source: "company", Package: "guidance", Target: "ota"}) {
		t.Fatalf("unexpected new key: %#v %v", k, err)
	}
}
