package integration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

func TestVersionListUsesStableGitHubReleaseCatalog(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/releases" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode([]any{
			map[string]any{"tag_name": "v1.2.0-rc.1", "prerelease": true},
			map[string]any{"tag_name": "v1.1.0", "assets": []any{map[string]any{"name": "checksums.txt"}}},
			map[string]any{"tag_name": "not-a-version", "assets": []any{}},
		})
	}))
	defer server.Close()
	bin := buildVersionCommandBinary(t)
	cmd := exec.Command(bin, "version", "list")
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "AACT_RELEASE_API_URL="+server.URL+"/releases")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("version list failed: %s: %v", output, err)
	}
	if got := string(output); !strings.Contains(got, "1.1.0") || strings.Contains(got, "rc.1") || strings.Contains(got, "not-a-version") {
		t.Fatalf("version list included non-stable release or missed stable: %q", got)
	}
}

func TestVersionInstallUsesManagedInstaller(t *testing.T) {
	archive := installerArchive(t)
	arch := runtime.GOARCH
	if runtime.GOOS == "windows" {
		arch = "arm64"
	}
	server := versionReleaseServer(t, "1.2.3", arch, archive, true)
	defer server.Close()
	bin := buildVersionCommandBinary(t)
	root := t.TempDir()
	cmd := exec.Command(bin, "version", "install", "1.2.3")
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "AACT_RELEASE_API_URL="+server.URL+"/releases", "AACT_DOWNLOAD_BASE="+server.URL+"/download", "AACT_INSTALL_DIR="+root, "AACT_INSTALLER_SKIP_USER_PATH=1")
	if runtime.GOOS == "windows" {
		cmd.Env = append(cmd.Env, "PROCESSOR_ARCHITEW6432=ARM64")
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("version install failed: %s: %v", output, err)
	}
	if !strings.Contains(string(output), "Installed AACT 1.2.3") {
		t.Fatalf("installer result was not reported: %q", output)
	}
	if _, err := os.Stat(filepath.Join(root, "releases", "1.2.3-"+runtime.GOOS+"-"+arch, "bin", func() string {
		if runtime.GOOS == "windows" {
			return "aact.exe"
		}
		return "aact"
	}())); err != nil {
		t.Fatalf("managed release missing: %v", err)
	}
}

func versionReleaseServer(t *testing.T, version, arch string, archive []byte, validChecksum bool) *httptest.Server {
	t.Helper()
	extension, installerName := "tar.gz", "install.sh"
	if runtime.GOOS == "windows" {
		extension, installerName = "zip", "install.ps1"
	}
	archiveName := fmt.Sprintf("aact_%s_%s_%s.%s", version, runtime.GOOS, arch, extension)
	installer, err := os.ReadFile("../../" + installerName)
	if err != nil {
		t.Fatal(err)
	}
	archiveSum := digestHex(archive)
	if !validChecksum {
		archiveSum = strings.Repeat("0", 64)
	}
	checksums := []byte(fmt.Sprintf("%s  %s\n", archiveSum, archiveName))
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases":
			_ = json.NewEncoder(w).Encode([]any{map[string]any{"tag_name": "v" + version, "assets": []any{
				map[string]any{"name": installerName, "browser_download_url": serverAssetURL(r, "/assets/installer"), "digest": "sha256:" + digestHex(installer)},
				map[string]any{"name": "checksums.txt", "browser_download_url": serverAssetURL(r, "/assets/checksums"), "digest": "sha256:" + digestHex(checksums)},
				map[string]any{"name": archiveName, "browser_download_url": serverAssetURL(r, "/assets/archive"), "digest": "sha256:" + digestHex(archive)},
			}}})
		case "/assets/installer":
			_, _ = w.Write(installer)
		case "/assets/checksums":
			_, _ = w.Write(checksums)
		case "/assets/archive":
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
}

func digestHex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func serverAssetURL(r *http.Request, suffix string) string {
	return "http://" + r.Host + path.Clean(suffix)
}

func TestVersionListReportsOfflineCatalogError(t *testing.T) {
	bin := buildVersionCommandBinary(t)
	cmd := exec.Command(bin, "version", "list")
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "AACT_RELEASE_API_URL=http://127.0.0.1:1/releases")
	output, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(strings.ToLower(string(output)), "release") {
		t.Fatalf("offline catalog lookup was not reported: %q, %v", output, err)
	}
}

