package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
)

func fixtureRepo(t *testing.T, path, remote string) string {
	t.Helper()
	r, err := git.PlainInit(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if remote != "" {
		if _, err = r.CreateRemote(&config.RemoteConfig{Name: "origin", URLs: []string{remote}}); err != nil {
			t.Fatal(err)
		}
	}
	return path
}
func expectScan(t *testing.T, roots, hosts []string, want []Repository) {
	t.Helper()
	got, err := Scan(context.Background(), roots, hosts)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v; want %#v", got, want)
	}
}

// Stopping after one root or sorting by path instead of host/name/path breaks this.
func TestSeveralRoots(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	z := fixtureRepo(t, filepath.Join(a, "z"), "git@z.example:org/apple.git")
	x := fixtureRepo(t, filepath.Join(a, "x"), "https://a.example/org/zebra.git")
	y := fixtureRepo(t, filepath.Join(b, "y"), "https://a.example/org/apple.git")
	expectScan(t, []string{a, b}, nil, []Repository{{"a.example", "org/apple", y}, {"a.example", "org/zebra", x}, {"z.example", "org/apple", z}})
}
func TestMultipleCheckoutsPreserved(t *testing.T) {
	root := t.TempDir()
	a := fixtureRepo(t, filepath.Join(root, "a"), "https://git.example/team/app.git")
	b := fixtureRepo(t, filepath.Join(root, "b"), "git@git.example:team/app.git")
	expectScan(t, []string{root, a}, nil, []Repository{{"git.example", "team/app", a}, {"git.example", "team/app", b}})
}
func TestSameCoordinatesDifferentHosts(t *testing.T) {
	root := t.TempDir()
	a := fixtureRepo(t, filepath.Join(root, "a"), "https://a.example/team/app.git")
	b := fixtureRepo(t, filepath.Join(root, "b"), "https://b.example/team/app.git")
	expectScan(t, []string{root}, nil, []Repository{{"a.example", "team/app", a}, {"b.example", "team/app", b}})
	expectScan(t, []string{root}, []string{"B.Example"}, []Repository{{"b.example", "team/app", b}})
}

