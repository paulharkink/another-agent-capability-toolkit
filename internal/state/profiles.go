package state

import (
	"errors"
	"os"
	"path/filepath"
)

// ProfileRecord keeps a configured profile visible even before its MCP has
// been started or registered with an agent.
type ProfileSelection struct {
	ItemIDs        []string `json:"item_ids"`
	DestinationIDs []string `json:"destination_ids"`
}
type ProfileRecord struct {
	Selection *ProfileSelection `json:"selection,omitempty"`
	Key       Key               `json:"key"`
	Name      string            `json:"name,omitempty"`
	Local     bool              `json:"local,omitempty"`
}

type profileLedger struct {
	Version  int             `json:"version"`
	Profiles []ProfileRecord `json:"profiles"`
}

func (s *Store) profilesPath() string {
	return filepath.Join(s.root, "manager", "profiles.json")
}

func (s *Store) loadProfiles() (profileLedger, error) {
	var saved profileLedger
	err := readJSON(s.profilesPath(), &saved)
	if errors.Is(err, os.ErrNotExist) {
		return profileLedger{Version: 1}, nil
	}
	if err != nil {
		return saved, err
	}
	if saved.Version != 1 {
		return saved, errors.New("unsupported profile state version")
	}
	return saved, nil
}

func (s *Store) Profiles() ([]ProfileRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	saved, err := s.loadProfiles()
	return saved.Profiles, err
}

func (s *Store) RecordProfile(profile ProfileRecord) error {
	if s.readonly {
		return ErrReadOnly
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	saved, err := s.loadProfiles()
	if err != nil {
		return err
	}
	for i, current := range saved.Profiles {
		if current.Key == profile.Key {
			saved.Profiles[i] = profile
			return WriteJSON(s.profilesPath(), saved)
		}
	}
	saved.Profiles = append(saved.Profiles, profile)
	return WriteJSON(s.profilesPath(), saved)
}

func (s *Store) RemoveProfile(key Key) error {
	if s.readonly {
		return ErrReadOnly
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	saved, err := s.loadProfiles()
	if err != nil {
		return err
	}
	kept := saved.Profiles[:0]
	for _, profile := range saved.Profiles {
		if profile.Key != key {
			kept = append(kept, profile)
		}
	}
	saved.Profiles = kept
	return WriteJSON(s.profilesPath(), saved)
}