func TestVersionInstallRejectsUnavailableRelease(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]any{map[string]any{"tag_name": "v1.1.0"}})
	}))
	defer server.Close()
	bin := buildVersionCommandBinary(t)
	cmd := exec.Command(bin, "version", "install", "1.2.3")
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "AACT_RELEASE_API_URL="+server.URL+"/releases")
	output, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "1.2.3 is unavailable") {
		t.Fatalf("unavailable release was not reported: %q, %v", output, err)
	}
}

func TestVersionInstallFromUnmanagedBinaryRequiresExplicitInstallRoot(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]any{map[string]any{"tag_name": "v1.2.3"}})
	}))
	defer server.Close()
	cmd := exec.Command(buildVersionCommandBinary(t), "version", "install", "1.2.3")
	cmd.Dir = t.TempDir()
	cmd.Env = cleanTestEnvironment(os.Environ(), "HOME="+t.TempDir(), "AACT_RELEASE_API_URL="+server.URL+"/releases")
	output, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "set AACT_INSTALL_DIR") {
		t.Fatalf("unmanaged binary did not fail clearly: %q, %v", output, err)
	}
}

func TestVersionUpgradeUsesManagedDefaultRootAndMigratesLegacyLauncher(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("legacy bin migration applies to Unix installers")
	}
	home := t.TempDir()
	root := filepath.Join(home, ".local", "share", "aact")
	version := "0.9.0"
	release := filepath.Join(root, "releases", version+"-"+runtime.GOOS+"-"+runtime.GOARCH)
	binDir := filepath.Join(release, "bin")
	if err := os.MkdirAll(binDir, 0700); err != nil {
		t.Fatal(err)
	}
	binary := buildVersionCommandBinary(t)
	oldBinary := filepath.Join(binDir, "aact")
	content, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldBinary, content, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(release, ".archive-sha256"), []byte(strings.Repeat("a", 64)), 0600); err != nil {
		t.Fatal(err)
	}
	canonical := filepath.Join(root, "bin", "aact")
	if err := os.MkdirAll(filepath.Dir(canonical), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(oldBinary, canonical); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(home, ".local", "bin", "aact")
	if err := os.MkdirAll(filepath.Dir(legacy), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(oldBinary, legacy); err != nil {
		t.Fatal(err)
	}
	server := versionReleaseServer(t, "1.2.3", runtime.GOARCH, installerArchive(t), true)
	defer server.Close()
	cmd := exec.Command(oldBinary, "version", "upgrade")
	cmd.Dir = t.TempDir()
	cmd.Env = cleanTestEnvironment(os.Environ(), "HOME="+home, "AACT_RELEASE_API_URL="+server.URL+"/releases")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("version upgrade failed: %s: %v", output, err)
	}
	legacyTarget, err := os.Readlink(legacy)
	if err != nil {
		t.Fatalf("legacy launcher was not retained: %v", err)
	}
	if legacyTarget != canonical {
		t.Fatalf("legacy launcher target = %q, want migrated canonical path %q; output=%s", legacyTarget, canonical, output)
	}
	if _, err := os.Stat(filepath.Join(root, "releases", "1.2.3-"+runtime.GOOS+"-"+runtime.GOARCH, "bin", "aact")); err != nil {
		t.Fatalf("upgraded release missing from default root: %v", err)
	}
}

