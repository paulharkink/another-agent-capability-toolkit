package integration

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInstallerNativeHelper(t *testing.T) {
	if os.Getenv("AACT_INSTALLER_HELPER") != "1" {
		return
	}
	fmt.Print("native fixture ready\n")
	os.Exit(0)
}
func installerArchive(t *testing.T) []byte {
	t.Helper()
	native, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if runtime.GOOS == "windows" {
		z := zip.NewWriter(&b)
		for _, f := range []struct {
			name string
			data []byte
		}{{"bin/aact.exe", native}, {"packages/sample/SKILL.md", []byte("fixture skill")}, {"release.json", []byte(`{"version":"fixture"}`)}} {
			h := &zip.FileHeader{Name: f.name, Method: zip.Deflate}
			h.SetMode(0755)
			w, err := z.CreateHeader(h)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = w.Write(f.data); err != nil {
				t.Fatal(err)
			}
		}
		z.Close()
		return b.Bytes()
	}
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	for _, f := range []struct {
		name string
		data []byte
		mode int64
	}{{"bin/aact", native, 0755}, {"packages/sample/SKILL.md", []byte("fixture skill"), 0644}, {"release.json", []byte(`{"version":"fixture"}`), 0644}} {
		if err = tw.WriteHeader(&tar.Header{Name: f.name, Mode: f.mode, Size: int64(len(f.data))}); err != nil {
			t.Fatal(err)
		}
		if _, err = tw.Write(f.data); err != nil {
			t.Fatal(err)
		}
	}
	tw.Close()
	gz.Close()
	return b.Bytes()
}
func fixtureServer(t *testing.T, corrupt bool) *httptest.Server {
	t.Helper()
	archive := installerArchive(t)
	sum := sha256.Sum256(archive)
	hash := hex.EncodeToString(sum[:])
	if corrupt {
		hash = strings.Repeat("0", 64)
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/checksums.txt") {
			version := strings.TrimPrefix(path.Base(path.Dir(r.URL.Path)), "v")
			extension := "tar.gz"
			if installerPlatform() == "windows" {
				extension = "zip"
			}
			filename := fmt.Sprintf("aact_%s_%s_amd64.%s", version, installerPlatform(), extension)
			fmt.Fprintf(w, "%s  %s\n", hash, filename)
			return
		}
		if strings.HasSuffix(r.URL.Path, ".tar.gz") || strings.HasSuffix(r.URL.Path, ".zip") {
			w.Write(archive)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(s.Close)
	return s
}
func runInstaller(t *testing.T, root, version, base string) (string, error) {
	t.Helper()
	cmd := exec.Command("/bin/sh", "../../install.sh", "--version", version, "--install-dir", root, "--base-url", base)
	if runtime.GOOS == "windows" {
		cmd = exec.Command(powerShell(t), "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", "../../install.ps1", "-Version", version, "-InstallDir", root, "-BaseUrl", base)
	}
	cmd.Env = append(os.Environ(), "AACT_OS=linux", "AACT_ARCH=amd64", "HOME="+t.TempDir())
	out, err := cmd.CombinedOutput()
	return string(out), err
}
func TestInstallerVersionAndArchitectureMapping(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell mapper")
	}
	for _, tc := range []struct{ os, arch, want string }{{"Darwin", "arm64", "darwin_arm64"}, {"Linux", "x86_64", "linux_amd64"}, {"Linux", "aarch64", "linux_arm64"}, {"MINGW64_NT-10.0", "AMD64", "windows_amd64"}, {"MSYS_NT-10.0", "ARM64", "windows_arm64"}} {
		cmd := exec.Command("/bin/sh", "../../install.sh", "--print-target")
		cmd.Env = append(os.Environ(), "AACT_OS="+tc.os, "AACT_ARCH="+tc.arch)
		out, err := cmd.CombinedOutput()
		if err != nil || strings.TrimSpace(string(out)) != tc.want {
			t.Errorf("%v: output %q err %v", tc, out, err)
		}
	}
}
func TestChecksumMismatchPreservesInstalledRelease(t *testing.T) {
	root := t.TempDir()
	good := fixtureServer(t, false)
	if out, err := runInstaller(t, root, "1.2.3", good.URL); err != nil {
		t.Fatalf("%s: %v", out, err)
	}
	launcher := filepath.Join(root, "bin", "aact")
	before, err := launcherContents(launcher)
	if err != nil {
		t.Fatal(err)
	}
	bad := fixtureServer(t, true)
	out, err := runInstaller(t, root, "1.2.4", bad.URL)
	if err == nil || !strings.Contains(strings.ToLower(out), "checksum") {
		t.Fatalf("bad checksum accepted: %q %v", out, err)
	}
	after, _ := launcherContents(launcher)
	if before != after {
		t.Fatal("launcher changed after failed verification")
	}
	if _, err = os.Stat(filepath.Join(root, "releases", "1.2.4-"+installerPlatform()+"-amd64")); !os.IsNotExist(err) {
		t.Fatal("unverified release published")
	}
}
func TestUpdateKeepsReferencedResources(t *testing.T) {
	root := t.TempDir()
	s := fixtureServer(t, false)
	if out, err := runInstaller(t, root, "1.2.3", s.URL); err != nil {
		t.Fatalf("%s: %v", out, err)
	}
	old, _ := launcherContents(filepath.Join(root, "bin", "aact"))
	if out, err := runInstaller(t, root, "1.2.4", s.URL); err != nil {
		t.Fatalf("%s: %v", out, err)
	}
	current, _ := launcherContents(filepath.Join(root, "bin", "aact"))
	if current == old {
		t.Fatal("launcher not updated")
	}
	if _, err := os.Stat(filepath.Join(root, "releases", "1.2.3-"+installerPlatform()+"-amd64", "packages", "sample", "SKILL.md")); err != nil {
		t.Fatal("old resource removed")
	}
	if _, err := os.Stat(filepath.Join(root, ".profile")); !os.IsNotExist(err) {
		t.Fatal("shell profile written")
	}
}
func TestRuntimeWorksWithoutHostScriptTools(t *testing.T) {
	root := t.TempDir()
	s := fixtureServer(t, false)
	if out, err := runInstaller(t, root, "1.2.3", s.URL); err != nil {
		t.Fatalf("%s: %v", out, err)
	}
	cmd := exec.Command(filepath.Join(root, "bin", "aact"), "-test.run=^TestInstallerNativeHelper$")
	if runtime.GOOS == "windows" {
		cmd = exec.Command(os.Getenv("COMSPEC"), "/d", "/c", filepath.Join(root, "bin", "aact.cmd"), "-test.run=TestInstallerNativeHelper")
	}
	cmd.Env = append(os.Environ(), "AACT_INSTALLER_HELPER=1", "PATH=")
	out, err := cmd.CombinedOutput()
	if err != nil || string(out) != "native fixture ready\n" {
		t.Fatalf("runtime needs script tools: %q %v", out, err)
	}
}

func installerPlatform() string {
	if runtime.GOOS == "windows" {
		return "windows"
	}
	return "linux"
}
func launcherContents(path string) (string, error) {
	if runtime.GOOS == "windows" {
		b, err := os.ReadFile(path + ".cmd")
		return string(b), err
	}
	return os.Readlink(path)
}
func powerShell(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"pwsh", "powershell"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	t.Fatal("PowerShell is required for native Windows installer tests")
	return ""
}
func TestPowerShellInstallerArchitectureMapping(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("native PowerShell test runs on Windows CI")
	}
	for _, tc := range []struct{ arch, want string }{{"AMD64", "windows_amd64"}, {"ARM64", "windows_arm64"}} {
		cmd := exec.Command(powerShell(t), "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", "../../install.ps1", "-PrintTarget")
		cmd.Env = append(os.Environ(), "AACT_ARCH="+tc.arch)
		out, err := cmd.CombinedOutput()
		if err != nil || strings.TrimSpace(string(out)) != tc.want {
			t.Fatalf("%s: %q %v", tc.arch, out, err)
		}
	}
}
