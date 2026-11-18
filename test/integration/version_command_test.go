package integration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
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
	wrongArch := "arm64"
	if arch == "arm64" {
		wrongArch = "amd64"
	}
	server := versionReleaseServerWithInstaller(t, "1.2.3", arch, archive, true, nil)
	defer server.Close()
	root := t.TempDir()
	managedDir := filepath.Join(root, "releases", "0.9.0-"+runtime.GOOS+"-"+runtime.GOARCH, "bin")
	if err := os.MkdirAll(managedDir, 0700); err != nil {
		t.Fatal(err)
	}
	managedBinary := filepath.Join(managedDir, "aact")
	if runtime.GOOS == "windows" {
		managedBinary += ".exe"
	}
	binary, err := os.ReadFile(buildVersionCommandBinary(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(managedBinary, binary, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"install.sh", "install.ps1"} {
		data, err := os.ReadFile(filepath.Join("..", "..", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(managedBinary, "version", "install", "1.2.3")
	cmd.Dir = t.TempDir()
	wrongOS := "darwin"
	if runtime.GOOS == "darwin" {
		wrongOS = "linux"
	}
	cmd.Env = cleanTestEnvironment(os.Environ(), "HOME="+t.TempDir(), "AACT_RELEASE_API_URL="+server.URL+"/releases", "AACT_INSTALLER_SKIP_USER_PATH=1", "AACT_VERSION=9.9.9", "AACT_ARCH="+wrongArch, "AACT_OS="+wrongOS)
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

func TestWindowsVersionUpgradeUsesNativeArchitecture(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows release installer")
	}
	root := t.TempDir()
	managedDir := filepath.Join(root, "releases", "0.9.0-windows-"+runtime.GOARCH, "bin")
	if err := os.MkdirAll(managedDir, 0700); err != nil {
		t.Fatal(err)
	}
	managedBinary := filepath.Join(managedDir, "aact.exe")
	binary, err := os.ReadFile(buildVersionCommandBinary(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(managedBinary, binary, 0755); err != nil {
		t.Fatal(err)
	}
	server := versionReleaseServer(t, "1.2.3", "arm64", installerArchive(t), true)
	defer server.Close()
	cmd := exec.Command(managedBinary, "version", "upgrade")
	cmd.Dir = t.TempDir()
	cmd.Env = cleanTestEnvironment(os.Environ(), "HOME="+t.TempDir(), "AACT_RELEASE_API_URL="+server.URL+"/releases", "AACT_INSTALLER_SKIP_USER_PATH=1", "AACT_VERSION=9.9.9", "AACT_ARCH=amd64", "AACT_OS=linux", "PROCESSOR_ARCHITEW6432=ARM64")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Windows version upgrade failed: %s: %v", output, err)
	}
	if _, err := os.Stat(filepath.Join(root, "releases", "1.2.3-windows-arm64", "bin", "aact.exe")); err != nil {
		t.Fatalf("Windows ARM64 release missing: %v", err)
	}
}

func versionReleaseServer(t *testing.T, version, arch string, archive []byte, validChecksum bool) *httptest.Server {
	t.Helper()
	installerName := "install.sh"
	if runtime.GOOS == "windows" {
		installerName = "install.ps1"
	}
	installer, err := os.ReadFile("../../" + installerName)
	if err != nil {
		t.Fatal(err)
	}
	return versionReleaseServerWithInstaller(t, version, arch, archive, validChecksum, installer)
}

func versionReleaseServerWithInstaller(t *testing.T, version, arch string, archive []byte, validChecksum bool, installer []byte) *httptest.Server {
	t.Helper()
	extension := "tar.gz"
	if runtime.GOOS == "windows" {
		extension = "zip"
	}
	installerName := "install.sh"
	if runtime.GOOS == "windows" {
		installerName = "install.ps1"
	}
	archiveName := fmt.Sprintf("aact_%s_%s_%s.%s", version, runtime.GOOS, arch, extension)
	archiveSum := digestHex(archive)
	if !validChecksum {
		archiveSum = strings.Repeat("0", 64)
	}
	checksums := []byte(fmt.Sprintf("%s  %s\n", archiveSum, archiveName))
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases":
			targetAssets := []any{
				map[string]any{"name": "checksums.txt", "browser_download_url": serverAssetURL(r, "/assets/checksums"), "digest": "sha256:" + digestHex(checksums)},
				map[string]any{"name": archiveName, "browser_download_url": serverAssetURL(r, "/assets/archive"), "digest": "sha256:" + digestHex(archive)},
			}
			if installer != nil {
				targetAssets = append(targetAssets, map[string]any{"name": installerName, "browser_download_url": serverAssetURL(r, "/assets/installer"), "digest": "sha256:" + digestHex(installer)})
			}
			releases := []any{map[string]any{"tag_name": "v" + version, "assets": targetAssets}}
			_ = json.NewEncoder(w).Encode(releases)
		case "/assets/installer":
			if installer == nil {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(installer)
		case "/assets/checksums":
			_, _ = w.Write(checksums)
		case "/assets/archive":
			_, _ = w.Write(archive)
		}
	}))
}

func TestVersionInstallRejectsMissingLatestInstallerBeforeActivation(t *testing.T) {
	archiveName := fmt.Sprintf("aact_0.5.0_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		archiveName = fmt.Sprintf("aact_0.5.0_windows_%s.zip", runtime.GOARCH)
	}
	installerName := "install.sh"
	if runtime.GOOS == "windows" {
		installerName = "install.ps1"
	}
	olderInstaller, err := os.ReadFile(filepath.Join("..", "..", installerName))
	if err != nil {
		t.Fatal(err)
	}
	archive := installerArchive(t)
	checksums := []byte(fmt.Sprintf("%s  %s\n", digestHex(archive), archiveName))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases":
			_ = json.NewEncoder(w).Encode([]any{
				map[string]any{"tag_name": "v0.7.4", "assets": []any{map[string]any{"name": "notes.txt"}}},
				map[string]any{"tag_name": "v0.6.1", "assets": []any{
					map[string]any{"name": installerName, "browser_download_url": serverAssetURL(r, "/assets/older-installer"), "digest": "sha256:" + digestHex(olderInstaller)},
				}},
				map[string]any{"tag_name": "v0.5.0", "assets": []any{
					map[string]any{"name": "checksums.txt", "browser_download_url": serverAssetURL(r, "/assets/checksums"), "digest": "sha256:" + digestHex(checksums)},
					map[string]any{"name": archiveName, "browser_download_url": serverAssetURL(r, "/assets/archive"), "digest": "sha256:" + digestHex(archive)},
				}},
			})
		case "/assets/older-installer":
			_, _ = w.Write(olderInstaller)
		case "/assets/checksums":
			_, _ = w.Write(checksums)
		case "/assets/archive":
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	root := t.TempDir()
	sentinel := filepath.Join(root, "existing-install")
	if err := os.WriteFile(sentinel, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(buildVersionCommandBinary(t), "version", "install", "0.5.0")
	cmd.Dir = t.TempDir()
	cmd.Env = cleanTestEnvironment(os.Environ(), "HOME="+t.TempDir(), "AACT_INSTALL_DIR="+root, "AACT_RELEASE_API_URL="+server.URL+"/releases")
	output, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "has no "+installerName+" asset") {
		t.Fatalf("missing latest bootstrap installer was not rejected: %q, %v", output, err)
	}
	if got, readErr := os.ReadFile(sentinel); readErr != nil || string(got) != "preserve" {
		t.Fatalf("existing installation changed: %q, %v", got, readErr)
	}
}

func TestVersionInstallAvoidsProxyRequestsForVerifiedLocalArchive(t *testing.T) {
	version, arch := "1.2.3", runtime.GOARCH
	if runtime.GOOS == "windows" {
		if native := strings.ToUpper(os.Getenv("PROCESSOR_ARCHITECTURE")); native == "ARM64" || native == "AARCH64" {
			arch = "arm64"
		} else if native == "AMD64" || native == "X86_64" || native == "X64" {
			arch = "amd64"
		}
	}
	trustedArchive := installerArchiveWithMarker(t, "verified local archive")
	server := versionReleaseServer(t, version, arch, trustedArchive, true)
	defer server.Close()
	serverURL := strings.TrimPrefix(server.URL, "http://")
	archiveName := fmt.Sprintf("aact_%s_%s_%s.tar.gz", version, runtime.GOOS, arch)
	if runtime.GOOS == "windows" {
		archiveName = fmt.Sprintf("aact_%s_windows_%s.zip", version, arch)
	}
	attackerArchive := installerArchiveWithMarker(t, "unverified proxy archive")
	attackerChecksums := []byte(fmt.Sprintf("%s  %s\n", digestHex(attackerArchive), archiveName))
	var intercepted atomic.Int64
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.URL.Host
		if host == "" {
			host = r.Host
		}
		if host == serverURL {
			upstream, err := http.NewRequestWithContext(r.Context(), http.MethodGet, server.URL+r.URL.RequestURI(), nil)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			response, err := (&http.Client{Transport: transport}).Do(upstream)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			defer response.Body.Close()
			for name, values := range response.Header {
				for _, value := range values {
					w.Header().Add(name, value)
				}
			}
			w.WriteHeader(response.StatusCode)
			_, _ = io.Copy(w, response.Body)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/checksums.txt") {
			_, _ = w.Write(attackerChecksums)
			intercepted.Add(1)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/"+archiveName) {
			_, _ = w.Write(attackerArchive)
			intercepted.Add(1)
			return
		}
		http.NotFound(w, r)
	}))
	defer proxy.Close()

	root := t.TempDir()
	home := t.TempDir()
	if runtime.GOOS != "windows" {
		if err := os.WriteFile(filepath.Join(home, ".curlrc"), []byte("proxy = \""+proxy.URL+"\"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, ".wgetrc"), []byte("use_proxy = on\nhttp_proxy = "+proxy.URL+"\nhttps_proxy = "+proxy.URL+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(buildVersionCommandBinary(t), "version", "install", version)
	cmd.Dir = t.TempDir()
	environment := []string{"HOME=" + home, "AACT_INSTALL_DIR=" + root,
		"AACT_RELEASE_API_URL=" + server.URL + "/releases", "AACT_INSTALLER_SKIP_USER_PATH=1",
		"HTTP_PROXY=" + proxy.URL, "http_proxy=" + proxy.URL, "HTTPS_PROXY=" + proxy.URL,
		"https_proxy=" + proxy.URL, "ALL_PROXY=" + proxy.URL, "all_proxy=" + proxy.URL,
		"NO_PROXY=", "no_proxy="}
	if runtime.GOOS == "windows" {
		environment = append(environment, "PSModulePath="+t.TempDir())
	}
	cmd.Env = cleanTestEnvironment(os.Environ(), environment...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("version install failed: %s: %v", output, err)
	}
	if intercepted.Load() != 0 {
		t.Fatalf("proxy received %d staged-asset requests", intercepted.Load())
	}
	marker, err := os.ReadFile(filepath.Join(root, "releases", version+"-"+runtime.GOOS+"-"+arch, "packages", "sample", "SKILL.md"))
	if err != nil || string(marker) != "verified local archive" {
		t.Fatalf("installer did not consume the digest-verified local archive: marker=%q err=%v", marker, err)
	}
}

func TestVersionInstallPreservesUnrelatedEnvironmentOnUnix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix installer environment")
	}
	archive := installerArchive(t)
	realInstaller, err := filepath.Abs("../../install.sh")
	if err != nil {
		t.Fatal(err)
	}
	wrapper := []byte(`#!/bin/sh
printf '%s' "${PSModulePath-<unset>}" > "$AACT_TEST_CAPTURE_PATH"
exec /bin/sh "$AACT_TEST_INSTALLER_PATH" "$@"
`)
	server := versionReleaseServerWithInstaller(t, "1.2.3", runtime.GOARCH, archive, true, wrapper)
	defer server.Close()
	root, home := t.TempDir(), t.TempDir()
	capture := filepath.Join(t.TempDir(), "psmodulepath.txt")
	modulePath := "unrelated-module-path-value"
	cmd := exec.Command(buildVersionCommandBinary(t), "version", "install", "1.2.3")
	cmd.Dir = t.TempDir()
	cmd.Env = cleanTestEnvironment(os.Environ(), "HOME="+home, "AACT_INSTALL_DIR="+root,
		"AACT_RELEASE_API_URL="+server.URL+"/releases", "PSModulePath="+modulePath,
		"AACT_TEST_CAPTURE_PATH="+capture, "AACT_TEST_INSTALLER_PATH="+realInstaller)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("version install failed: %s: %v", output, err)
	}
	got, err := os.ReadFile(capture)
	if err != nil || string(got) != modulePath {
		t.Fatalf("installer did not receive the unrelated environment value: got %q, want %q (err=%v)", got, modulePath, err)
	}
	if _, err := os.Stat(filepath.Join(root, "releases", "1.2.3-"+runtime.GOOS+"-"+runtime.GOARCH, "bin", "aact")); err != nil {
		t.Fatalf("managed install did not complete: %v", err)
	}
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

