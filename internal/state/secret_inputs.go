package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type secretInputRecord struct {
	Version int            `json:"version"`
	Values  map[string]any `json:"values"`
}

// SecretInputs returns secret form inputs stored separately from ordinary
// answer state. The AACT-owned file lives in this instance's private
// authentication dir, alongside runtime-owned state without sharing its names.
func (s *Store) SecretInputs(k Key) (map[string]any, error) {
	path := filepath.Join(s.AuthDir(k), "aact-private-inputs.json")
	s.mu.Lock()
	defer s.mu.Unlock()
	var record secretInputRecord
	err := readJSON(path, &record)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	if record.Version != 1 {
		return nil, fmt.Errorf("unsupported private input state version %d", record.Version)
	}
	if record.Values == nil {
		record.Values = map[string]any{}
	}
	return record.Values, nil
}

// SaveSecretInputs merges the submitted secret fields into this instance's
// private input store. Omitted and blank values preserve existing credentials;
// callers clear a value explicitly through resetNames.
func (s *Store) SaveSecretInputs(k Key, submitted map[string]any, resetNames []string) error {
	if s.readonly {
		return ErrReadOnly
	}
	if _, err := s.EnsureAuthDir(k); err != nil {
		return err
	}
	path := filepath.Join(s.AuthDir(k), "aact-private-inputs.json")
	s.mu.Lock()
	defer s.mu.Unlock()
	values := map[string]any{}
	var record secretInputRecord
	err := readJSON(path, &record)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil {
		if record.Version != 1 {
			return fmt.Errorf("unsupported private input state version %d", record.Version)
		}
		for name, value := range record.Values {
			values[name] = value
		}
	}
	for name, value := range submitted {
		if blankSecretInput(value) {
			continue
		}
		values[name] = value
	}
	for _, name := range resetNames {
		delete(values, name)
	}
	if len(values) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	return WriteJSON(path, secretInputRecord{Version: 1, Values: values})
}

func blankSecretInput(value any) bool {
	if value == nil {
		return true
	}
	text, ok := value.(string)
	return ok && text == ""
}
