package state

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

type Key struct {
	Source      string `json:"source"`
	Package     string `json:"package"`
	Environment string `json:"environment"`
	Target      string `json:"target"`
}

func (k Key) ID() string {
	h := sha256.Sum256([]byte(strings.Join([]string{k.Source, k.Package, k.Environment, k.Target}, "\x00")))
	return hex.EncodeToString(h[:])
}

type Installation struct {
	Key              Key       `json:"key"`
	AgentID          string    `json:"agent_id"`
	AgentHome        string    `json:"agent_home,omitempty"`
	AgentKind        string    `json:"agent_kind,omitempty"`
	Component        string    `json:"component"`
	Destination      string    `json:"destination"`
	SourcePath       string    `json:"source_path,omitempty"`
	Mode             string    `json:"mode"`
	ReleaseID        string    `json:"release_id,omitempty"`
	Digest           string    `json:"digest,omitempty"`
	RegistrationName string    `json:"registration_name,omitempty"`
	URL              string    `json:"url,omitempty"`
	Transport        string    `json:"transport,omitempty"`
	TimeoutMS        int       `json:"timeout_ms,omitempty"`
	LastAction       string    `json:"last_action,omitempty"`
	LastActionAt     time.Time `json:"last_action_at,omitempty"`
}
type Store struct {
	root     string
	mu       sync.Mutex
	readonly bool
}

var ErrReadOnly = errors.New("state store is read-only")

type ledger struct {
	Version       int            `json:"version"`
	Installations []Installation `json:"installations"`
}
type answerRecord struct {
	Version int            `json:"version"`
	Values  map[string]any `json:"values"`
}