// Real worktrees keep origin in the common directory, not the .git file target.
func fixtureWorktree(t *testing.T, root string) (string, string, string) {
	t.Helper()
	a := fixtureRepo(t, filepath.Join(root, "main"), "https://git.example/team/app.git")
	worktree := filepath.Join(root, "linked")
	gitdir := filepath.Join(a, ".git", "worktrees", "linked")
	for _, dir := range []string{worktree, gitdir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{filepath.Join(worktree, ".git"): "gitdir: ../main/.git/worktrees/linked\n", filepath.Join(gitdir, "commondir"): "../..\n", filepath.Join(gitdir, "HEAD"): "ref: refs/heads/main\n", filepath.Join(gitdir, "gitdir"): filepath.Join(worktree, ".git") + "\n"}
	for path, body := range files {
		if err := os.WriteFile(path, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return a, worktree, gitdir
}
func TestWorktreeGitFile(t *testing.T) {
	root := t.TempDir()
	a, worktree, gitdir := fixtureWorktree(t, root)
	expectScan(t, []string{root}, nil, []Repository{{"git.example", "team/app", worktree}, {"git.example", "team/app", a}})
	// Scanning must release metadata handles so Windows can remove the checkout.
	if err := os.Remove(filepath.Join(gitdir, "commondir")); err != nil {
		t.Fatalf("scan retained commondir handle: %v", err)
	}
}

func TestWorktreeConfigExtensionStillMapsOrigin(t *testing.T) {
	root := t.TempDir()
	main, worktree, _ := fixtureWorktree(t, root)
	configPath := filepath.Join(main, ".git", "config")
	file, err := os.OpenFile(configPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("\n[extensions]\n\tworktreeConfig = true\n"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	expectScan(t, []string{root}, nil, []Repository{{"git.example", "team/app", worktree}, {"git.example", "team/app", main}})
}

func TestPrunedDependencies(t *testing.T) {
	root := t.TempDir()
	visible := fixtureRepo(t, filepath.Join(root, "app"), "https://git.example/team/app.git")
	for _, dir := range []string{"node_modules", ".cache", ".npm", ".cargo", ".rustup", ".gradle", ".venv", "vendor"} {
		fixtureRepo(t, filepath.Join(root, dir, "hidden"), "https://git.example/team/hidden.git")
	}
	expectScan(t, []string{root}, nil, []Repository{{"git.example", "team/app", visible}})
}
func TestUnreadableRootVisible(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing")
	got, err := Scan(context.Background(), []string{root}, nil)
	if err == nil || !strings.Contains(err.Error(), root) {
		t.Fatalf("got %#v %v; want error naming inaccessible root", got, err)
	}
}
func TestSymlinkCycleDoesNotLoop(t *testing.T) {
	root := t.TempDir()
	p := fixtureRepo(t, filepath.Join(root, "repo"), "https://git.example/team/app.git")
	if err := os.Symlink(root, filepath.Join(root, "cycle")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	got, err := Scan(ctx, []string{root}, nil)
	if err != nil || !reflect.DeepEqual(got, []Repository{{"git.example", "team/app", p}}) {
		t.Fatalf("got %#v %v", got, err)
	}
}
func TestScanCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Scan(ctx, []string{t.TempDir()}, nil); err == nil {
		t.Fatal("canceled scan succeeded")
	}
}
func TestNoOriginAndLocalOriginIgnored(t *testing.T) {
	root := t.TempDir()
	fixtureRepo(t, filepath.Join(root, "none"), "")
	fixtureRepo(t, filepath.Join(root, "local"), "../local")
	expectScan(t, []string{root}, nil, []Repository{})
}

func TestPermissionDeniedRootVisible(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0); err != nil {
		t.Skipf("cannot change permissions: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0700) })
	if _, err := os.ReadDir(root); err == nil {
		t.Skip("platform or elevated user bypasses permissions")
	}
	if _, err := Scan(context.Background(), []string{root}, nil); err == nil || !strings.Contains(err.Error(), root) {
		t.Fatalf("missing unreadable directory diagnostic: %v", err)
	}
}
func TestMalformedGitMetadataVisible(t *testing.T) {
	for _, name := range []string{"broken", "broken\\checkout"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			repo := filepath.Join(root, name)
			if err := os.MkdirAll(repo, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(repo, ".git"), []byte("invalid git file\n"), 0644); err != nil {
				t.Fatal(err)
			}
			if rows, err := Scan(context.Background(), []string{root}, nil); err == nil || !strings.Contains(err.Error(), strconv.Quote(repo)) || len(rows) != 0 {
				t.Fatalf("missing metadata diagnostic: %v rows=%v", err, rows)
			}
		})
	}
}

func TestMultipleOriginURLs(t *testing.T) {
	root := t.TempDir()
	p := fixtureRepo(t, filepath.Join(root, "checkout"), "")
	repo, err := git.PlainOpen(p)
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.CreateRemote(&config.RemoteConfig{Name: "origin", URLs: []string{"https://a.example/team/app.git", "git@a.example:team/app.git", "https://b.example/team/app.git"}})
	if err != nil {
		t.Fatal(err)
	}
	expectScan(t, []string{root}, nil, []Repository{{"a.example", "team/app", p}, {"b.example", "team/app", p}})
}
func TestExplicitCacheRootScanned(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".cache")
	p := fixtureRepo(t, filepath.Join(root, "checkout"), "https://git.example/team/app.git")
	expectScan(t, []string{root}, nil, []Repository{{"git.example", "team/app", p}})
}

func TestPrunedLocalRuntimeState(t *testing.T) {
	root := t.TempDir()
	fixtureRepo(t, filepath.Join(root, ".local", "share", "hidden"), "https://git.example/team/hidden.git")
	visible := fixtureRepo(t, filepath.Join(root, "project", "share", "visible"), "https://git.example/team/visible.git")
	expectScan(t, []string{root}, nil, []Repository{{"git.example", "team/visible", visible}})
}
