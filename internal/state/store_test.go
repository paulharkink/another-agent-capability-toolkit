package state

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sampleKey(source string) Key {
	return Key{Source: source, Package: "cluster-inspector", Environment: "company", Target: "prod"}
}

func TestReadOnlyOpenHasNoWrites(t *testing.T) {
	root := filepath.Join(t.TempDir(), "absent")
	s, e := OpenReadOnly(root)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(root); !os.IsNotExist(e) {
		t.Fatal("read-only open created state")
	}
	if _, e = s.Answers(Key{Source: "x"}); e != nil {
		t.Fatal(e)
	}
	if e = s.SaveAnswers(Key{}, map[string]any{}); !errors.Is(e, ErrReadOnly) {
		t.Fatal(e)
	}
	if e = s.WithLock(context.Background(), func() error { t.Fatal("read-only lock callback invoked"); return nil }); !errors.Is(e, ErrReadOnly) {
		t.Fatal(e)
	}
}
func TestInstallationIDPersistsPerStateRoot(t *testing.T) {
	root := t.TempDir()
	first, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	id, err := first.InstallationID()
	if err != nil || id == "" {
		t.Fatalf("first installation ID = %q, %v", id, err)
	}
	reopened, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	again, err := reopened.InstallationID()
	if err != nil || again != id {
		t.Fatalf("reopened installation ID = %q, %v; want %q", again, err, id)
	}
	other, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	otherID, err := other.InstallationID()
	if err != nil || otherID == "" || otherID == id {
		t.Fatalf("other installation ID = %q, %v; first = %q", otherID, err, id)
	}
}

func TestReadOnlyInstallationIDDoesNotCreateIdentity(t *testing.T) {
	root := filepath.Join(t.TempDir(), "absent")
	s, err := OpenReadOnly(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.InstallationID(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing read-only identity error = %v", err)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read-only identity created state: %v", err)
	}
}
func TestSourceScopedAnswers(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SaveAnswers(sampleKey("one"), map[string]any{"enabled": true, "dirs": []string{"/a", "/b"}}); err != nil {
		t.Fatal(err)
	}
	one, err := s.Answers(sampleKey("one"))
	if err != nil || one["enabled"] != true {
		t.Fatalf("got=%v err=%v", one, err)
	}
	two, err := s.Answers(sampleKey("two"))
	if err != nil || len(two) != 0 {
		t.Fatalf("other source got=%v err=%v", two, err)
	}
}
func TestUnsupportedWritePreservesPreviousAnswers(t *testing.T) {
	s, _ := Open(t.TempDir())
	key := sampleKey("one")
	if err := s.SaveAnswers(key, map[string]any{"team": "old"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveAnswers(key, map[string]any{"bad": func() {}}); err == nil {
		t.Fatal("unsupported value accepted")
	}
	got, _ := s.Answers(key)
	if got["team"] != "old" {
		t.Fatalf("got=%v", got)
	}
}
func TestInstallationsRoundTripAndRemoveOnlySelected(t *testing.T) {
	s, _ := Open(t.TempDir())
	a := Installation{Key: sampleKey("one"), AgentID: "codex", Component: "skill", Destination: "/a", SourcePath: "/source", Mode: "symlink"}
	b := a
	b.Key = sampleKey("two")
	b.Destination = "/b"
	if err := s.Record(a); err != nil {
		t.Fatal(err)
	}
	if err := s.Record(b); err != nil {
		t.Fatal(err)
	}
	if err := s.Record(a); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Installations()
	if len(got) != 2 {
		t.Fatalf("got=%v", got)
	}
	if err := s.Remove(a); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Installations()
	if len(got) != 1 || got[0].Key.Source != "two" {
		t.Fatalf("got=%v", got)
	}
}
func TestKeyCannotEscapeRoot(t *testing.T) {
	s, _ := Open(t.TempDir())
	k := sampleKey("../../foreign")
	k.Target = "../../elsewhere"
	dir := s.KeyDir(k)
	rel, err := filepath.Rel(s.Root(), dir)
	if err != nil || strings.HasPrefix(rel, "..") {
		t.Fatalf("escaped=%s", dir)
	}
}
func TestLockExcludesConcurrentStore(t *testing.T) {
	root := t.TempDir()
	one, _ := Open(root)
	two, _ := Open(root)
	held := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- one.WithLock(context.Background(), func() error { close(held); <-release; return nil })
	}()
	<-held
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err := two.WithLock(ctx, func() error { t.Error("entered second lock"); return nil })
	close(release)
	if first := <-done; first != nil {
		t.Fatal(first)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v", err)
	}
}
func TestDefaultRootsAndOverride(t *testing.T) {
	env := map[string]string{"HOME": "/home/test", "XDG_STATE_HOME": "/custom", "LOCALAPPDATA": "C:\\Users\\test\\AppData\\Local"}
	get := func(k string) string { return env[k] }
	if got := DefaultRoot("linux", "/home/test", get); got != filepath.Join("/custom", "agent-skills") {
		t.Fatal(got)
	}
	delete(env, "XDG_STATE_HOME")
	if got := DefaultRoot("darwin", "/home/test", get); got != filepath.Join("/home/test", ".local", "state", "agent-skills") {
		t.Fatal(got)
	}
	if got := DefaultRoot("windows", "/home/test", get); got != filepath.Join(env["LOCALAPPDATA"], "aact", "state") {
		t.Fatal(got)
	}
	env["AACT_STATE_DIR"] = "/override"
	if got := DefaultRoot("linux", "/home/test", get); got != "/override" {
		t.Fatal(got)
	}
}
func TestRegistryAndAuthAliasesCannotCrossSources(t *testing.T) {
	s, _ := Open(t.TempDir())
	a := sampleKey("a")
	b := sampleKey("b")
	legacy := filepath.Join(s.Root(), "cluster-inspector", "company", "prod")
	os.MkdirAll(legacy, 0700)
	if err := s.AdoptAuth(a, legacy); err != nil {
		t.Fatal(err)
	}
	if s.AuthDir(a) != legacy {
		t.Fatal("did not preserve auth path")
	}
	if err := s.AdoptAuth(b, legacy); err == nil {
		t.Fatal("cross-source credential alias accepted")
	}
	if s.AuthDir(b) == legacy {
		t.Fatal("cross-source credential reuse")
	}
}
func TestSecretValuesAreAbsentFromLedger(t *testing.T) {
	s, _ := Open(t.TempDir())
	i := Installation{Key: sampleKey("one"), AgentID: "codex", Component: "mcp", Destination: "/a", Mode: "registration"}
	if err := s.Record(i); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(s.Root(), "manager", "installations.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "SecretEnv") || strings.Contains(string(b), "token") {
		t.Fatalf("ledger has secret fields: %s", b)
	}
}