func DefaultRoot(goos, home string, getenv func(string) string) string {
	if override := getenv("AACT_STATE_DIR"); override != "" {
		return override
	}
	if goos == "windows" {
		base := getenv("LOCALAPPDATA")
		if base == "" {
			base = filepath.Join(home, "AppData", "Local")
		}
		return filepath.Join(base, "aact", "state")
	}
	base := getenv("XDG_STATE_HOME")
	if base == "" {
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "agent-skills")
}
func Open(root string) (*Store, error) {
	return open(root, false)
}
func OpenReadOnly(root string) (*Store, error) { return open(root, true) }
func open(root string, readonly bool) (*Store, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	if root == "" {
		root = DefaultRoot(runtime.GOOS, home, os.Getenv)
	}
	if root == "~" {
		root = home
	} else if strings.HasPrefix(root, "~/") || strings.HasPrefix(root, "~\\") {
		root = filepath.Join(home, root[2:])
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if !readonly {
		if err = os.MkdirAll(filepath.Join(root, "manager"), 0700); err != nil {
			return nil, err
		}
	}
	return &Store{root: root, readonly: readonly}, nil
}
func (s *Store) Root() string              { return s.root }
func (s *Store) KeyDir(k Key) string       { return filepath.Join(s.root, "instances", k.ID()) }
func (s *Store) GeneratedDir(k Key) string { return filepath.Join(s.root, "generated", k.ID()) }
func (s *Store) InstallationID() (string, error) {
	path := filepath.Join(s.root, "manager", "installation-id")
	read := func() (string, error) {
		b, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		id := strings.TrimSpace(string(b))
		decoded, err := hex.DecodeString(id)
		if err != nil || len(decoded) != 16 {
			return "", fmt.Errorf("invalid installation ID in %s", path)
		}
		return id, nil
	}
	if id, err := read(); !errors.Is(err, os.ErrNotExist) {
		return id, err
	}
	if s.readonly {
		return "", os.ErrNotExist
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".installation-id-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = fmt.Fprintln(f, hex.EncodeToString(random[:]))
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return "", err
	}
	if closeErr != nil {
		return "", closeErr
	}
	if err = os.Link(f.Name(), path); err != nil && !errors.Is(err, os.ErrExist) {
		return "", err
	}
	return read()
}
func (s *Store) Answers(k Key) (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var rec answerRecord
	err := readJSON(filepath.Join(s.root, "answers", k.ID()+".json"), &rec)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	if rec.Version != 1 {
		return nil, fmt.Errorf("unsupported answers state version %d", rec.Version)
	}
	if rec.Values == nil {
		rec.Values = map[string]any{}
	}
	return rec.Values, nil
}
func (s *Store) HasAnswers(k Key) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	info, err := os.Stat(filepath.Join(s.root, "answers", k.ID()+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("answers path for %s is not a regular file", k.ID())
	}
	return true, nil
}
func (s *Store) SaveAnswers(k Key, v map[string]any) error {
	if s.readonly {
		return ErrReadOnly
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return WriteJSON(filepath.Join(s.root, "answers", k.ID()+".json"), answerRecord{1, v})
}
func (s *Store) loadLedger() (ledger, error) {
	var l ledger
	err := readJSON(filepath.Join(s.root, "manager", "installations.json"), &l)
	if errors.Is(err, os.ErrNotExist) {
		return ledger{Version: 1}, nil
	}
	if err != nil {
		return l, err
	}
	if l.Version != 1 {
		return l, fmt.Errorf("unsupported installation state version %d", l.Version)
	}
	return l, nil
}
func (s *Store) Installations() ([]Installation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, e := s.loadLedger()
	return l.Installations, e
}
func sameInstallation(a, b Installation) bool {
	return a.Key == b.Key && a.AgentID == b.AgentID && a.Component == b.Component && a.Destination == b.Destination
}

// Record/Remove serialize writes within this Store. Multi-operation callers use
// WithLock to coordinate read-modify-write workflows across manager processes.
func (s *Store) Record(i Installation) error {
	if s.readonly {
		return ErrReadOnly
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	l, err := s.loadLedger()
	if err != nil {
		return err
	}
	found := false
	for n, v := range l.Installations {
		if sameInstallation(v, i) {
			l.Installations[n] = i
			found = true
			break
		}
	}
	if !found {
		l.Installations = append(l.Installations, i)
	}
	return WriteJSON(filepath.Join(s.root, "manager", "installations.json"), l)
}
func (s *Store) Remove(i Installation) error {
	if s.readonly {
		return ErrReadOnly
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	l, err := s.loadLedger()
	if err != nil {
		return err
	}
	out := make([]Installation, 0, len(l.Installations))
	for _, v := range l.Installations {
		if !sameInstallation(v, i) {
			out = append(out, v)
		}
	}
	l.Installations = out
	return WriteJSON(filepath.Join(s.root, "manager", "installations.json"), l)
}
func (s *Store) AuthDir(k Key) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var aliases map[string]string
	if readJSON(filepath.Join(s.root, "manager", "auth-aliases.json"), &aliases) == nil {
		if p := aliases[k.ID()]; p != "" {
			return p
		}
	}
	return filepath.Join(s.KeyDir(k), "auth")
}
func (s *Store) AdoptAuth(k Key, path string) error {
	if s.readonly {
		return ErrReadOnly
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(s.root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return errors.New("authentication adoption path must be inside state root")
	}
	aliases := map[string]string{}
	err = readJSON(filepath.Join(s.root, "manager", "auth-aliases.json"), &aliases)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for id, existing := range aliases {
		if id != k.ID() && filepath.Clean(existing) == path {
			return errors.New("authentication state is already owned by another source/target")
		}
	}
	aliases[k.ID()] = path
	return WriteJSON(filepath.Join(s.root, "manager", "auth-aliases.json"), aliases)
}
func (s *Store) WithLock(ctx context.Context, fn func() error) error {
	if s.readonly {
		return ErrReadOnly
	}
	f, err := os.OpenFile(filepath.Join(s.root, "manager", ".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	for {
		ok, e := tryLock(f)
		if e != nil {
			return e
		}
		if ok {
			defer unlock(f)
			return fn()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}
func readJSON(path string, out any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if err = d.Decode(out); err != nil {
		return fmt.Errorf("read state %s: %w", filepath.Base(path), err)
	}
	if err = d.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("read state %s: trailing or invalid JSON", filepath.Base(path))
	}
	return nil
}
func WriteJSON(path string, value any) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return WriteAtomic(path, b, 0600)
}
func WriteAtomic(path string, b []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".aact-write-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(mode); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, path)
}