func TestVersionInstallChecksumMismatchPreservesManagedInstallation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix checksum-preservation path; Windows installer has a separate CI path")
	}
	root := t.TempDir()
	version := "1.2.3"
	release := filepath.Join(root, "releases", version+"-"+runtime.GOOS+"-"+runtime.GOARCH)
	binDir := filepath.Join(release, "bin")
	if err := os.MkdirAll(binDir, 0700); err != nil {
		t.Fatal(err)
	}
	oldBinary := filepath.Join(binDir, "aact")
	if err := os.WriteFile(oldBinary, []byte("old managed executable"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(release, ".archive-sha256"), []byte(strings.Repeat("b", 64)), 0600); err != nil {
		t.Fatal(err)
	}
	canonical := filepath.Join(root, "bin", "aact")
	if err := os.MkdirAll(filepath.Dir(canonical), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(oldBinary, canonical); err != nil {
		t.Fatal(err)
	}
	server := versionReleaseServer(t, version, runtime.GOARCH, installerArchive(t), false)
	defer server.Close()
	cmd := exec.Command(buildVersionCommandBinary(t), "version", "install", version)
	cmd.Dir = t.TempDir()
	cmd.Env = cleanTestEnvironment(os.Environ(), "AACT_INSTALL_DIR="+root, "AACT_RELEASE_API_URL="+server.URL+"/releases")
	output, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "Release checksum mismatch") {
		t.Fatalf("bad checksum was not reported: %q, %v", output, err)
	}
	if got, err := os.ReadFile(oldBinary); err != nil || string(got) != "old managed executable" {
		t.Fatalf("managed executable changed: %q, %v", got, err)
	}
	if got, err := os.Readlink(canonical); err != nil || got != oldBinary {
		t.Fatalf("canonical launcher changed: %q, %v", got, err)
	}
}

func TestVersionInstallRejectsMismatchedReleaseDigestBeforeActivation(t *testing.T) {
	installerName := "install.sh"
	if runtime.GOOS == "windows" {
		installerName = "install.ps1"
	}
	archiveName := fmt.Sprintf("aact_1.2.3_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		archiveName = fmt.Sprintf("aact_1.2.3_windows_%s.zip", runtime.GOARCH)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]any{map[string]any{"tag_name": "v1.2.3", "assets": []any{
			map[string]any{"name": installerName, "browser_download_url": serverAssetURL(r, "/installer"), "digest": "sha256:" + strings.Repeat("0", 64)},
			map[string]any{"name": "checksums.txt", "browser_download_url": serverAssetURL(r, "/checksums"), "digest": "sha256:" + strings.Repeat("0", 64)},
			map[string]any{"name": archiveName, "browser_download_url": serverAssetURL(r, "/archive"), "digest": "sha256:" + strings.Repeat("0", 64)},
		}}})
	}))
	defer server.Close()
	bin := buildVersionCommandBinary(t)
	root := t.TempDir()
	sentinel := filepath.Join(root, "existing-release")
	if err := os.WriteFile(sentinel, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "version", "install", "1.2.3")
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "AACT_RELEASE_API_URL="+server.URL+"/releases", "AACT_INSTALL_DIR="+root)
	output, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "SHA-256 does not match") {
		t.Fatalf("mismatched digest was not rejected: %q, %v", output, err)
	}
	if got, readErr := os.ReadFile(sentinel); readErr != nil || string(got) != "preserve" {
		t.Fatalf("existing install sentinel changed: %q, %v", got, readErr)
	}
}

func buildVersionCommandBinary(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "aact")
	if runtime.GOOS == "windows" {
		path += ".exe"
	}
	cmd := exec.Command("go", "build", "-ldflags", "-X=github.com/paulharkink/another-agent-capability-toolkit/main/internal/cli.Version=0.9.0 -X=github.com/paulharkink/another-agent-capability-toolkit/main/internal/versioninfo.Version=0.9.0", "-o", path, "./cmd/aact")
	cmd.Dir = "../../main"
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build aact: %s: %v", output, err)
	}
	return path
}

func cleanTestEnvironment(environment []string, overrides ...string) []string {
	keys := map[string]string{}
	for _, override := range overrides {
		parts := strings.SplitN(override, "=", 2)
		keys[parts[0]] = override
	}
	for _, name := range []string{"AACT_INSTALL_DIR", "AACT_RELEASE_API_URL", "AACT_INSTALLER_SKIP_USER_PATH", "PROCESSOR_ARCHITEW6432"} {
		if _, set := keys[name]; !set {
			keys[name] = ""
		}
	}
	result := make([]string, 0, len(environment)+len(keys))
	for _, item := range environment {
		name := strings.SplitN(item, "=", 2)[0]
		if _, replaced := keys[name]; replaced {
			continue
		}
		result = append(result, item)
	}
	for _, value := range keys {
		if value != "" {
			result = append(result, value)
		}
	}
	return result
}
