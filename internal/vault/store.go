package vault

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	keepass "github.com/tobischo/gokeepasslib/v3"
	"github.com/tobischo/gokeepasslib/v3/wrappers"
)

const (
	defaultKDFMemory     = 64 << 20
	defaultKDFIterations = 3
	defaultKDFThreads    = 2
	maxSecretNameBytes   = 1024
)

// Store keeps one unlocked KeePass database and persists each change atomically.
// Its in-memory protected fields are locked between operations.
type Store struct {
	mu sync.Mutex

	path         string
	pathMu       *sync.Mutex
	db           *keepass.Database
	daemonInfo   *DaemonInfo
	controlToken []byte
}

var vaultPathLocks sync.Map // canonical path -> *sync.Mutex

func pathLock(path string) *sync.Mutex {
	lock, _ := vaultPathLocks.LoadOrStore(path, &sync.Mutex{})
	return lock.(*sync.Mutex)
}

// Open creates or unlocks a KDBX 4.1 database at path. The password is never
// written outside the encrypted database.
func Open(path string, masterPassword []byte) (*Store, error) {
	if len(masterPassword) == 0 {
		return nil, errors.New("vault master password must not be empty")
	}
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("vault path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve vault path: %w", err)
	}
	absolute = filepath.Clean(absolute)
	pathMu := pathLock(absolute)
	pathMu.Lock()
	defer pathMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(absolute), 0700); err != nil {
		return nil, fmt.Errorf("create vault directory: %w", err)
	}
	if err := os.Chmod(filepath.Dir(absolute), 0700); err != nil {
		return nil, fmt.Errorf("secure vault directory: %w", err)
	}
	diskUnlock, err := lockVaultFile(absolute)
	if err != nil {
		return nil, err
	}
	defer diskUnlock()
	password := string(masterPassword)
	db := keepass.NewDatabase(keepass.WithDatabaseKDBXVersion41())
	db.Credentials = keepass.NewPasswordCredentials(password)

	if file, openErr := os.Open(absolute); openErr == nil {
		decodeErr := keepass.NewDecoder(file).Decode(db)
		closeErr := file.Close()
		if decodeErr != nil {
			return nil, fmt.Errorf("open KeePass vault: %w", decodeErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close KeePass vault: %w", closeErr)
		}
		if db.Header == nil || db.Header.FileHeaders == nil || db.Header.FileHeaders.KdfParameters == nil {
			return nil, errors.New("KeePass vault has no supported KDF parameters")
		}
		if err := db.UnlockProtectedEntries(); err != nil {
			return nil, fmt.Errorf("unlock KeePass vault entries: %w", err)
		}
		if err := db.LockProtectedEntries(); err != nil {
			return nil, fmt.Errorf("lock KeePass vault entries: %w", err)
		}
		if db.Content == nil || db.Content.Root == nil {
			return nil, errors.New("KeePass vault is missing its root group")
		}
		if !hasRootGroup(db.Content.Root, "AACT") {
			root := keepass.NewGroup()
			root.Name = "AACT"
			db.Content.Root.Groups = append(db.Content.Root.Groups, root)
		}
		if err := os.Chmod(absolute, 0600); err != nil {
			return nil, fmt.Errorf("secure KeePass vault file: %w", err)
		}
		return &Store{path: absolute, pathMu: pathMu, db: db}, nil
	} else if !errors.Is(openErr, os.ErrNotExist) {
		return nil, fmt.Errorf("open KeePass vault path: %w", openErr)
	}

	db.Content.Meta.DatabaseName = "AACT shared credentials"
	db.Content.Meta.MemoryProtection.ProtectPassword = wrappers.NewBoolWrapper(true)
	db.Header.FileHeaders.KdfParameters.Memory = defaultKDFMemory
	db.Header.FileHeaders.KdfParameters.Iterations = defaultKDFIterations
	db.Header.FileHeaders.KdfParameters.Parallelism = defaultKDFThreads
	root := keepass.NewGroup()
	root.Name = "AACT"
	db.Content.Root.Groups = []keepass.Group{root}
	store := &Store{path: absolute, pathMu: pathMu, db: db}
	store.mu.Lock()
	err = store.persistLocked()
	store.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("create KeePass vault: %w", err)
	}
	return store, nil
}

