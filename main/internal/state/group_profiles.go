package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// GroupProfileMember points at one existing Capability Instance in a Pack.
// Instance records themselves remain in the existing instance ledger.
type GroupProfileMember struct {
	CapabilityID string `json:"capability_id"`
	InstanceName string `json:"instance_name"`
}

// GroupProfile stores a user's membership edits for one named Pack Profile.
// Pack-supplied names and descriptions remain in the read-only Pack manifest.
type GroupProfile struct {
	PackID  string               `json:"pack_id"`
	Name    string               `json:"name"`
	Members []GroupProfileMember `json:"members"`
}

type groupProfileLedger struct {
	Version  int            `json:"version"`
	Profiles []GroupProfile `json:"profiles"`
}

var groupProfileID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

func (s *Store) groupProfilesPath() string {
	return filepath.Join(s.root, "manager", "group-profiles.json")
}

func (s *Store) loadGroupProfiles() (groupProfileLedger, error) {
	var ledger groupProfileLedger
	data, err := os.ReadFile(s.groupProfilesPath())
	if os.IsNotExist(err) {
		return groupProfileLedger{Version: 1, Profiles: []GroupProfile{}}, nil
	}
	if err != nil {
		return ledger, err
	}
	if err := json.Unmarshal(data, &ledger); err != nil {
		return ledger, err
	}
	if ledger.Version != 1 {
		return ledger, errors.New("unsupported group profile state version")
	}
	if ledger.Profiles == nil {
		ledger.Profiles = []GroupProfile{}
	}
	return ledger, nil
}

// GroupProfiles returns the local membership overrides for one Capability Pack.
func (s *Store) GroupProfiles(packID string) ([]GroupProfile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ledger, err := s.loadGroupProfiles()
	if err != nil {
		return nil, err
	}
	profiles := make([]GroupProfile, 0)
	for _, profile := range ledger.Profiles {
		if profile.PackID != packID {
			continue
		}
		profile.Members = append([]GroupProfileMember{}, profile.Members...)
		profiles = append(profiles, profile)
	}
	sort.Slice(profiles, func(i, j int) bool { return profiles[i].Name < profiles[j].Name })
	return profiles, nil
}

// SaveGroupProfile creates or replaces a user's local membership for a Pack Profile.
func (s *Store) SaveGroupProfile(profile GroupProfile) error {
	if s.readonly {
		return ErrReadOnly
	}
	if err := validateGroupProfile(profile); err != nil {
		return err
	}
	profile.Members = append([]GroupProfileMember{}, profile.Members...)
	s.mu.Lock()
	defer s.mu.Unlock()
	ledger, err := s.loadGroupProfiles()
	if err != nil {
		return err
	}
	for i := range ledger.Profiles {
		if ledger.Profiles[i].PackID == profile.PackID && ledger.Profiles[i].Name == profile.Name {
			ledger.Profiles[i] = profile
			return WriteJSON(s.groupProfilesPath(), ledger)
		}
	}
	ledger.Profiles = append(ledger.Profiles, profile)
	return WriteJSON(s.groupProfilesPath(), ledger)
}

// RemoveGroupProfile discards a local profile or pack-profile membership override.
func (s *Store) RemoveGroupProfile(packID, name string) error {
	if s.readonly {
		return ErrReadOnly
	}
	if !groupProfileID.MatchString(packID) || !validGroupProfileName(name) {
		return errors.New("Capability Pack ID and Profile name are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ledger, err := s.loadGroupProfiles()
	if err != nil {
		return err
	}
	kept := ledger.Profiles[:0]
	for _, profile := range ledger.Profiles {
		if profile.PackID != packID || profile.Name != name {
			kept = append(kept, profile)
		}
	}
	ledger.Profiles = kept
	return WriteJSON(s.groupProfilesPath(), ledger)
}

func validateGroupProfile(profile GroupProfile) error {
	if !groupProfileID.MatchString(profile.PackID) || !validGroupProfileName(profile.Name) {
		return errors.New("Capability Pack ID is invalid or Profile name is not a valid display name")
	}
	seen := make(map[GroupProfileMember]bool, len(profile.Members))
	for _, member := range profile.Members {
		if !groupProfileID.MatchString(member.CapabilityID) || !groupProfileID.MatchString(member.InstanceName) {
			return fmt.Errorf("invalid Capability Instance reference %q/%q", member.CapabilityID, member.InstanceName)
		}
		if seen[member] {
			return fmt.Errorf("duplicate Capability Instance %s/%s", member.CapabilityID, member.InstanceName)
		}
		seen[member] = true
	}
	return nil
}

func validGroupProfileName(name string) bool {
	if name == "" || strings.TrimSpace(name) != name || len(name) > 80 || name == "." || name == ".." || strings.ContainsAny(name, `/\\`) {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
