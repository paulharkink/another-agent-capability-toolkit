package release

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type compileFixture struct{ targets []string }

func (c *compileFixture) Run(ctx context.Context, args []string, cwd string, in []byte, env map[string]string, cb func([]byte)) ([]byte, error) {
	c.targets = append(c.targets, env["GOOS"]+"/"+env["GOARCH"])
	for n, a := range args {
		if a == "-o" {
			os.MkdirAll(filepath.Dir(args[n+1]), 0755)
			return nil, os.WriteFile(args[n+1], syntheticNativeHeader(env["GOOS"], env["GOARCH"]), 0755)
		}
	}
	return nil, nil
}
func sourceFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, pkg := range []string{"git-repo-map", "find-session", "cluster-inspector", "grafana-inspector", "azure-inspector", "forgejo", "non-interactive-ready-planning"} {
		d := filepath.Join(root, "packages", pkg)
		os.MkdirAll(d, 0755)
		os.WriteFile(filepath.Join(d, "package.toml"), []byte("fixture manifest"), 0644)
		os.WriteFile(filepath.Join(d, "SKILL.md.mustache"), []byte("fixture template"), 0644)
	}
	for _, name := range []string{"README.md", "LICENSE", "THIRD_PARTY_NOTICES.md"} {
		os.WriteFile(filepath.Join(root, name), []byte(name), 0644)
	}
	return root
}

type entry struct {
	data string
	mode os.FileMode
}

