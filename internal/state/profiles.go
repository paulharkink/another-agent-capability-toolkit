package state

import (
	"errors"
	"os"
	"path/filepath"
)

// InstanceSelection records the selected components and destinations for an
// instance before its MCP has been started or registered with an agent.
type InstanceSelection struct {
	ItemIDs        []string `json:"item_ids"`
	DestinationIDs []string `json:"destination_ids"`
}

// InstanceRecord keeps a configured instance visible even before runtime
// registration. Profile aliases remain for source compatibility.
type InstanceRecord struct {
	Selection *InstanceSelection `json:"selection,omitempty"`
	Key       Key                `json:"key"`
	Name      string             `json:"name,omitempty"`
	Local     bool               `json:"local,omitempty"`
}
type ProfileSelection = InstanceSelection
type ProfileRecord = InstanceRecord

type instanceLedger struct {
	Version   int              `json:"version"`
	Instances []InstanceRecord `json:"instances"`
}
type profileLedger struct {
	Version  int             `json:"version"`
	Profiles []ProfileRecord `json:"profiles"`
}

func (s *Store) instancesPath() string { return filepath.Join(s.root, "manager", "instances.json") }
func (s *Store) profilesPath() string  { return filepath.Join(s.root, "manager", "profiles.json") }

func (s *Store) loadInstances() (instanceLedger, error) {
	var saved instanceLedger
	err := readJSON(s.instancesPath(), &saved)
	if errors.Is(err, os.ErrNotExist) {
		var legacy profileLedger
		legacyErr := readJSON(s.profilesPath(), &legacy)
		if errors.Is(legacyErr, os.ErrNotExist) {
			return instanceLedger{Version: 1}, nil
		}
		if legacyErr != nil {
			return saved, legacyErr
		}
		if legacy.Version != 1 {
			return saved, errors.New("unsupported instance state version")
		}
		return instanceLedger{Version: legacy.Version, Instances: legacy.Profiles}, nil
	}
	if err != nil {
		return saved, err
	}
	if saved.Version != 1 {
		return saved, errors.New("unsupported instance state version")
	}
	return saved, nil
}

func (s *Store) Instances() ([]InstanceRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	saved, err := s.loadInstances()
	return saved.Instances, err
}

func (s *Store) RecordInstance(instance InstanceRecord) error {
	if s.readonly {
		return ErrReadOnly
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	saved, err := s.loadInstances()
	if err != nil {
		return err
	}
	for i, current := range saved.Instances {
		if current.Key == instance.Key {
			saved.Instances[i] = instance
			return WriteJSON(s.instancesPath(), saved)
		}
	}
	saved.Instances = append(saved.Instances, instance)
	return WriteJSON(s.instancesPath(), saved)
}

func (s *Store) RemoveInstance(key Key) error {
	if s.readonly {
		return ErrReadOnly
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	saved, err := s.loadInstances()
	if err != nil {
		return err
	}
	kept := saved.Instances[:0]
	for _, instance := range saved.Instances {
		if instance.Key != key {
			kept = append(kept, instance)
		}
	}
	saved.Instances = kept
	return WriteJSON(s.instancesPath(), saved)
}

func (s *Store) Profiles() ([]ProfileRecord, error)         { return s.Instances() }
func (s *Store) RecordProfile(instance ProfileRecord) error { return s.RecordInstance(instance) }
func (s *Store) RemoveProfile(key Key) error                { return s.RemoveInstance(key) }