func TestVersionInstallFromCheckoutUsesLocalInstallerForHistoricalRelease(t *testing.T) {
	archive := installerArchive(t)
	server := versionReleaseServerWithInstaller(t, "0.5.0", runtime.GOARCH, archive, true, nil)
	defer server.Close()
	root := t.TempDir()
	cmd := exec.Command(buildVersionCommandBinary(t), "version", "install", "0.5.0")
	cmd.Dir = filepath.Join("..", "..")
	cmd.Env = cleanTestEnvironment(os.Environ(), "HOME="+t.TempDir(), "AACT_INSTALL_DIR="+root, "AACT_RELEASE_API_URL="+server.URL+"/releases")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("historical release install from checkout failed: %s: %v", output, err)
	}
	installed := filepath.Join(root, "releases", "0.5.0-"+runtime.GOOS+"-"+runtime.GOARCH, "bin", "aact")
	if runtime.GOOS == "windows" {
		installed += ".exe"
	}
	if _, err := os.Stat(installed); err != nil {
		t.Fatalf("historical managed release missing at %s: %v", installed, err)
	}
}

func TestExistingManagedInstallUsesVerifiedFallbackWithoutReadingInstallerFromWorkingDirectory(t *testing.T) {
	version, arch := "1.2.3", runtime.GOARCH
	server := versionReleaseServer(t, version, arch, installerArchive(t), true)
	defer server.Close()
	home := t.TempDir()
	root := filepath.Join(home, ".local", "share", "aact")
	managedDir := filepath.Join(root, "releases", "0.9.0-"+runtime.GOOS+"-"+runtime.GOARCH, "bin")
	if err := os.MkdirAll(managedDir, 0700); err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(buildVersionCommandBinary(t))
	if err != nil {
		t.Fatal(err)
	}
	binaryName := "aact"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	managedBinary := filepath.Join(managedDir, binaryName)
	if err := os.WriteFile(managedBinary, binary, 0755); err != nil {
		t.Fatal(err)
	}
	workingDir := t.TempDir()
	marker := filepath.Join(workingDir, "untrusted-installer-ran")
	installerName, untrustedScript := "install.sh", "#!/bin/sh\nprintf malicious > \"$AACT_TEST_MARKER\"\nexit 9\n"
	if runtime.GOOS == "windows" {
		installerName, untrustedScript = "install.ps1", "Set-Content -LiteralPath $env:AACT_TEST_MARKER -Value malicious; exit 9\n"
	}
	if err := os.WriteFile(filepath.Join(workingDir, installerName), []byte(untrustedScript), 0600); err != nil {
		t.Fatal(err)
	}
	// Even a directory that resembles the checkout layout must not override the
	// installer location for a managed executable.
	if err := os.MkdirAll(filepath.Join(workingDir, "main"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workingDir, "go.mod"), []byte("module untrusted\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workingDir, "main", "go.mod"), []byte("module untrusted/main\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(managedBinary, "version", "install", version)
	cmd.Dir = workingDir
	cmd.Env = cleanTestEnvironment(os.Environ(), "HOME="+home, "AACT_RELEASE_API_URL="+server.URL+"/releases", "AACT_TEST_MARKER="+marker, "AACT_INSTALLER_SKIP_USER_PATH=1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("existing managed install did not use verified installer fallback: %s: %v", output, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("untrusted working-directory installer ran: stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "releases", version+"-"+runtime.GOOS+"-"+arch, "bin", binaryName)); err != nil {
		t.Fatalf("upgraded release missing: %v", err)
	}
}

func TestVersionInstallChecksumMismatchPreservesManagedInstallation(t *testing.T) {
	root := t.TempDir()
	version := "1.2.3"
	arch := runtime.GOARCH
	if runtime.GOOS == "windows" {
		arch = "arm64"
	}
	release := filepath.Join(root, "releases", version+"-"+runtime.GOOS+"-"+arch)
	binDir := filepath.Join(release, "bin")
	if err := os.MkdirAll(binDir, 0700); err != nil {
		t.Fatal(err)
	}
	oldBinary := filepath.Join(binDir, "aact")
	if runtime.GOOS == "windows" {
		oldBinary += ".exe"
	}
	if err := os.WriteFile(oldBinary, []byte("old managed executable"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(release, ".archive-sha256"), []byte(strings.Repeat("b", 64)), 0600); err != nil {
		t.Fatal(err)
	}
	canonical := filepath.Join(root, "bin", "aact")
	if runtime.GOOS != "windows" {
		if err := os.MkdirAll(filepath.Dir(canonical), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(oldBinary, canonical); err != nil {
			t.Fatal(err)
		}
	}
	server := versionReleaseServer(t, version, arch, installerArchive(t), false)
	defer server.Close()
	cmd := exec.Command(buildVersionCommandBinary(t), "version", "install", version)
	cmd.Dir = t.TempDir()
	cmd.Env = cleanTestEnvironment(os.Environ(), "AACT_INSTALL_DIR="+root, "AACT_RELEASE_API_URL="+server.URL+"/releases", "AACT_INSTALLER_SKIP_USER_PATH=1")
	if runtime.GOOS == "windows" {
		cmd.Env = append(cmd.Env, "PROCESSOR_ARCHITEW6432=ARM64")
	}
	output, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "Release checksum mismatch") {
		t.Fatalf("bad checksum was not reported: %q, %v", output, err)
	}
	if got, err := os.ReadFile(oldBinary); err != nil || string(got) != "old managed executable" {
		t.Fatalf("managed executable changed: %q, %v", got, err)
	}
	if runtime.GOOS != "windows" {
		if got, err := os.Readlink(canonical); err != nil || got != oldBinary {
			t.Fatalf("canonical launcher changed: %q, %v", got, err)
		}
	} else if _, err := os.Stat(filepath.Join(root, "bin", "aact.cmd")); err == nil {
		t.Fatal("checksum failure created or changed the Windows launcher")
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
	for _, name := range []string{"AACT_INSTALL_DIR", "AACT_RELEASE_API_URL", "AACT_INSTALLER_SKIP_USER_PATH", "PROCESSOR_ARCHITEW6432", "AACT_ARCH", "AACT_OS", "AACT_VERSION", "AACT_DOWNLOAD_BASE", "HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy"} {
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