func readArchive(t *testing.T, path string) map[string]entry {
	t.Helper()
	out := map[string]entry{}
	if strings.HasSuffix(path, ".zip") {
		z, err := zip.OpenReader(path)
		if err != nil {
			t.Fatal(err)
		}
		defer z.Close()
		for _, f := range z.File {
			if f.FileInfo().IsDir() {
				continue
			}
			r, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			b, err := io.ReadAll(r)
			r.Close()
			if err != nil {
				t.Fatal(err)
			}
			out[f.Name] = entry{string(b), f.Mode()}
		}
		return out
	}
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
	r := tar.NewReader(gz)
	for {
		h, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		b, err := io.ReadAll(r)
		if err != nil {
			t.Fatal(err)
		}
		out[h.Name] = entry{string(b), os.FileMode(h.Mode)}
	}
	return out
}
func TestArchiveContainsAllPackagesAndNativeHelpers(t *testing.T) {
	for _, target := range []struct{ os, arch string }{{"darwin", "arm64"}, {"linux", "amd64"}, {"windows", "arm64"}} {
		t.Run(target.os, func(t *testing.T) {
			compiler := &compileFixture{}
			builder := Builder{SourceDir: sourceFixture(t), Executor: compiler, Go: "fixture-go"}
			artifact, err := builder.Build(context.Background(), "0.1.0-dev", target.os, target.arch, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if artifact.Path == "" {
				t.Fatal("missing archive")
			}
			files := readArchive(t, artifact.Path)
			ext := ""
			if target.os == "windows" {
				ext = ".exe"
			}
			for _, name := range []string{"bin/aact" + ext, "packages/git-repo-map/bin/repo-map" + ext, "packages/find-session/bin/find-session" + ext, "packages/cluster-inspector/bin/inspector-helper" + ext, "packages/grafana-inspector/bin/inspector-helper" + ext, "packages/azure-inspector/bin/inspector-helper" + ext, "packages/forgejo/bin/inspector-helper" + ext, "packages/git-repo-map/SKILL.md.mustache", "packages/non-interactive-ready-planning/package.toml", "release.json", "LICENSE", "THIRD_PARTY_NOTICES.md"} {
				if _, ok := files[name]; !ok {
					t.Errorf("missing %s", name)
				}
			}
			b, _ := os.ReadFile(artifact.Path)
			sum := sha256.Sum256(b)
			if artifact.SHA256 != hex.EncodeToString(sum[:]) {
				t.Fatal("checksum does not cover archive")
			}
			if len(compiler.targets) != 4 {
				t.Fatalf("native helpers not all compiled: %v", compiler.targets)
			}
		})
	}
}
func TestExecutableBitsAndWindowsNames(t *testing.T) {
	builder := Builder{SourceDir: sourceFixture(t), Executor: &compileFixture{}, Go: "fixture-go"}
	for _, goos := range []string{"linux", "windows"} {
		artifact, err := builder.Build(context.Background(), "1.2.3", goos, "amd64", t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		for name, e := range readArchive(t, artifact.Path) {
			if strings.Contains(name, "/bin/") {
				if e.mode.Perm()&0111 == 0 {
					t.Errorf("not executable: %s", name)
				}
				if goos == "windows" && !strings.HasSuffix(name, ".exe") {
					t.Errorf("wrong Windows name %s", name)
				}
			}
		}
	}
}
func TestArchiveRejectsResourceSymlink(t *testing.T) {
	root := sourceFixture(t)
	outside := filepath.Join(t.TempDir(), "secret")
	os.WriteFile(outside, []byte("secret"), 0600)
	if err := os.Symlink(outside, filepath.Join(root, "packages", "forgejo", "escape")); err != nil {
		t.Skip(err)
	}
	b := Builder{SourceDir: root, Executor: &compileFixture{}}
	if _, err := b.Build(context.Background(), "1.2.3", "linux", "amd64", t.TempDir()); err == nil {
		t.Fatal("archive followed resource outside source")
	}
}
func TestInvalidTargetAndVersionRejected(t *testing.T) {
	b := Builder{SourceDir: sourceFixture(t), Executor: &compileFixture{}}
	for _, tc := range []struct{ v, os, arch string }{{"../escape", "linux", "amd64"}, {"..", "linux", "amd64"}, {"1", "plan9", "amd64"}, {"1", "linux", "386"}} {
		if _, err := b.Build(context.Background(), tc.v, tc.os, tc.arch, t.TempDir()); err == nil {
			t.Fatalf("accepted %v", tc)
		}
	}
}

type wrongArchitectureFixture struct{}

func (wrongArchitectureFixture) Run(ctx context.Context, args []string, cwd string, in []byte, env map[string]string, cb func([]byte)) ([]byte, error) {
	env["GOARCH"] = "amd64"
	return (&compileFixture{}).Run(ctx, args, cwd, in, env, cb)
}
func TestArchiveVerifiesAllNativeTargetHeaders(t *testing.T) {
	for _, goos := range []string{"darwin", "linux", "windows"} {
		for _, arch := range []string{"amd64", "arm64"} {
			b := Builder{SourceDir: sourceFixture(t), Executor: &compileFixture{}}
			artifact, err := b.Build(context.Background(), "1.2.3", goos, arch, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if err = Verify(artifact.Path, goos, arch); err != nil {
				t.Fatalf("%s/%s: %v", goos, arch, err)
			}
		}
	}
}
func TestVerifyRejectsWrongNativeArchitecture(t *testing.T) {
	b := Builder{SourceDir: sourceFixture(t), Executor: wrongArchitectureFixture{}}
	artifact, err := b.Build(context.Background(), "1.2.3", "linux", "arm64", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = Verify(artifact.Path, "linux", "arm64"); err == nil {
		t.Fatal("amd64 binaries accepted in arm64 archive")
	}
}

// Literal synthetic headers model the documented ELF, Mach-O and PE target
// identifiers. The actual six-target build is independently verified later.
func syntheticNativeHeader(goos, arch string) []byte {
	b := make([]byte, 128)
	switch goos {
	case "linux":
		copy(b, []byte{0x7f, 'E', 'L', 'F', 2, 1})
		if arch == "arm64" {
			b[18] = 183
		} else {
			b[18] = 62
		}
	case "darwin":
		copy(b, []byte{0xcf, 0xfa, 0xed, 0xfe, 7, 0, 0, 1})
		if arch == "arm64" {
			b[4] = 12
		}
	case "windows":
		copy(b, []byte("MZ"))
		b[60] = 64
		copy(b[64:], []byte{'P', 'E', 0, 0, 0x64, 0x86})
		if arch == "arm64" {
			b[68] = 0x64
			b[69] = 0xaa
		}
	}
	return b
}

func TestVerifyRejectsChecksumMismatch(t *testing.T) {
	b := Builder{SourceDir: sourceFixture(t), Executor: &compileFixture{}}
	artifact, err := b.Build(context.Background(), "1.2.3", "linux", "amd64", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(artifact.Path+".sha256", []byte(strings.Repeat("0", 64)+"  archive\n"), 0644)
	if err = Verify(artifact.Path, "linux", "amd64"); err == nil {
		t.Fatal("archive checksum mismatch accepted")
	}
}

func TestNativeArchiveModesIndependentOfHostPermissions(t *testing.T) {
	for _, isZip := range []bool{false, true} {
		root := t.TempDir()
		name := "bin/aact"
		if isZip {
			name += ".exe"
		}
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("native fixture"), 0644); err != nil {
			t.Fatal(err)
		}
		archive := filepath.Join(t.TempDir(), "release.tar.gz")
		if isZip {
			archive = filepath.Join(t.TempDir(), "release.zip")
		}
		if err := archiveTree(context.Background(), root, archive, isZip); err != nil {
			t.Fatal(err)
		}
		if got := readArchive(t, archive)[name].mode.Perm(); got != 0755 {
			t.Errorf("native mode depends on host: %o", got)
		}
	}
}