// Get returns a copied secret value from one capability-instance namespace.
func (s *Store) Get(namespace, name string) ([]byte, bool, error) {
	if err := validateEntry(namespace, name); err != nil {
		return nil, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pathMu.Lock()
	defer s.pathMu.Unlock()
	diskUnlock, err := lockVaultFile(s.path)
	if err != nil {
		return nil, false, err
	}
	defer diskUnlock()
	if err := s.refreshLocked(); err != nil {
		return nil, false, err
	}
	var result []byte
	found := false
	err = s.withPlaintextLocked(func() error {
		group := s.findNamespaceLocked(namespace)
		if group == nil {
			return nil
		}
		for i := range group.Entries {
			entry := &group.Entries[i]
			if entry.GetTitle() != name {
				continue
			}
			password := entry.GetPassword()
			decoded, err := base64.StdEncoding.DecodeString(password)
			if err != nil {
				return fmt.Errorf("decode KeePass entry %q: %w", name, err)
			}
			result = decoded
			found = true
			break
		}
		return nil
	})
	return result, found, err
}

// Put creates or replaces a secret in exactly one capability-instance namespace.
func (s *Store) Put(namespace, name string, value []byte) error {
	if err := validateEntry(namespace, name); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pathMu.Lock()
	defer s.pathMu.Unlock()
	diskUnlock, err := lockVaultFile(s.path)
	if err != nil {
		return err
	}
	defer diskUnlock()
	if err := s.refreshLocked(); err != nil {
		return err
	}
	backup, err := os.ReadFile(s.path)
	if err != nil {
		return fmt.Errorf("snapshot KeePass vault before update: %w", err)
	}
	err = s.withPlaintextLocked(func() error {
		group := s.findNamespaceLocked(namespace)
		if group == nil {
			root := s.rootGroupLocked()
			if root == nil {
				return errors.New("KeePass vault is missing its AACT root group")
			}
			newGroup := keepass.NewGroup()
			newGroup.Name = namespace
			root.Groups = append(root.Groups, newGroup)
			group = &root.Groups[len(root.Groups)-1]
		}
		encoded := base64.StdEncoding.EncodeToString(value)
		first := -1
		for i := range group.Entries {
			if group.Entries[i].GetTitle() == name {
				if first < 0 {
					first = i
					setEntryPassword(&group.Entries[i], encoded)
				}
			}
		}
		if first >= 0 {
			entries := group.Entries[:0]
			for i := range group.Entries {
				if i != first && group.Entries[i].GetTitle() == name {
					continue
				}
				entries = append(entries, group.Entries[i])
			}
			group.Entries = entries
			return nil
		}
		entry := keepass.NewEntry()
		entry.Values = append(entry.Values,
			keepass.ValueData{Key: "Title", Value: keepass.V{Content: name}},
			keepass.ValueData{Key: "Password", Value: keepass.V{Content: encoded, Protected: wrappers.NewBoolWrapper(true)}},
		)
		group.Entries = append(group.Entries, entry)
		return nil
	})
	if err != nil {
		return err
	}
	if err := s.persistLocked(); err != nil {
		if restoreErr := s.restoreLocked(backup); restoreErr != nil {
			return errors.Join(fmt.Errorf("persist KeePass vault: %w", err), fmt.Errorf("restore prior vault state: %w", restoreErr))
		}
		return fmt.Errorf("persist KeePass vault: %w", err)
	}
	return nil
}

// Delete removes a secret. Deleting an absent key is a no-op.
func (s *Store) Delete(namespace, name string) error {
	if err := validateEntry(namespace, name); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pathMu.Lock()
	defer s.pathMu.Unlock()
	diskUnlock, err := lockVaultFile(s.path)
	if err != nil {
		return err
	}
	defer diskUnlock()
	if err := s.refreshLocked(); err != nil {
		return err
	}
	group := s.findNamespaceLocked(namespace)
	if group == nil {
		return nil
	}
	found := false
	for i := range group.Entries {
		if group.Entries[i].GetTitle() == name {
			found = true
			break
		}
	}
	if found {
		backup, err := os.ReadFile(s.path)
		if err != nil {
			return fmt.Errorf("snapshot KeePass vault before delete: %w", err)
		}
		if err := s.db.UnlockProtectedEntries(); err != nil {
			return fmt.Errorf("unlock KeePass vault entries: %w", err)
		}
		group = s.findNamespaceLocked(namespace)
		entries := group.Entries[:0]
		for j := range group.Entries {
			if group.Entries[j].GetTitle() != name {
				entries = append(entries, group.Entries[j])
			}
		}
		group.Entries = entries
		if len(group.Entries) == 0 {
			root := s.rootGroupLocked()
			if root == nil {
				_ = s.restoreLocked(backup)
				return errors.New("KeePass vault is missing its AACT root group")
			}
			for j := range root.Groups {
				if root.Groups[j].Name == namespace {
					root.Groups = append(root.Groups[:j], root.Groups[j+1:]...)
					break
				}
			}
		}
		if err := s.db.LockProtectedEntries(); err != nil {
			_ = s.restoreLocked(backup)
			return fmt.Errorf("lock KeePass vault entries: %w", err)
		}
		if err := s.persistLocked(); err != nil {
			if restoreErr := s.restoreLocked(backup); restoreErr != nil {
				return errors.Join(fmt.Errorf("persist KeePass vault: %w", err), fmt.Errorf("restore prior vault state: %w", restoreErr))
			}
			return fmt.Errorf("persist KeePass vault: %w", err)
		}
		return nil
	}
	return nil
}

// List returns copies of all secrets in the requested namespace.
func (s *Store) List(namespace string) (map[string][]byte, error) {
	if err := validateNamespace(namespace); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pathMu.Lock()
	defer s.pathMu.Unlock()
	diskUnlock, err := lockVaultFile(s.path)
	if err != nil {
		return nil, err
	}
	defer diskUnlock()
	if err := s.refreshLocked(); err != nil {
		return nil, err
	}
	result := map[string][]byte{}
	err = s.withPlaintextLocked(func() error {
		group := s.findNamespaceLocked(namespace)
		if group == nil {
			return nil
		}
		for i := range group.Entries {
			entry := &group.Entries[i]
			if _, exists := result[entry.GetTitle()]; exists {
				continue
			}
			value, err := base64.StdEncoding.DecodeString(entry.GetPassword())
			if err != nil {
				return fmt.Errorf("decode KeePass entry %q: %w", entry.GetTitle(), err)
			}
			result[entry.GetTitle()] = value
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func validateEntry(namespace, name string) error {
	if err := validateNamespace(namespace); err != nil {
		return err
	}
	if name == "" || len(name) > maxSecretNameBytes || strings.ContainsRune(name, '\x00') {
		return errors.New("vault secret name is empty or invalid")
	}
	return nil
}

func validateNamespace(namespace string) error {
	if len(namespace) != 64 {
		return errors.New("vault namespace must be a 64-character capability-instance ID")
	}
	decoded, err := hex.DecodeString(namespace)
	if err != nil || len(decoded) != 32 {
		return errors.New("vault namespace must be a hexadecimal capability-instance ID")
	}
	if namespace != strings.ToLower(namespace) {
		return errors.New("vault namespace must use canonical lowercase hexadecimal")
	}
	return nil
}

// refreshLocked reloads the latest on-disk snapshot while the per-path lock is
// held, preventing separately opened Store handles from overwriting each other.
func (s *Store) refreshLocked() error {
	db := keepass.NewDatabase()
	db.Credentials = s.db.Credentials
	file, err := os.Open(s.path)
	if err != nil {
		return fmt.Errorf("open KeePass vault for refresh: %w", err)
	}
	decodeErr := keepass.NewDecoder(file).Decode(db)
	closeErr := file.Close()
	if decodeErr != nil {
		return fmt.Errorf("refresh KeePass vault: %w", decodeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close KeePass vault after refresh: %w", closeErr)
	}
	if err := db.UnlockProtectedEntries(); err != nil {
		return fmt.Errorf("unlock refreshed KeePass vault: %w", err)
	}
	if err := db.LockProtectedEntries(); err != nil {
		return fmt.Errorf("lock refreshed KeePass vault: %w", err)
	}
	s.db = db
	return nil
}

func (s *Store) findNamespaceLocked(namespace string) *keepass.Group {
	root := s.rootGroupLocked()
	if root == nil {
		return nil
	}
	for i := range root.Groups {
		group := &root.Groups[i]
		if group.Name == namespace {
			return group
		}
	}
	return nil
}

func (s *Store) rootGroupLocked() *keepass.Group {
	if s.db.Content == nil || s.db.Content.Root == nil {
		return nil
	}
	for i := range s.db.Content.Root.Groups {
		if s.db.Content.Root.Groups[i].Name == "AACT" {
			return &s.db.Content.Root.Groups[i]
		}
	}
	return nil
}

func hasRootGroup(root *keepass.RootData, name string) bool {
	for i := range root.Groups {
		if root.Groups[i].Name == name {
			return true
		}
	}
	return false
}

func setEntryPassword(entry *keepass.Entry, encoded string) {
	for i := range entry.Values {
		if entry.Values[i].Key == "Password" {
			entry.Values[i].Value.Content = encoded
			entry.Values[i].Value.Protected = wrappers.NewBoolWrapper(true)
			return
		}
	}
	entry.Values = append(entry.Values, keepass.ValueData{Key: "Password", Value: keepass.V{Content: encoded, Protected: wrappers.NewBoolWrapper(true)}})
}

func (s *Store) withPlaintextLocked(action func() error) (err error) {
	if err = s.db.UnlockProtectedEntries(); err != nil {
		return fmt.Errorf("unlock KeePass vault entries: %w", err)
	}
	defer func() {
		if lockErr := s.db.LockProtectedEntries(); lockErr != nil {
			err = errors.Join(err, fmt.Errorf("lock KeePass vault entries: %w", lockErr))
		}
	}()
	return action()
}

func (s *Store) persistLocked() error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create vault directory: %w", err)
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return fmt.Errorf("secure vault directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".aact-vault-*.kdbx")
	if err != nil {
		return fmt.Errorf("create temporary KeePass vault: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("secure temporary KeePass vault: %w", err)
	}
	encodeErr := keepass.NewEncoder(tmp).Encode(s.db)
	if encodeErr == nil {
		encodeErr = tmp.Sync()
	}
	closeErr := tmp.Close()
	if encodeErr != nil {
		return fmt.Errorf("encode KeePass vault: %w", encodeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close temporary KeePass vault: %w", closeErr)
	}
	if err := replaceFile(tmpPath, s.path); err != nil {
		return fmt.Errorf("replace KeePass vault: %w", err)
	}
	return nil
}

func (s *Store) restoreLocked(contents []byte) error {
	db := keepass.NewDatabase()
	db.Credentials = s.db.Credentials
	if err := keepass.NewDecoder(bytes.NewReader(contents)).Decode(db); err != nil {
		return fmt.Errorf("decode saved KeePass vault: %w", err)
	}
	if err := db.UnlockProtectedEntries(); err != nil {
		return fmt.Errorf("unlock saved KeePass vault: %w", err)
	}
	if err := db.LockProtectedEntries(); err != nil {
		return fmt.Errorf("lock saved KeePass vault: %w", err)
	}
	s.db = db
	return nil
}
