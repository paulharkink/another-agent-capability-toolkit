package integration_test

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestArchiveContracts(t *testing.T) {
	dir := os.Getenv("AACT_ARCHIVE_DIR")
	if dir == "" {
		t.Skip("set AACT_ARCHIVE_DIR to a completed GoReleaser snapshot")
	}
	archives, _ := filepath.Glob(filepath.Join(dir, "aact_*.tar.gz"))
	windows, _ := filepath.Glob(filepath.Join(dir, "aact_*.zip"))
	archives = append(archives, windows...)
	if len(archives) != 6 {
		t.Fatalf("expected six OS/arch archives, found %d in %s", len(archives), dir)
	}
	checksums, err := os.ReadFile(filepath.Join(dir, "checksums.txt"))
	if err != nil {
		t.Fatal(err)
	}
	nativeRoot := t.TempDir()
	nativeFound := false
	for _, path := range archives {
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			digest := sha256.Sum256(data)
			if !strings.Contains(string(checksums), hex.EncodeToString(digest[:])+"  "+filepath.Base(path)) {
				t.Fatal("published archive checksum mismatch")
			}
			sizes := map[string]int64{}
			native := strings.Contains(filepath.Base(path), "_"+runtime.GOOS+"_"+runtime.GOARCH+".")
			extract := func(name string, mode os.FileMode, r io.Reader) error {
				name = strings.TrimPrefix(name, "./")
				if name == "" || strings.HasSuffix(name, "/") {
					return nil
				}
				clean := filepath.Clean(filepath.FromSlash(name))
				if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
					return fmt.Errorf("unsafe archive entry %q", name)
				}
				if native {
					dest := filepath.Join(nativeRoot, clean)
					if err := os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
						return err
					}
					b, err := io.ReadAll(r)
					if err != nil {
						return err
					}
					return os.WriteFile(dest, b, mode)
				}
				return nil
			}
			if strings.HasSuffix(path, ".zip") {
				z, err := zip.OpenReader(path)
				if err != nil {
					t.Fatal(err)
				}
				defer z.Close()
				for _, f := range z.File {
					sizes[strings.TrimPrefix(f.Name, "./")] = int64(f.UncompressedSize64)
					r, err := f.Open()
					if err != nil {
						t.Fatal(err)
					}
					err = extract(f.Name, f.Mode(), r)
					r.Close()
					if err != nil {
						t.Fatal(err)
					}
				}
			} else {
				f, err := os.Open(path)
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				gz, err := gzip.NewReader(f)
				if err != nil {
					t.Fatal(err)
				}
				defer gz.Close()
				tr := tar.NewReader(gz)
				for {
					h, err := tr.Next()
					if err == io.EOF {
						break
					}
					if err != nil {
						t.Fatal(err)
					}
					sizes[strings.TrimPrefix(h.Name, "./")] = h.Size
					if h.Typeflag == tar.TypeReg {
						if err := extract(h.Name, os.FileMode(h.Mode), tr); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			suffix := ""
			if strings.Contains(filepath.Base(path), "_windows_") {
				suffix = ".exe"
			}
			for _, name := range []string{"bin/aact", "bin/inspector-helper", "bin/find-session", "bin/repo-map", "packages/cluster-inspector/bin/inspector-helper", "packages/grafana-inspector/bin/inspector-helper", "packages/azure-inspector/bin/inspector-helper", "packages/find-session/bin/find-session", "packages/git-repo-map/bin/repo-map"} {
				if sizes[name+suffix] <= 0 {
					t.Errorf("missing executable %s", name+suffix)
				}
			}
			for _, name := range []string{"README.md", "LICENSE", "packages/cluster-inspector/package.toml", "packages/git-provider/package.toml"} {
				if sizes[name] <= 0 {
					t.Errorf("missing resource %s", name)
				}
			}
			nativeFound = nativeFound || native
		})
	}
	if !nativeFound {
		t.Skip("archives have no binary native to this verification host")
	}
	exe := filepath.Join(nativeRoot, "bin", "aact")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	home := t.TempDir()
	stateRoot := t.TempDir()
	cmd := exec.Command(exe, "catalog", "--state-dir", stateRoot, "--json")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "HOME="+home, "USERPROFILE="+home, "AACT_CONFIG=")
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "git-provider") {
		t.Fatalf("native unpacked archive catalog: %v %s", err, out)
	}
}
