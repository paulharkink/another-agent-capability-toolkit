package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func gitOrigin(t *testing.T, root, origin string) {
	put(t, filepath.Join(root, ".git", "HEAD"), "ref: refs/heads/main\n")
	put(t, filepath.Join(root, ".git", "config"), "[core]\nrepositoryformatversion = 0\nbare = false\n[remote \"origin\"]\nurl = "+origin+"\n")
}
func TestIdentitySurvivesCheckoutMove(t *testing.T) {
	a, b := checkout(t), checkout(t)
	gitOrigin(t, a, "git@example.com:team/skills.git")
	gitOrigin(t, b, "ssh://git@example.com/team/skills.git")
	for _, root := range []string{a, b} {
		put(t, filepath.Join(root, "nested", "aact.toml"), "schema_version=1\n")
	}
	state := t.TempDir()
	sa, ea := Discover(filepath.Join(a, "nested"), "", "", state)
	sb, eb := Discover(filepath.Join(b, "nested"), "", "", state)
	if ea != nil || eb != nil || sa.ID != sb.ID {
		t.Fatalf("%#v %#v %v %v", sa, sb, ea, eb)
	}
}
func TestCopiedSourceDoesNotRepointLinks(t *testing.T) {
	a, b := checkout(t), checkout(t)
	for _, root := range []string{a, b} {
		put(t, filepath.Join(root, "aact.toml"), "schema_version=1\n")
	}
	state := t.TempDir()
	sa, ea := Discover(a, "", "", state)
	again, er := Discover(a, "", "", state)
	sb, eb := Discover(b, "", "", state)
	if ea != nil || er != nil || eb != nil || sa.ID != again.ID || sa.ID == sb.ID {
		t.Fatalf("%#v %#v %#v %v %v %v", sa, again, sb, ea, er, eb)
	}
}

func TestWorktreeCommonConfigMatchesMainAndCopiedCheckoutIdentity(t *testing.T) {
	main, copied := checkout(t), checkout(t)
	gitOrigin(t, main, "git@example.test:team/toolkit.git")
	gitOrigin(t, copied, "https://example.test/team/toolkit.git")
	worktree := t.TempDir()
	gitdir := filepath.Join(main, ".git", "worktrees", "fixture")
	put(t, filepath.Join(gitdir, "commondir"), "../..\n")
	put(t, filepath.Join(worktree, ".git"), "gitdir: "+gitdir+"\n")
	state := t.TempDir()
	ids := []string{}
	for _, root := range []string{main, worktree, copied} {
		put(t, filepath.Join(root, "nested", "aact.toml"), "schema_version=1\n")
		source, err := Discover(filepath.Join(root, "nested"), "", "", state)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, source.ID)
	}
	if ids[0] != ids[1] || ids[0] != ids[2] {
		t.Fatalf("worktree identity differs from common origin: %v", ids)
	}
}

func TestConcurrentIndependentDiscoveriesRetainAllSourceIdentities(t *testing.T) {
	state := t.TempDir()
	roots := make([]string, 32)
	for i := range roots {
		roots[i] = checkout(t)
		put(t, filepath.Join(roots[i], "aact.toml"), "schema_version=1\n")
	}
	ids := make([]string, len(roots))
	errs := make([]error, len(roots))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i, root := range roots {
		wg.Add(1)
		go func(i int, root string) {
			defer wg.Done()
			<-start
			source, err := Discover(root, "", "", state)
			ids[i], errs[i] = source.ID, err
		}(i, root)
	}
	close(start)
	wg.Wait()
	for i, root := range roots {
		if errs[i] != nil {
			t.Fatal(errs[i])
		}
		again, err := Discover(root, "", "", state)
		if err != nil || again.ID != ids[i] {
			t.Fatalf("concurrent source %d identity lost: %q -> %q: %v", i, ids[i], again.ID, err)
		}
	}
}

func TestConcurrentDiscoveriesOfSamePathUseSingleStableIdentity(t *testing.T) {
	root, state := checkout(t), t.TempDir()
	put(t, filepath.Join(root, "aact.toml"), "schema_version=1\n")
	ids := make([]string, 32)
	errs := make([]error, 32)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range ids {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			source, err := Discover(root, "", "", state)
			ids[i], errs[i] = source.ID, err
		}(i)
	}
	close(start)
	wg.Wait()
	stable, err := Discover(root, "", "", state)
	if err != nil {
		t.Fatal(err)
	}
	for i, id := range ids {
		if errs[i] != nil || id != stable.ID {
			t.Fatalf("same path produced multiple IDs: %v stable=%q error=%v", ids, stable.ID, errs[i])
		}
	}
}

func TestLegacyIdentityMapRemainsReadableWithoutPreviewWrites(t *testing.T) {
	root, state := checkout(t), t.TempDir()
	manifest := filepath.Join(root, "aact.toml")
	put(t, manifest, "schema_version=1\n")
	canonical, err := filepath.EvalSymlinks(manifest)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]string{canonical: "local-legacy-fixture"})
	mapPath := filepath.Join(state, "manager", "source-identities.json")
	put(t, mapPath, string(data))
	preview, err := DiscoverPreview(root, "", "", state)
	if err != nil || preview.ID != "local-legacy-fixture" {
		t.Fatal(preview, err)
	}
	again, err := Discover(root, "", "", state)
	if err != nil || again.ID != preview.ID {
		t.Fatal(again, err)
	}
	after, err := os.ReadFile(mapPath)
	if err != nil || string(after) != string(data) {
		t.Fatal("legacy map changed", err)
	}
}

func TestDiscoveryWaitsForExclusiveIdentityRecordWriter(t *testing.T) {
	root, state := checkout(t), t.TempDir()
	manifest := filepath.Join(root, "aact.toml")
	put(t, manifest, "schema_version=1\n")
	canonical, err := filepath.EvalSymlinks(manifest)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte(canonical))
	recordPath := filepath.Join(state, "manager", "source-identities", hex.EncodeToString(hash[:])+".json")
	put(t, recordPath, "")
	written := make(chan error, 1)
	go func() {
		time.Sleep(30 * time.Millisecond)
		data, _ := json.Marshal(map[string]string{"path": canonical, "id": "local-winning-fixture"})
		written <- os.WriteFile(recordPath, data, 0600)
	}()
	source, err := Discover(root, "", "", state)
	if writeErr := <-written; writeErr != nil {
		t.Fatal(writeErr)
	}
	if err != nil || source.ID != "local-winning-fixture" {
		t.Fatalf("partially written exclusive identity ignored: %+v %v", source, err)
	}
}
