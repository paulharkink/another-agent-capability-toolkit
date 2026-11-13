package state

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
)

const secretInputsVaultKey = "aact-private-inputs-v1"

// MigrateLegacySecretInputs moves all existing form-secret records into the
// attached vault. Each plaintext source is removed only after its encrypted
// copy has been stored successfully.
func (s *Store) MigrateLegacySecretInputs() error {
	if s.readonly {
		return ErrReadOnly
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.secretBackend == nil {
		return errors.New("encrypted secret backend is not attached")
	}
	paths := map[string]string{}
	instancesRoot := filepath.Join(s.root, "instances")
	instances, err := os.ReadDir(instancesRoot)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, instance := range instances {
		if !instance.IsDir() || !validSecretNamespace(instance.Name()) {
			continue
		}
		paths[instance.Name()] = filepath.Join(instancesRoot, instance.Name(), "auth", "aact-private-inputs.json")
	}
	var aliases map[string]string
	if err := readJSON(filepath.Join(s.root, "manager", "auth-aliases.json"), &aliases); err == nil {
		for namespace, path := range aliases {
			if validSecretNamespace(namespace) {
				paths[namespace] = filepath.Join(path, "aact-private-inputs.json")
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for namespace, path := range paths {
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("legacy private input file is not regular: %s", path)
		}
		// A prior migration may have stored the encrypted copy and failed before
		// deleting this plaintext file. Never let a stale retry overwrite the
		// vault record; when the encrypted value exists, it is authoritative.
		if _, found, err := s.secretBackend.Get(namespace, secretInputsVaultKey); err != nil {
			return err
		} else if found {
			if err := os.Remove(path); err != nil {
				return err
			}
			continue
		}
		var record secretInputRecord
		if err := readJSON(path, &record); err != nil {
			return err
		}
		if record.Version != 1 {
			return fmt.Errorf("unsupported private input state version %d", record.Version)
		}
		if record.Values == nil {
			record.Values = map[string]any{}
		}
		encoded, err := json.Marshal(record)
		if err != nil {
			return err
		}
		if err := s.secretBackend.Put(namespace, secretInputsVaultKey, encoded); err != nil {
			return err
		}
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	return nil
}

func validSecretNamespace(namespace string) bool {
	if len(namespace) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(namespace)
	return err == nil && len(decoded) == 32 && namespace == strings.ToLower(namespace)
}

type secretInputRecord struct {
	Version  int            `json:"version"`
	Revision string         `json:"revision,omitempty"`
	Values   map[string]any `json:"values"`
}

// SecretInputs returns secret form inputs stored separately from ordinary
// answer state. The AACT-owned file lives in this instance's private
// authentication dir, alongside runtime-owned state without sharing its names.
func (s *Store) SecretInputs(k Key) (map[string]any, error) {
	path := filepath.Join(s.AuthDir(k), "aact-private-inputs.json")
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.secretBackend != nil {
		return s.secretInputsFromVaultLocked(k, path)
	}
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

// SecretInputsRevision returns an opaque, non-secret revision that changes
// when the saved private input values change. Runtime specs can use it to
// restart consumers after credential rotation without exposing credentials or
// credential hashes in Docker configuration.
func (s *Store) SecretInputsRevision(k Key) (string, error) {
	path := filepath.Join(s.AuthDir(k), "aact-private-inputs.json")
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.secretInputsRevisionLocked(k, path)
}

func (s *Store) secretInputsRevisionLocked(k Key, path string) (string, error) {
	var record secretInputRecord
	if s.secretBackend != nil {
		encoded, found, err := s.secretBackend.Get(k.ID(), secretInputsVaultKey)
		if err != nil {
			return "", err
		}
		if found {
			if err := json.Unmarshal(encoded, &record); err != nil {
				return "", fmt.Errorf("decode encrypted private input state: %w", err)
			}
		} else {
			if err := readJSON(path, &record); err != nil && !errors.Is(err, os.ErrNotExist) {
				return "", err
			}
		}
	} else if err := readJSON(path, &record); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if record.Version != 0 && record.Version != 1 {
		return "", fmt.Errorf("unsupported private input state version %d", record.Version)
	}
	return record.Revision, nil
}

func newSecretInputsRevision() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("create private input revision: %w", err)
	}
	return hex.EncodeToString(value), nil
}

func copySecretInputValues(values map[string]any) map[string]any {
	copy := make(map[string]any, len(values))
	for key, value := range values {
		copy[key] = value
	}
	return copy
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
	if s.secretBackend != nil {
		revision, err := s.secretInputsRevisionLocked(k, path)
		if err != nil {
			return err
		}
		values, err := s.secretInputsFromVaultLocked(k, path)
		if err != nil {
			return err
		}
		original := copySecretInputValues(values)
		for name, value := range submitted {
			if !blankSecretInput(value) {
				values[name] = value
			}
		}
		for _, name := range resetNames {
			delete(values, name)
		}
		if len(values) == 0 {
			if err := s.secretBackend.Delete(k.ID(), secretInputsVaultKey); err != nil {
				return err
			}
		} else {
			if revision == "" || !reflect.DeepEqual(original, values) {
				revision, err = newSecretInputsRevision()
				if err != nil {
					return err
				}
			}
			encoded, err := json.Marshal(secretInputRecord{Version: 1, Revision: revision, Values: values})
			if err != nil {
				return err
			}
			if err := s.secretBackend.Put(k.ID(), secretInputsVaultKey, encoded); err != nil {
				return err
			}
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
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
	original := copySecretInputValues(values)
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
	if record.Revision == "" || !reflect.DeepEqual(original, values) {
		record.Revision, err = newSecretInputsRevision()
		if err != nil {
			return err
		}
	}
	return WriteJSON(path, secretInputRecord{Version: 1, Revision: record.Revision, Values: values})
}

func (s *Store) secretInputsFromVaultLocked(k Key, legacyPath string) (map[string]any, error) {
	encoded, found, err := s.secretBackend.Get(k.ID(), secretInputsVaultKey)
	if err != nil {
		return nil, err
	}
	if found {
		var record secretInputRecord
		if err := json.Unmarshal(encoded, &record); err != nil {
			return nil, fmt.Errorf("decode encrypted private input state: %w", err)
		}
		if record.Version != 1 {
			return nil, fmt.Errorf("unsupported private input state version %d", record.Version)
		}
		if record.Values == nil {
			record.Values = map[string]any{}
		}
		if err := os.Remove(legacyPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		return record.Values, nil
	}
	var legacy secretInputRecord
	err = readJSON(legacyPath, &legacy)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	if legacy.Version != 1 {
		return nil, fmt.Errorf("unsupported private input state version %d", legacy.Version)
	}
	if legacy.Values == nil {
		legacy.Values = map[string]any{}
	}
	encoded, err = json.Marshal(legacy)
	if err != nil {
		return nil, err
	}
	if err := s.secretBackend.Put(k.ID(), secretInputsVaultKey, encoded); err != nil {
		return nil, err
	}
	if err := os.Remove(legacyPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return legacy.Values, nil
}

func blankSecretInput(value any) bool {
	if value == nil {
		return true
	}
	text, ok := value.(string)
	return ok && text == ""
}
