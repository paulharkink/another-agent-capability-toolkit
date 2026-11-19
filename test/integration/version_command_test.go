package integration

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/vault"
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
	cmd.Env = cleanTestEnvironment(t, os.Environ(), "AACT_RELEASE_API_URL="+server.URL+"/releases")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("version list failed: %s: %v", output, err)
	}
	if got := string(output); !strings.Contains(got, "1.1.0") || strings.Contains(got, "rc.1") || strings.Contains(got, "not-a-version") {
		t.Fatalf("version list included non-stable release or missed stable: %q", got)
	}
}

func TestVersionInstallUsesManagedInstaller(t *testing.T) {
	arch := runtime.GOARCH
	if runtime.GOOS == "windows" {
		arch = "arm64"
	}
	wrongArch := "arm64"
	if arch == "arm64" {
		wrongArch = "amd64"
	}
	targetArchive := installerArchive(t)
	server := versionReleaseServerWithInstaller(t, "1.2.3", arch, targetArchive, true, nil)
	defer server.Close()
	root := t.TempDir()
	managedDir := filepath.Join(root, "releases", "0.9.0-"+runtime.GOOS+"-"+runtime.GOARCH, "bin")
	binaryPath := buildVersionCommandBinary(t)
	binary, err := os.ReadFile(binaryPath)
	if err != nil {
		t.Fatal(err)
	}
	bundledArchive := installerArchiveWithNative(t, binary, "fixture skill")
	releaseDir := filepath.Dir(managedDir)
	extractVersionArchive(t, bundledArchive, releaseDir)
	managedBinary := filepath.Join(managedDir, "aact")
	if runtime.GOOS == "windows" {
		managedBinary += ".exe"
	}
	cmd := exec.Command(managedBinary, "version", "install", "1.2.3")
	cmd.Dir = t.TempDir()
	wrongOS := "darwin"
	if runtime.GOOS == "darwin" {
		wrongOS = "linux"
	}
	cmd.Env = cleanTestEnvironment(t, os.Environ(), "AACT_RELEASE_API_URL="+server.URL+"/releases", "AACT_INSTALLER_SKIP_USER_PATH=1", "AACT_VERSION=9.9.9", "AACT_ARCH="+wrongArch, "AACT_OS="+wrongOS)
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

func extractVersionArchive(t *testing.T, archive []byte, destination string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range reader.File {
			path := filepath.Join(destination, filepath.FromSlash(entry.Name))
			if entry.FileInfo().IsDir() {
				if err := os.MkdirAll(path, 0700); err != nil {
					t.Fatal(err)
				}
				continue
			}
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			input, err := entry.Open()
			if err != nil {
				t.Fatal(err)
			}
			output, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, entry.Mode().Perm())
			if err != nil {
				input.Close()
				t.Fatal(err)
			}
			_, copyErr := io.Copy(output, input)
			closeInputErr, closeOutputErr := input.Close(), output.Close()
			if copyErr != nil || closeInputErr != nil || closeOutputErr != nil {
				t.Fatalf("extract %s: copy=%v close input=%v close output=%v", entry.Name, copyErr, closeInputErr, closeOutputErr)
			}
		}
		return
	}
	compressed, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatal(err)
	}
	defer compressed.Close()
	reader := tar.NewReader(compressed)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(destination, filepath.FromSlash(header.Name))
		if header.Typeflag == tar.TypeDir {
			if err := os.MkdirAll(path, 0700); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if header.Typeflag != tar.TypeReg {
			t.Fatalf("unexpected archive entry type %d for %s", header.Typeflag, header.Name)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		output, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(header.Mode).Perm())
		if err != nil {
			t.Fatal(err)
		}
		_, copyErr := io.Copy(output, reader)
		closeErr := output.Close()
		if copyErr != nil || closeErr != nil {
			t.Fatalf("extract %s: copy=%v close=%v", header.Name, copyErr, closeErr)
		}
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
	cmd.Env = cleanTestEnvironment(t, os.Environ(), "AACT_RELEASE_API_URL="+server.URL+"/releases", "AACT_INSTALLER_SKIP_USER_PATH=1", "AACT_VERSION=9.9.9", "AACT_ARCH=amd64", "AACT_OS=linux", "PROCESSOR_ARCHITEW6432=ARM64")
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
			if version == "0.5.0" && installer != nil {
				releases = append([]any{map[string]any{"tag_name": "v0.6.1", "assets": []any{map[string]any{"name": installerName, "browser_download_url": serverAssetURL(r, "/assets/compatible-installer"), "digest": "sha256:" + digestHex(installer)}}}}, releases...)
			}
			_ = json.NewEncoder(w).Encode(releases)
		case "/assets/installer":
			if installer == nil {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(installer)
		case "/assets/compatible-installer":
			_, _ = w.Write(installer)
		case "/assets/checksums":
			_, _ = w.Write(checksums)
		case "/assets/archive":
			_, _ = w.Write(archive)
		}
	}))
}

func TestVersionInstallUsesOlderCompatibleVerifiedInstaller(t *testing.T) {
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
	cmd := exec.Command(buildVersionCommandBinary(t), "version", "install", "0.5.0")
	cmd.Dir = t.TempDir()
	cmd.Env = cleanTestEnvironment(t, os.Environ(), "AACT_INSTALL_DIR="+root, "AACT_INSTALLER_SKIP_USER_PATH=1", "AACT_RELEASE_API_URL="+server.URL+"/releases")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("older compatible installer was not used: %q, %v", output, err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "releases", "0.5.0-"+runtime.GOOS+"-"+runtime.GOARCH, "bin", func() string {
		if runtime.GOOS == "windows" {
			return "aact.exe"
		}
		return "aact"
	}())); statErr != nil {
		t.Fatalf("historical release was not installed: %v", statErr)
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
	cmd.Env = cleanTestEnvironment(t, os.Environ(), environment...)
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
	cmd.Env = cleanTestEnvironment(t, os.Environ(), "HOME="+home, "AACT_INSTALL_DIR="+root,
		"AACT_RELEASE_API_URL="+server.URL+"/releases", "AACT_INSTALLER_SKIP_USER_PATH=1", "PSModulePath="+modulePath,
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
	cmd.Env = cleanTestEnvironment(t, os.Environ(), "AACT_RELEASE_API_URL=http://127.0.0.1:1/releases")
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
	cmd.Env = cleanTestEnvironment(t, os.Environ(), "AACT_RELEASE_API_URL="+server.URL+"/releases")
	output, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "1.2.3 is unavailable") {
		t.Fatalf("unavailable release was not reported: %q, %v", output, err)
	}
}

func TestVersionUpgradeStopsAndRestartsRunningDaemonUsingSavedCredential(t *testing.T) {
	const password = "isolated-version-upgrade-password"
	binary, root, oldInfo, trace, server := startVersionInstallFixture(t, password, true, nil)
	defer server.Close()
	namespace, name, secret := strings.Repeat("a", 64), "probe", "scoped value survives restart"
	store, err := vault.Open(filepath.Join(root, "credentials.kdbx"), []byte(password))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(namespace, name, []byte(secret)); err != nil {
		t.Fatal(err)
	}
	scopedAccess := newScopedVaultAccess(t, oldInfo, root, password, namespace)
	assertScopedVaultValue(t, scopedAccess, oldInfo, name, secret)
	command := exec.Command(binary, "version", "upgrade")
	command.Dir = t.TempDir()
	command.Env = versionInstallEnvironment(t, root, server.URL+"/releases")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("version install failed: %s: %v", output, err)
	}
	newInfo, err := vault.ReadDaemonInfo(root)
	if err != nil || !vault.DaemonRunning(newInfo) {
		t.Fatalf("updated daemon was not running: info=%+v err=%v output=%q", newInfo, err, output)
	}
	if newInfo.APIAddress != oldInfo.APIAddress {
		t.Fatalf("Vault API endpoint changed across restart: old=%q new=%q", oldInfo.APIAddress, newInfo.APIAddress)
	}
	assertScopedVaultValue(t, scopedAccess, newInfo, name, secret)
	if marker, err := os.ReadFile(trace); err != nil || !strings.Contains(strings.ReplaceAll(string(marker), "\r\n", "\n"), "get\n") {
		t.Fatalf("upgrade did not retrieve the saved credential: trace=%q err=%v", marker, err)
	}
	if strings.Contains(string(output), password) {
		t.Fatal("Vault password appeared in upgrade output")
	}
}

type scopedVaultAccess struct {
	client *http.Client
	token  string
}

func newScopedVaultAccess(t *testing.T, info vault.DaemonInfo, root, password, namespace string) scopedVaultAccess {
	t.Helper()
	controlToken, err := vault.ControlTokenForVault(filepath.Join(root, "credentials.kdbx"), []byte(password))
	if err != nil {
		t.Fatalf("unlock isolated Vault for scoped API request: %v", err)
	}
	defer clear(controlToken)
	token, err := vault.IssueContainerToken(info, controlToken, namespace)
	if err != nil {
		t.Fatalf("issue isolated namespace credential: %v", err)
	}
	client, err := vault.NewContainerHTTPClient(info)
	if err != nil {
		t.Fatalf("create pinned Vault API client: %v", err)
	}
	transport := client.Transport.(*http.Transport)
	transport.DialContext = func(ctx context.Context, _, address string) (net.Conn, error) {
		_, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		return (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(info.APIListenIP, port))
	}
	return scopedVaultAccess{client: client, token: token}
}

func assertScopedVaultValue(t *testing.T, access scopedVaultAccess, info vault.DaemonInfo, name, want string) {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, strings.TrimSuffix(info.APIAddress, "/")+"/v1/secrets/"+name, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+access.token)
	response, err := access.client.Do(request)
	if err != nil {
		t.Fatalf("read scoped Vault credential: %v", err)
	}
	defer response.Body.Close()
	var result struct {
		Value string `json:"value"`
	}
	if response.StatusCode != http.StatusOK || json.NewDecoder(response.Body).Decode(&result) != nil || result.Value != want {
		t.Fatalf("scoped Vault credential response = status %d, value %q; want %q", response.StatusCode, result.Value, want)
	}
}

func TestDirectInstallerUsesManagedLifecycleController(t *testing.T) {
	const targetVersion = "0.7.5-SNAPSHOT-feature-aact-wishlist"
	const password = "isolated-direct-installer-password"
	targetBinary := buildVersionedCLIBinary(t, targetVersion)
	binary, root, oldInfo, _, server := startVersionInstallFixture(t, password, true, nil)
	defer server.Close()
	prepareManagedControllerLayout(t, root, binary)
	installRoot := t.TempDir()
	archiveName := fmt.Sprintf("aact_%s_%s_%s.tar.gz", targetVersion, runtime.GOOS, versionInstallerArch())
	if runtime.GOOS == "windows" {
		archiveName = fmt.Sprintf("aact_%s_windows_%s.zip", targetVersion, versionInstallerArch())
	}
	archive := installerArchiveWithNative(t, mustReadBinary(t, targetBinary), "direct lifecycle install")
	archivePath := filepath.Join(t.TempDir(), archiveName)
	if err := os.WriteFile(archivePath, archive, 0600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(archive)
	if err := os.WriteFile(filepath.Join(filepath.Dir(archivePath), "checksums.txt"), []byte(hex.EncodeToString(digest[:])+"  "+archiveName+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	scriptName := "install.sh"
	if runtime.GOOS == "windows" {
		scriptName = "install.ps1"
	}
	script, err := filepath.Abs(filepath.Join("..", "..", scriptName))
	if err != nil {
		t.Fatal(err)
	}
	var command *exec.Cmd
	if runtime.GOOS == "windows" {
		powershell := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
		command = exec.Command(powershell, "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", script, "-Version", targetVersion, "-LocalArchive", archivePath, "-InstallDir", installRoot, "-SkipUserPath")
	} else {
		command = exec.Command("/bin/sh", script, "--version", targetVersion, "--local-archive", archivePath, "--install-dir", installRoot)
	}
	command.Dir = t.TempDir()
	command.Env = versionInstallEnvironment(t, root, "")
	if runtime.GOOS == "windows" {
		environment := command.Env[:0]
		for _, entry := range command.Env {
			name, _, _ := strings.Cut(entry, "=")
			if !strings.EqualFold(name, "PSModulePath") {
				environment = append(environment, entry)
			}
		}
		command.Env = append(environment, "PSModulePath="+filepath.Join(t.TempDir(), "powershell7-modules"))
	}
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("direct installer failed: %s: %v", output, err)
	}
	newInfo, err := vault.ReadDaemonInfo(root)
	if err != nil || !vault.DaemonRunning(newInfo) {
		t.Fatalf("direct installer did not leave daemon running: info=%+v err=%v output=%q", newInfo, err, output)
	}
	if newInfo.APIAddress != oldInfo.APIAddress {
		t.Fatalf("direct install changed the Vault API endpoint: old=%q new=%q", oldInfo.APIAddress, newInfo.APIAddress)
	}
	installed := filepath.Join(installRoot, "releases", targetVersion+"-"+runtime.GOOS+"-"+versionInstallerArch(), "bin", "aact")
	if runtime.GOOS == "windows" {
		installed += ".exe"
	}
	if _, err := os.Stat(installed); err != nil {
		t.Fatalf("explicit install directory was ignored: missing %s: %v", installed, err)
	}
	versionCommand := exec.Command(installed, "version")
	if versionOutput, err := versionCommand.CombinedOutput(); err != nil || strings.TrimSpace(string(versionOutput)) != targetVersion {
		t.Fatalf("snapshot executable reports %q, err=%v; want %s", versionOutput, err, targetVersion)
	}
}

func TestInstallerLifecycleRejectsUnsafeVersionWithoutRunningScript(t *testing.T) {
	binary := buildPublicCLIBinary(t)
	root := t.TempDir()
	marker := filepath.Join(root, "installer-ran")
	scriptPath := filepath.Join(root, "installer.sh")
	script := "#!/bin/sh\nprintf ran > " + shellQuote(marker) + "\n"
	if runtime.GOOS == "windows" {
		scriptPath = filepath.Join(root, "installer.ps1")
		script = "Set-Content -LiteralPath '" + strings.ReplaceAll(marker, "'", "''") + "' -Value ran\n"
	}
	if err := os.WriteFile(scriptPath, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(binary, "__installer-lifecycle", "--run", scriptPath, "--", "--version", "../outside")
	command.Dir = root
	command.Env = cleanTestEnvironment(t, os.Environ(), "HOME="+root, "AACT_STATE_DIR="+filepath.Join(root, "state"))
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "stable release or pipeline snapshot version") {
		t.Fatalf("unsafe version was not rejected: output=%q err=%v", output, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("installer script ran for unsafe version: stat err=%v", err)
	}
}

func TestInstallerLifecycleProvidesWindowsPowerShellModules(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("nested Windows PowerShell environment test")
	}
	binary := buildPublicCLIBinary(t)
	root := t.TempDir()
	source := filepath.Join(root, "archive.zip")
	marker := filepath.Join(root, "hash.txt")
	if err := os.WriteFile(source, []byte("verified test archive"), 0600); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "installer.ps1")
	if err := os.WriteFile(script, []byte("$hash = (Get-FileHash -LiteralPath $env:AACT_TEST_HASH_SOURCE -Algorithm SHA256).Hash\n[IO.File]::WriteAllText($env:AACT_TEST_HASH_MARKER, $hash)\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, "__installer-lifecycle", "--run", script, "--", "--version", "0.7.4")
	command.Dir = root
	command.Env = cleanTestEnvironment(t, os.Environ(), "HOME="+root, "AACT_STATE_DIR="+filepath.Join(root, "state"),
		"PSModulePath="+filepath.Join(root, "powershell7-modules"), "AACT_TEST_HASH_SOURCE="+source, "AACT_TEST_HASH_MARKER="+marker)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("public lifecycle command did not run the Windows installer: output=%q err=%v", output, err)
	}
	got, err := os.ReadFile(marker)
	want := fmt.Sprintf("%x", sha256.Sum256([]byte("verified test archive")))
	if err != nil || !strings.EqualFold(strings.TrimSpace(string(got)), want) {
		t.Fatalf("nested PowerShell Get-FileHash result=%q err=%v; want %s", got, err, want)
	}
}

func TestPipedInstallerUsesVerifiedStagedControllerAndIgnoresCwdScript(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell installer is not used on native Windows")
	}
	root := t.TempDir()
	maliciousDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "cwd-script-executed")
	malicious := "#!/bin/sh\nprintf compromised > \"" + marker + "\"\nexit 91\n"
	if err := os.WriteFile(filepath.Join(maliciousDir, "sh"), []byte(malicious), 0700); err != nil {
		t.Fatal(err)
	}
	binary := buildPublicCLIBinary(t)
	archive := installerArchiveWithNative(t, mustReadBinary(t, binary), "piped installer")
	archiveName := fmt.Sprintf("aact_1.2.3_%s_%s.tar.gz", runtime.GOOS, versionInstallerArch())
	archivePath := filepath.Join(t.TempDir(), archiveName)
	if err := os.WriteFile(archivePath, archive, 0600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(archive)
	checksums := fmt.Sprintf("%s  %s\n", hex.EncodeToString(digest[:]), archiveName)
	if err := os.WriteFile(filepath.Join(filepath.Dir(archivePath), "checksums.txt"), []byte(checksums), 0600); err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile(filepath.Join("..", "..", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command("/bin/sh", "-s", "--", "--version", "1.2.3", "--local-archive", archivePath, "--install-dir", root)
	command.Dir = maliciousDir
	command.Stdin = bytes.NewReader(script)
	command.Env = cleanTestEnvironment(t, os.Environ(), "HOME="+t.TempDir(), "AACT_STATE_DIR="+filepath.Join(root, "state"), "AACT_INSTALL_DIR="+root, "AACT_OS="+runtime.GOOS, "AACT_ARCH="+versionInstallerArch())
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("piped install failed: %s: %v", output, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("installer executed a CWD/sh file: stat err=%v", err)
	}
	installed := filepath.Join(root, "releases", "1.2.3-"+runtime.GOOS+"-"+versionInstallerArch(), "bin", "aact")
	if _, err := os.Stat(installed); err != nil {
		t.Fatalf("piped install did not activate verified release: %v", err)
	}
}

func TestPipedInstallerReadsUnlockPasswordFromControllingTerminal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix controlling terminal path")
	}
	const password = "isolated-piped-installer-terminal-password"
	binary, root, _, _, server := startVersionInstallFixture(t, password, true, nil)
	defer server.Close()
	prepareManagedControllerLayout(t, root, binary)
	writeCredentialHookResponse(t, `{"status":"missing"}`)
	archiveName := fmt.Sprintf("aact_1.2.3_%s_%s.tar.gz", runtime.GOOS, versionInstallerArch())
	archive := installerArchiveWithNative(t, mustReadBinary(t, binary), "terminal installer")
	archivePath := filepath.Join(t.TempDir(), archiveName)
	if err := os.WriteFile(archivePath, archive, 0600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(archive)
	if err := os.WriteFile(filepath.Join(filepath.Dir(archivePath), "checksums.txt"), []byte(hex.EncodeToString(digest[:])+"  "+archiveName+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs(filepath.Join("..", "..", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command("/bin/sh", "-c", `cat -- "$1" | /bin/sh -s -- --version 1.2.3 --local-archive "$2" --install-dir "$3"`, "sh", script, archivePath, root)
	command.Dir = t.TempDir()
	command.Env = versionInstallEnvironment(t, root, "")
	terminal, err := pty.Start(command)
	if err != nil {
		t.Fatal(err)
	}
	defer terminal.Close()
	output := &lockedOutput{}
	readDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(output, terminal)
		close(readDone)
	}()
	waitDone := make(chan error, 1)
	go func() { waitDone <- command.Wait() }()
	deadline := time.After(15 * time.Second)
	sentPassword := false
	for {
		captured := output.String()
		if !sentPassword && strings.Contains(captured, "Vault master password:") {
			if _, err := terminal.Write([]byte(password + "\n")); err != nil {
				t.Fatal(err)
			}
			sentPassword = true
		}
		if sentPassword {
			break
		}
		select {
		case err := <-waitDone:
			captured = output.String()
			t.Fatalf("piped update exited before opening controlling terminal: output=%q err=%v", captured, err)
		case <-deadline:
			_ = command.Process.Kill()
			t.Fatalf("piped update did not prompt on controlling terminal: %q", captured)
		case <-time.After(25 * time.Millisecond):
		}
	}
	select {
	case err := <-waitDone:
		if err != nil {
			captured := output.String()
			t.Fatalf("piped update failed after terminal unlock: output=%q err=%v", captured, err)
		}
	case <-time.After(30 * time.Second):
		_ = command.Process.Kill()
		t.Fatal("piped update hung after terminal unlock")
	}
	<-readDone
	info, err := vault.ReadDaemonInfo(root)
	if err != nil || !vault.DaemonRunning(info) {
		t.Fatalf("daemon was not restarted after terminal unlock: info=%+v err=%v", info, err)
	}
}

type lockedOutput struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (output *lockedOutput) Write(value []byte) (int, error) {
	output.mu.Lock()
	defer output.mu.Unlock()
	return output.buffer.Write(value)
}

func (output *lockedOutput) String() string {
	output.mu.Lock()
	defer output.mu.Unlock()
	return output.buffer.String()
}

func prepareManagedControllerLayout(t *testing.T, root, binary string) {
	t.Helper()
	target := filepath.Join(root, "releases", "0.7.4-"+runtime.GOOS+"-"+versionInstallerArch())
	binDir := filepath.Join(target, "bin")
	if err := os.MkdirAll(binDir, 0700); err != nil {
		t.Fatal(err)
	}
	name := "aact"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	managedBinary := filepath.Join(binDir, name)
	if _, err := os.Stat(managedBinary); os.IsNotExist(err) {
		data, err := os.ReadFile(binary)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(managedBinary, data, 0700); err != nil {
			t.Fatal(err)
		}
	}
	metadata := filepath.Join(target, ".archive-sha256")
	if _, err := os.Stat(metadata); os.IsNotExist(err) {
		if err := os.WriteFile(metadata, []byte(strings.Repeat("a", 64)+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, installer := range []string{"install.sh", "install.ps1"} {
		path := filepath.Join(target, installer)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			data, err := os.ReadFile(filepath.Join("..", "..", installer))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	launchers := filepath.Join(root, "bin")
	if err := os.MkdirAll(launchers, 0700); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		content := fmt.Sprintf("@\"%%~dp0..\\releases\\0.7.4-windows-%s\\bin\\aact.exe\" %%*\r\n", versionInstallerArch())
		launcher := filepath.Join(launchers, "aact.cmd")
		if _, err := os.Stat(launcher); os.IsNotExist(err) {
			if err := os.WriteFile(launcher, []byte(content), 0700); err != nil {
				t.Fatal(err)
			}
		}
	} else if _, err := os.Lstat(filepath.Join(launchers, "aact")); os.IsNotExist(err) {
		if err := os.Symlink(managedBinary, filepath.Join(launchers, "aact")); err != nil {
			t.Fatal(err)
		}
	}
}

func mustReadBinary(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestVersionInstallRequiresUnlockBeforeStoppingDaemon(t *testing.T) {
	const password = "isolated-version-upgrade-no-keyring-password"
	binary, root, oldInfo, _, server := startVersionInstallFixture(t, password, true, nil)
	defer server.Close()
	writeCredentialHookResponse(t, `{"status":"missing"}`)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, "version", "install", "1.2.3")
	command.Stdin = bytes.NewReader(nil)
	command.Dir = t.TempDir()
	command.Env = versionInstallEnvironment(t, root, server.URL+"/releases")
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "requires an interactive terminal") {
		t.Fatalf("upgrade without saved credential did not request interactive unlock: output=%q err=%v", output, err)
	}
	if !vault.DaemonRunning(oldInfo) {
		t.Fatal("daemon was stopped before credentials were available")
	}
	if _, err := os.Stat(filepath.Join(root, "releases", "1.2.3-"+runtime.GOOS+"-"+versionInstallerArch())); !os.IsNotExist(err) {
		t.Fatalf("release was installed before credentials were available: stat err=%v", err)
	}
}

func TestVersionInstallLeavesDaemonStoppedWhenInstallerFails(t *testing.T) {
	const password = "isolated-failed-installer-password"
	binary, root, _, _, server := startVersionInstallFixture(t, password, false, nil)
	defer server.Close()
	command := exec.Command(binary, "version", "install", "1.2.3")
	command.Dir = t.TempDir()
	command.Env = versionInstallEnvironment(t, root, server.URL+"/releases")
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "AACT daemon remains stopped") || !strings.Contains(string(output), "Release checksum mismatch") {
		t.Fatalf("installer failure did not report the stopped daemon: output=%q err=%v", output, err)
	}
	if _, readErr := vault.ReadDaemonInfo(root); !os.IsNotExist(readErr) {
		t.Fatalf("daemon metadata remained after installer failure: err=%v", readErr)
	}
}

func TestVersionInstallLeavesActivatedReleaseAndDaemonStoppedWhenNewDaemonCannotStart(t *testing.T) {
	const password = "isolated-failed-daemon-password"
	invalidExecutable := []byte("not an executable")
	binary, root, _, _, server := startVersionInstallFixture(t, password, true, invalidExecutable)
	defer server.Close()
	prepareManagedControllerLayout(t, root, binary)
	command := exec.Command(binary, "version", "install", "1.2.3")
	command.Dir = t.TempDir()
	command.Env = versionInstallEnvironment(t, root, server.URL+"/releases")
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "installation remains active and the daemon remains stopped") {
		t.Fatalf("new daemon failure did not report the installed target and stopped daemon: output=%q err=%v", output, err)
	}
	installed := filepath.Join(root, "releases", "1.2.3-"+runtime.GOOS+"-"+versionInstallerArch(), "bin", "aact")
	if runtime.GOOS == "windows" {
		installed += ".exe"
	}
	data, readErr := os.ReadFile(installed)
	if readErr != nil || string(data) != string(invalidExecutable) {
		t.Fatalf("target release did not remain installed: bytes=%q err=%v", data, readErr)
	}
	if _, readErr := vault.ReadDaemonInfo(root); !os.IsNotExist(readErr) {
		t.Fatalf("daemon metadata remained after failed target startup: err=%v", readErr)
	}
}

func TestInstallingNoDaemonReleaseLeavesPreviousDaemonStopped(t *testing.T) {
	const password = "isolated-no-daemon-target-password"
	binary, root, _, _, server := startVersionInstallFixtureForVersion(t, "0.5.0", password, true, nil)
	defer server.Close()
	command := exec.Command(binary, "version", "install", "0.5.0")
	command.Dir = t.TempDir()
	command.Env = versionInstallEnvironment(t, root, server.URL+"/releases")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("installing no-daemon release failed: %s: %v", output, err)
	}
	if !strings.Contains(string(output), "Vault-dependent running MCPs remain disconnected") {
		t.Fatalf("no-daemon impact was not reported: %q", output)
	}
	if _, err := vault.ReadDaemonInfo(root); !os.IsNotExist(err) {
		t.Fatalf("daemon metadata remained after installing a no-daemon release: err=%v", err)
	}
}

func TestInstallingHistoricalDaemonReleaseUsesLegacyBootstrap(t *testing.T) {
	const password = "isolated-legacy-daemon-target-password"
	binary, root, oldInfo, _, server := startVersionInstallFixtureForVersion(t, "0.5.17", password, true, nil)
	defer server.Close()
	command := exec.Command(binary, "version", "install", "0.5.17")
	command.Dir = t.TempDir()
	command.Env = versionInstallEnvironment(t, root, server.URL+"/releases")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("installing historical daemon release failed: %s: %v", output, err)
	}
	newInfo, err := vault.ReadDaemonInfo(root)
	if err != nil || !vault.DaemonRunning(newInfo) {
		t.Fatalf("legacy daemon was not restarted: info=%+v err=%v output=%q", newInfo, err, output)
	}
	if newInfo.APIAddress != oldInfo.APIAddress {
		t.Fatalf("legacy restart changed the Vault API endpoint: old=%q new=%q", oldInfo.APIAddress, newInfo.APIAddress)
	}
	if !strings.Contains(string(output), "no browser API") {
		t.Fatalf("historical browser limitation was not reported: %q", output)
	}
}

func TestDirectForceUpdateOfRunningReleaseRestartsUpdatedDaemon(t *testing.T) {
	const password = "isolated-force-same-release-password"
	binary, root, _, _, server := startVersionInstallFixtureForVersion(t, "0.7.4", password, true, nil)
	defer server.Close()
	prepareManagedControllerLayout(t, root, binary)
	archiveName := fmt.Sprintf("aact_0.7.4_%s_%s.tar.gz", runtime.GOOS, versionInstallerArch())
	if runtime.GOOS == "windows" {
		archiveName = fmt.Sprintf("aact_0.7.4_windows_%s.zip", versionInstallerArch())
	}
	archive := installerArchiveWithNative(t, mustReadBinary(t, binary), "same release")
	archivePath := filepath.Join(t.TempDir(), archiveName)
	if err := os.WriteFile(archivePath, archive, 0600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(archive)
	if err := os.WriteFile(filepath.Join(filepath.Dir(archivePath), "checksums.txt"), []byte(hex.EncodeToString(digest[:])+"  "+archiveName+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs(filepath.Join("..", "..", "install.sh"))
	if runtime.GOOS == "windows" {
		script, err = filepath.Abs(filepath.Join("..", "..", "install.ps1"))
	}
	if err != nil {
		t.Fatal(err)
	}
	var command *exec.Cmd
	if runtime.GOOS == "windows" {
		command = exec.Command(powerShell(t), "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", script, "-Version", "0.7.4", "-LocalArchive", archivePath, "-InstallDir", root, "-Force", "-SkipUserPath")
	} else {
		command = exec.Command("/bin/sh", script, "--version", "0.7.4", "--local-archive", archivePath, "--install-dir", root, "--force")
	}
	command.Dir = t.TempDir()
	command.Env = versionInstallEnvironment(t, root, "")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("same-release force update failed: output=%q err=%v", output, err)
	}
	info, readErr := vault.ReadDaemonInfo(root)
	if readErr != nil || !vault.DaemonRunning(info) {
		t.Fatalf("daemon was not restarted after force update: info=%+v err=%v", info, readErr)
	}
}

func TestDirectInstallerUsesManagedBootstrapForLegacyArchiveInExplicitRoot(t *testing.T) {
	const password = "isolated-legacy-direct-install-password"
	binary := buildPublicCLIBinary(t)
	stateRoot := prepareCredentialCLITest(t, password)
	home := t.TempDir()
	controllerRoot := t.TempDir()
	configuredRoot := t.TempDir()
	prepareManagedControllerLayout(t, controllerRoot, binary)
	controllerExecutable := filepath.Join(controllerRoot, "releases", "0.7.4-"+runtime.GOOS+"-"+versionInstallerArch(), "bin", "aact")
	if runtime.GOOS == "windows" {
		controllerExecutable += ".exe"
	}
	_, err := vault.StartDetached(controllerExecutable, stateRoot, []byte(password))
	if err != nil {
		t.Fatalf("start isolated managed daemon: %v", err)
	}
	t.Cleanup(func() {
		if info, readErr := vault.ReadDaemonInfo(stateRoot); readErr == nil {
			if token, tokenErr := vault.ControlTokenForVault(filepath.Join(stateRoot, "credentials.kdbx"), []byte(password)); tokenErr == nil {
				defer clear(token)
				_ = vault.StopDetached(info, token)
			}
		}
	})
	t.Setenv("AACT_VAULT_CREDENTIAL_HOOK", writeSavedCredentialHook(t, password))
	archiveName := fmt.Sprintf("aact_0.5.0_%s_%s.tar.gz", runtime.GOOS, versionInstallerArch())
	if runtime.GOOS == "windows" {
		archiveName = fmt.Sprintf("aact_0.5.0_windows_%s.zip", versionInstallerArch())
	}
	archive := legacyArchiveWithoutInstaller(t, mustReadBinary(t, buildLegacyNoopBinary(t)))
	archivePath := filepath.Join(t.TempDir(), archiveName)
	if err := os.WriteFile(archivePath, archive, 0600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(archive)
	if err := os.WriteFile(filepath.Join(filepath.Dir(archivePath), "checksums.txt"), []byte(hex.EncodeToString(digest[:])+"  "+archiveName+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs(filepath.Join("..", "..", "install.sh"))
	if runtime.GOOS == "windows" {
		script, err = filepath.Abs(filepath.Join("..", "..", "install.ps1"))
	}
	if err != nil {
		t.Fatal(err)
	}
	var command *exec.Cmd
	if runtime.GOOS == "windows" {
		command = exec.Command(powerShell(t), "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", script, "-Version", "0.5.0", "-LocalArchive", archivePath, "-InstallDir", controllerRoot, "-SkipUserPath")
	} else {
		command = exec.Command("/bin/sh", script, "--version", "0.5.0", "--local-archive", archivePath, "--install-dir", controllerRoot)
	}
	command.Dir = t.TempDir()
	command.Env = cleanTestEnvironment(t, os.Environ(), "HOME="+home, "USERPROFILE="+home, "LOCALAPPDATA="+home,
		"AACT_STATE_DIR="+stateRoot, "AACT_INSTALL_DIR="+configuredRoot, "AACT_INSTALLER_SKIP_USER_PATH=1",
		"AACT_VAULT_CREDENTIAL_HOOK="+os.Getenv("AACT_VAULT_CREDENTIAL_HOOK"), "AACT_OS="+runtime.GOOS, "AACT_ARCH="+versionInstallerArch())
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("legacy archive install failed before/after lifecycle delegation: output=%q err=%v", output, err)
	}
	if !strings.Contains(string(output), "Vault-dependent running MCPs remain disconnected") {
		t.Fatalf("legacy no-daemon impact was not reported: %q", output)
	}
	installed := filepath.Join(controllerRoot, "releases", "0.5.0-"+runtime.GOOS+"-"+versionInstallerArch(), "bin", "aact")
	if runtime.GOOS == "windows" {
		installed += ".exe"
	}
	if _, err := os.Stat(installed); err != nil {
		t.Fatalf("legacy release was not installed from the explicit root: %v", err)
	}
	if _, err := vault.ReadDaemonInfo(stateRoot); !os.IsNotExist(err) {
		t.Fatalf("daemon was not stopped for no-daemon target: err=%v", err)
	}
}

func TestPipedInstallerUsesControllingTerminalWithLegacyController(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix installer reads /dev/tty for piped updates")
	}
	const password = "isolated-piped-legacy-controller-password"
	binary := buildPublicCLIBinary(t)
	stateRoot := prepareCredentialCLITest(t, password)
	installRoot := t.TempDir()
	prepareManagedControllerLayout(t, installRoot, binary)
	managed := filepath.Join(installRoot, "releases", "0.7.4-"+runtime.GOOS+"-"+versionInstallerArch(), "bin", "aact")
	legacyController := fmt.Sprintf("#!/bin/sh\ncase \"$1\" in __installer-lifecycle|daemon) exit 2 ;; esac\nexec %q \"$@\"\n", binary)
	if err := os.WriteFile(managed, []byte(legacyController), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := vault.StartDetached(managed, stateRoot, []byte(password)); err != nil {
		t.Fatalf("start isolated daemon from legacy controller: %v", err)
	}
	t.Cleanup(func() {
		if info, readErr := vault.ReadDaemonInfo(stateRoot); readErr == nil {
			if token, tokenErr := vault.ControlTokenForVault(filepath.Join(stateRoot, "credentials.kdbx"), []byte(password)); tokenErr == nil {
				defer clear(token)
				_ = vault.StopDetached(info, token)
			}
		}
	})
	writeCredentialHookResponse(t, `{"status":"missing"}`)
	archiveName := fmt.Sprintf("aact_0.5.0_%s_%s.tar.gz", runtime.GOOS, versionInstallerArch())
	archive := legacyArchiveWithoutInstaller(t, []byte("legacy executable without supervisor\n"))
	archivePath := filepath.Join(t.TempDir(), archiveName)
	if err := os.WriteFile(archivePath, archive, 0600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(archive)
	if err := os.WriteFile(filepath.Join(filepath.Dir(archivePath), "checksums.txt"), []byte(hex.EncodeToString(digest[:])+"  "+archiveName+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs(filepath.Join("..", "..", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command("/bin/sh", "-c", `cat -- "$1" | /bin/sh -s -- --version 0.5.0 --local-archive "$2" --install-dir "$3"`, "sh", script, archivePath, installRoot)
	command.Dir = t.TempDir()
	home := filepath.Dir(stateRoot)
	command.Env = cleanTestEnvironment(t, os.Environ(), "HOME="+home, "USERPROFILE="+home, "LOCALAPPDATA="+home,
		"AACT_STATE_DIR="+stateRoot, "AACT_INSTALL_DIR="+installRoot, "AACT_INSTALLER_SKIP_USER_PATH=1",
		"AACT_VAULT_API_BIND_IP=127.0.0.1", "AACT_VAULT_CREDENTIAL_HOOK="+os.Getenv("AACT_VAULT_CREDENTIAL_HOOK"),
		"AACT_OS="+runtime.GOOS, "AACT_ARCH="+versionInstallerArch())
	terminal, err := pty.Start(command)
	if err != nil {
		t.Fatal(err)
	}
	defer terminal.Close()
	output := &lockedOutput{}
	readDone := make(chan struct{})
	go func() { _, _ = io.Copy(output, terminal); close(readDone) }()
	waitDone := make(chan error, 1)
	go func() { waitDone <- command.Wait() }()
	deadline := time.After(15 * time.Second)
	sentPassword, declinedSave := false, false
	for {
		captured := output.String()
		if !sentPassword && strings.Contains(captured, "Vault master password:") {
			if _, err := terminal.Write([]byte(password + "\n")); err != nil {
				t.Fatal(err)
			}
			sentPassword = true
		}
		if sentPassword && !declinedSave && strings.Contains(captured, "Save the vault password to the OS credential store") {
			if _, err := terminal.Write([]byte("n\n")); err != nil {
				t.Fatal(err)
			}
			declinedSave = true
		}
		if declinedSave {
			break
		}
		select {
		case err := <-waitDone:
			t.Fatalf("piped install exited before legacy stop authenticated: output=%q err=%v", output.String(), err)
		case <-deadline:
			_ = command.Process.Kill()
			t.Fatalf("piped install did not request legacy controller credentials: %q", output.String())
		case <-time.After(25 * time.Millisecond):
		}
	}
	select {
	case err := <-waitDone:
		if err != nil {
			t.Fatalf("legacy controller update failed: output=%q err=%v", output.String(), err)
		}
	case <-time.After(30 * time.Second):
		_ = command.Process.Kill()
		t.Fatal("legacy controller update hung after terminal unlock")
	}
	<-readDone
	if _, err := vault.ReadDaemonInfo(stateRoot); !os.IsNotExist(err) {
		t.Fatalf("no-daemon historical install did not leave daemon stopped: err=%v output=%q", err, output.String())
	}
	installed := filepath.Join(installRoot, "releases", "0.5.0-"+runtime.GOOS+"-"+versionInstallerArch(), "bin", "aact")
	if _, err := os.Stat(installed); err != nil {
		t.Fatalf("piped legacy install did not activate release: %v", err)
	}
}

func TestHeadlessInstallerUsesConfiguredCredentialWithLegacyController(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix installer fallback")
	}
	const password = "isolated-headless-legacy-controller-password"
	binary := buildPublicCLIBinary(t)
	stateRoot := prepareCredentialCLITest(t, password)
	installRoot := t.TempDir()
	prepareManagedControllerLayout(t, installRoot, binary)
	managed := filepath.Join(installRoot, "releases", "0.7.4-"+runtime.GOOS+"-"+versionInstallerArch(), "bin", "aact")
	legacyController := fmt.Sprintf("#!/bin/sh\ncase \"$1\" in __installer-lifecycle|daemon) exit 2 ;; esac\nexec %q \"$@\"\n", binary)
	if err := os.WriteFile(managed, []byte(legacyController), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := vault.StartDetached(managed, stateRoot, []byte(password)); err != nil {
		t.Fatalf("start isolated daemon from legacy controller: %v", err)
	}
	t.Cleanup(func() {
		if info, readErr := vault.ReadDaemonInfo(stateRoot); readErr == nil {
			if token, tokenErr := vault.ControlTokenForVault(filepath.Join(stateRoot, "credentials.kdbx"), []byte(password)); tokenErr == nil {
				defer clear(token)
				_ = vault.StopDetached(info, token)
			}
		}
	})
	archiveName := fmt.Sprintf("aact_0.5.0_%s_%s.tar.gz", runtime.GOOS, versionInstallerArch())
	archive := legacyArchiveWithoutInstaller(t, []byte("legacy executable without supervisor\n"))
	archivePath := filepath.Join(t.TempDir(), archiveName)
	if err := os.WriteFile(archivePath, archive, 0600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(archive)
	if err := os.WriteFile(filepath.Join(filepath.Dir(archivePath), "checksums.txt"), []byte(hex.EncodeToString(digest[:])+"  "+archiveName+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs(filepath.Join("..", "..", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command("/bin/sh", script, "--version", "0.5.0", "--local-archive", archivePath, "--install-dir", installRoot)
	command.Dir = t.TempDir()
	command.Stdin = strings.NewReader("")
	detachTestProcessFromControllingTerminal(command)
	home := filepath.Dir(stateRoot)
	command.Env = cleanTestEnvironment(t, os.Environ(), "HOME="+home, "USERPROFILE="+home, "LOCALAPPDATA="+home,
		"AACT_STATE_DIR="+stateRoot, "AACT_INSTALL_DIR="+installRoot, "AACT_INSTALLER_SKIP_USER_PATH=1",
		"AACT_VAULT_API_BIND_IP=127.0.0.1", "AACT_VAULT_PASSWORD="+password,
		"AACT_OS="+runtime.GOOS, "AACT_ARCH="+versionInstallerArch())
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("headless legacy update failed with configured credentials: output=%q err=%v", output, err)
	}
	if strings.Contains(string(output), "Vault master password:") || !strings.Contains(string(output), "the previous daemon remains stopped and Vault-dependent running MCPs are disconnected") {
		t.Fatalf("headless update did not use configured credentials and report no-daemon impact: %q", output)
	}
	if _, err := vault.ReadDaemonInfo(stateRoot); !os.IsNotExist(err) {
		t.Fatalf("headless no-daemon update did not stop daemon: %v", err)
	}
}

func TestLegacyControllerRefusesRunningDaemonUpgradeToDaemonCapableTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix installer path")
	}
	const password = "isolated-legacy-target-upgrade-password"
	binary := buildPublicCLIBinary(t)
	stateRoot := prepareCredentialCLITest(t, password)
	installRoot := t.TempDir()
	prepareManagedControllerLayout(t, installRoot, binary)
	managed := filepath.Join(installRoot, "releases", "0.7.4-"+runtime.GOOS+"-"+versionInstallerArch(), "bin", "aact")
	legacyController := fmt.Sprintf("#!/bin/sh\ncase \"$1\" in __installer-lifecycle|daemon) exit 2 ;; esac\nexec %q \"$@\"\n", binary)
	if err := os.WriteFile(managed, []byte(legacyController), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := vault.StartDetached(managed, stateRoot, []byte(password)); err != nil {
		t.Fatalf("start isolated daemon from legacy controller: %v", err)
	}
	t.Cleanup(func() {
		if info, readErr := vault.ReadDaemonInfo(stateRoot); readErr == nil {
			if token, tokenErr := vault.ControlTokenForVault(filepath.Join(stateRoot, "credentials.kdbx"), []byte(password)); tokenErr == nil {
				defer clear(token)
				_ = vault.StopDetached(info, token)
			}
		}
	})
	archiveName := fmt.Sprintf("aact_0.5.17_%s_%s.tar.gz", runtime.GOOS, versionInstallerArch())
	archive := legacyArchiveWithoutInstaller(t, []byte("legacy daemon-capable target"))
	archivePath := filepath.Join(t.TempDir(), archiveName)
	if err := os.WriteFile(archivePath, archive, 0600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(archive)
	if err := os.WriteFile(filepath.Join(filepath.Dir(archivePath), "checksums.txt"), []byte(hex.EncodeToString(digest[:])+"  "+archiveName+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs(filepath.Join("..", "..", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command("/bin/sh", script, "--version", "0.5.17", "--local-archive", archivePath, "--install-dir", installRoot)
	command.Dir = t.TempDir()
	home := filepath.Dir(stateRoot)
	command.Env = cleanTestEnvironment(t, os.Environ(), "HOME="+home, "USERPROFILE="+home, "LOCALAPPDATA="+home,
		"AACT_STATE_DIR="+stateRoot, "AACT_INSTALL_DIR="+installRoot, "AACT_INSTALLER_SKIP_USER_PATH=1",
		"AACT_VAULT_API_BIND_IP=127.0.0.1", "AACT_OS="+runtime.GOOS, "AACT_ARCH="+versionInstallerArch())
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "A running daemon cannot be safely updated by this installer/controller combination; install was not started.") {
		t.Fatalf("legacy controller did not report its daemon-capable target limitation: output=%q err=%v", output, err)
	}
	info, err := vault.ReadDaemonInfo(stateRoot)
	if err != nil || !vault.DaemonRunning(info) {
		t.Fatalf("legacy-controller refusal stopped the running daemon: info=%+v err=%v", info, err)
	}
	if _, err := os.Stat(filepath.Join(installRoot, "releases", "0.5.17-"+runtime.GOOS+"-"+versionInstallerArch())); !os.IsNotExist(err) {
		t.Fatalf("legacy target was activated despite the safe refusal: err=%v", err)
	}
}

func TestHealthyDaemonCanBeReusedFromPreviousManagedRelease(t *testing.T) {
	const password = "isolated-previous-release-daemon-password"
	binary := buildPublicCLIBinary(t)
	stateRoot := prepareCredentialCLITest(t, password)
	root := t.TempDir()
	t.Setenv("AACT_VAULT_API_BIND_IP", "127.0.0.1")
	makeRelease := func(version string) string {
		release := filepath.Join(root, "releases", version+"-"+runtime.GOOS+"-"+versionInstallerArch())
		bin := filepath.Join(release, "bin")
		if err := os.MkdirAll(bin, 0700); err != nil {
			t.Fatal(err)
		}
		executable := filepath.Join(bin, "aact")
		if runtime.GOOS == "windows" {
			executable += ".exe"
		}
		if err := os.WriteFile(executable, mustReadBinary(t, binary), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(release, ".archive-sha256"), []byte(strings.Repeat("b", 64)+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		return executable
	}
	previous := makeRelease("0.7.4")
	next := makeRelease("1.2.3")
	launcherDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(launcherDir, 0700); err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(launcherDir, "aact")
	if runtime.GOOS == "windows" {
		launcher = filepath.Join(launcherDir, "aact.cmd")
	}
	setLauncher := func(executable string) {
		if runtime.GOOS == "windows" {
			releaseName := filepath.Base(filepath.Dir(filepath.Dir(executable)))
			content := fmt.Sprintf("@\"%%~dp0..\\releases\\%s\\bin\\aact.exe\" %%*\r\n", releaseName)
			if err := os.WriteFile(launcher, []byte(content), 0700); err != nil {
				t.Fatal(err)
			}
		} else {
			_ = os.Remove(launcher)
			if err := os.Symlink(executable, launcher); err != nil {
				t.Fatal(err)
			}
		}
	}
	setLauncher(previous)
	started, err := vault.StartDetached(previous, stateRoot, []byte(password))
	if err != nil {
		t.Fatalf("start previous managed release daemon: %v", err)
	}
	t.Cleanup(func() {
		if current, readErr := vault.ReadDaemonInfo(stateRoot); readErr == nil {
			if token, tokenErr := vault.ControlTokenForVault(filepath.Join(stateRoot, "credentials.kdbx"), []byte(password)); tokenErr == nil {
				defer clear(token)
				_ = vault.StopDetached(current, token)
			}
		}
	})
	setLauncher(next)
	reused, err := vault.StartDetached(previous, stateRoot, []byte(password))
	if err != nil {
		t.Fatalf("reuse healthy daemon from previous managed release: %v", err)
	}
	if reused.ControlAddress != started.ControlAddress || !vault.DaemonRunning(reused) {
		t.Fatalf("healthy daemon was replaced instead of reused: before=%+v after=%+v", started, reused)
	}
}

func TestPowerShellLegacyArchiveWithoutBundledInstallerExitsSuccessfully(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("native PowerShell behavior")
	}
	root := t.TempDir()
	stateRoot := filepath.Join(t.TempDir(), "state")
	controllerBinary := buildPublicCLIBinary(t)
	prepareManagedControllerLayout(t, root, controllerBinary)
	managedInstaller := filepath.Join(root, "releases", "0.7.4-windows-"+versionInstallerArch(), "install.ps1")
	if err := os.Remove(managedInstaller); err != nil {
		t.Fatal(err)
	}
	archiveName := fmt.Sprintf("aact_0.5.0_windows_%s.zip", versionInstallerArch())
	archivePath := filepath.Join(t.TempDir(), archiveName)
	archive := legacyArchiveWithoutInstaller(t, mustReadBinary(t, controllerBinary))
	if err := os.WriteFile(archivePath, archive, 0600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(archive)
	if err := os.WriteFile(filepath.Join(filepath.Dir(archivePath), "checksums.txt"), []byte(hex.EncodeToString(digest[:])+"  "+archiveName+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs(filepath.Join("..", "..", "install.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(powerShell(t), "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", script,
		"-Version", "0.5.0", "-LocalArchive", archivePath, "-InstallDir", root, "-SkipUserPath")
	command.Dir = t.TempDir()
	command.Env = cleanTestEnvironment(t, os.Environ(), "HOME="+t.TempDir(), "USERPROFILE="+t.TempDir(), "LOCALAPPDATA="+t.TempDir(),
		"AACT_STATE_DIR="+stateRoot, "AACT_INSTALL_DIR="+root, "AACT_INSTALLER_SKIP_USER_PATH=1",
		"AACT_OS=windows", "AACT_ARCH="+versionInstallerArch())
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("PowerShell reported failure for a successful legacy install: %s: %v", output, err)
	}
	if !strings.Contains(string(output), "Installed AACT 0.5.0") {
		t.Fatalf("legacy archive was not installed: %q", output)
	}
	installed := filepath.Join(root, "releases", "0.5.0-windows-"+versionInstallerArch(), "bin", "aact.exe")
	if _, err := os.Stat(installed); err != nil {
		t.Fatalf("legacy release was not activated: %v", err)
	}
}

func legacyArchiveWithoutInstaller(t *testing.T, binary []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if runtime.GOOS == "windows" {
		archive := zip.NewWriter(&buffer)
		for _, file := range []struct {
			name string
			data []byte
		}{
			{"bin/aact.exe", binary}, {"packages/sample/SKILL.md", []byte("legacy package")}, {"release.json", []byte(`{"version":"0.5.0"}`)},
		} {
			writer, err := archive.Create(file.name)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := writer.Write(file.data); err != nil {
				t.Fatal(err)
			}
		}
		if err := archive.Close(); err != nil {
			t.Fatal(err)
		}
		return buffer.Bytes()
	}
	gzipWriter := gzip.NewWriter(&buffer)
	tarWriter := tar.NewWriter(gzipWriter)
	for _, file := range []struct {
		name string
		data []byte
		mode int64
	}{
		{"bin/aact", binary, 0755}, {"packages/sample/SKILL.md", []byte("legacy package"), 0644}, {"release.json", []byte(`{"version":"0.5.0"}`), 0644},
	} {
		if err := tarWriter.WriteHeader(&tar.Header{Name: file.name, Mode: file.mode, Size: int64(len(file.data))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tarWriter.Write(file.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func startVersionInstallFixture(t *testing.T, password string, validChecksum bool, installedBinary []byte) (string, string, vault.DaemonInfo, string, *httptest.Server) {
	return startVersionInstallFixtureForVersion(t, "1.2.3", password, validChecksum, installedBinary)
}

func startVersionInstallFixtureForVersion(t *testing.T, version, password string, validChecksum bool, installedBinary []byte) (string, string, vault.DaemonInfo, string, *httptest.Server) {
	t.Helper()
	builtBinary := buildPublicCLIBinary(t)
	root := prepareCredentialCLITest(t, password)
	prepareManagedControllerLayout(t, root, builtBinary)
	binary := filepath.Join(root, "releases", "0.7.4-"+runtime.GOOS+"-"+versionInstallerArch(), "bin", "aact")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	t.Setenv("AACT_VAULT_API_BIND_IP", "127.0.0.1")
	trace := filepath.Join(t.TempDir(), "credential-hook-trace")
	t.Setenv("AACT_TEST_HOOK_TRACE", trace)
	t.Setenv("AACT_VAULT_CREDENTIAL_HOOK", writeSavedCredentialHook(t, password))
	oldInfo, err := vault.StartDetached(binary, root, []byte(password))
	if err != nil {
		t.Fatalf("start isolated daemon: %v", err)
	}
	t.Cleanup(func() {
		info, err := vault.ReadDaemonInfo(root)
		if err != nil {
			return
		}
		token, err := vault.ControlTokenForVault(filepath.Join(root, "credentials.kdbx"), []byte(password))
		if err == nil {
			defer clear(token)
			_ = vault.StopDetached(info, token)
		}
	})
	if installedBinary == nil {
		installedBinary, err = os.ReadFile(binary)
		if err != nil {
			t.Fatalf("read updated AACT executable fixture: %v", err)
		}
	}
	archive := installerArchiveWithNative(t, installedBinary, "version upgrade fixture")
	server := versionReleaseServer(t, version, versionInstallerArch(), archive, validChecksum)
	return binary, root, oldInfo, trace, server
}

func versionInstallerArch() string {
	arch := runtime.GOARCH
	if runtime.GOOS != "windows" {
		return arch
	}
	native := os.Getenv("PROCESSOR_ARCHITEW6432")
	if native == "" {
		native = os.Getenv("PROCESSOR_ARCHITECTURE")
	}
	switch strings.ToUpper(native) {
	case "AMD64", "X86_64", "X64":
		return "amd64"
	case "ARM64", "AARCH64":
		return "arm64"
	default:
		return arch
	}
}

func versionInstallEnvironment(t *testing.T, root, releaseURL string) []string {
	home := filepath.Dir(root)
	return cleanTestEnvironment(t, os.Environ(), "HOME="+home, "USERPROFILE="+home, "LOCALAPPDATA="+home,
		"XDG_STATE_HOME="+filepath.Join(home, "xdg"), "AACT_STATE_DIR="+root, "AACT_INSTALL_DIR="+root,
		"AACT_INSTALLER_SKIP_USER_PATH=1", "AACT_RELEASE_API_URL="+releaseURL,
		"AACT_VAULT_API_BIND_IP=127.0.0.1", "AACT_VAULT_CREDENTIAL_HOOK="+os.Getenv("AACT_VAULT_CREDENTIAL_HOOK"),
		"PROCESSOR_ARCHITEW6432="+os.Getenv("PROCESSOR_ARCHITEW6432"))
}

func TestVersionInstallFromUnmanagedBinaryRequiresExplicitInstallRoot(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]any{map[string]any{"tag_name": "v1.2.3"}})
	}))
	defer server.Close()
	cmd := exec.Command(buildVersionCommandBinary(t), "version", "install", "1.2.3")
	cmd.Dir = t.TempDir()
	cmd.Env = cleanTestEnvironment(t, os.Environ(), "AACT_RELEASE_API_URL="+server.URL+"/releases")
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
	cmd.Env = cleanTestEnvironment(t, os.Environ(), "HOME="+home, "AACT_RELEASE_API_URL="+server.URL+"/releases")
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

func TestVersionInstallFromCheckoutUsesVerifiedReleaseInstaller(t *testing.T) {
	archive := installerArchive(t)
	server := versionReleaseServer(t, "0.5.1", runtime.GOARCH, archive, true)
	defer server.Close()
	root := t.TempDir()
	cmd := exec.Command(buildVersionCommandBinary(t), "version", "install", "0.5.1")
	cmd.Dir = filepath.Join("..", "..")
	cmd.Env = cleanTestEnvironment(t, os.Environ(), "AACT_INSTALL_DIR="+root, "AACT_INSTALLER_SKIP_USER_PATH=1", "AACT_RELEASE_API_URL="+server.URL+"/releases")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("historical release install from checkout failed: %s: %v", output, err)
	}
	installed := filepath.Join(root, "releases", "0.5.1-"+runtime.GOOS+"-"+runtime.GOARCH, "bin", "aact")
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
	cmd.Env = cleanTestEnvironment(t, os.Environ(), "HOME="+home, "AACT_RELEASE_API_URL="+server.URL+"/releases", "AACT_TEST_MARKER="+marker, "AACT_INSTALLER_SKIP_USER_PATH=1")
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

func TestUnmanagedExecutableDoesNotTrustInstallerFromWorkingDirectory(t *testing.T) {
	version, arch := "1.2.3", runtime.GOARCH
	server := versionReleaseServer(t, version, arch, installerArchive(t), true)
	defer server.Close()
	root, workingDir := t.TempDir(), t.TempDir()
	executableDir := t.TempDir()
	executableName := "aact"
	if runtime.GOOS == "windows" {
		executableName += ".exe"
	}
	binary, err := os.ReadFile(buildVersionCommandBinary(t))
	if err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(executableDir, executableName)
	if err := os.WriteFile(executable, binary, 0755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(workingDir, "untrusted-installer-ran")
	installerName, untrustedScript := "install.sh", "#!/bin/sh\nprintf malicious > \"$AACT_TEST_MARKER\"\nexit 9\n"
	if runtime.GOOS == "windows" {
		installerName, untrustedScript = "install.ps1", "Set-Content -LiteralPath $env:AACT_TEST_MARKER -Value malicious; exit 9\n"
	}
	if err := os.WriteFile(filepath.Join(workingDir, installerName), []byte(untrustedScript), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workingDir, "go.mod"), []byte("module untrusted\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(executable, "version", "install", version)
	cmd.Dir = workingDir
	cmd.Env = cleanTestEnvironment(t, os.Environ(), "AACT_INSTALL_DIR="+root,
		"AACT_RELEASE_API_URL="+server.URL+"/releases", "AACT_TEST_MARKER="+marker, "AACT_INSTALLER_SKIP_USER_PATH=1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("unmanaged install failed to use a verified installer: %s: %v", output, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("untrusted working-directory installer ran: stat err=%v", err)
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
	cmd.Env = cleanTestEnvironment(t, os.Environ(), "AACT_INSTALL_DIR="+root, "AACT_RELEASE_API_URL="+server.URL+"/releases", "AACT_INSTALLER_SKIP_USER_PATH=1")
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
	cmd.Env = cleanTestEnvironment(t, os.Environ(), "AACT_RELEASE_API_URL="+server.URL+"/releases", "AACT_INSTALL_DIR="+root, "AACT_INSTALLER_SKIP_USER_PATH=1")
	output, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "SHA-256 does not match") {
		t.Fatalf("mismatched digest was not rejected: %q, %v", output, err)
	}
	if got, readErr := os.ReadFile(sentinel); readErr != nil || string(got) != "preserve" {
		t.Fatalf("existing install sentinel changed: %q, %v", got, readErr)
	}
}

func cleanTestEnvironment(t *testing.T, environment []string, overrides ...string) []string {
	t.Helper()
	keys := map[string]string{}
	for _, override := range overrides {
		parts := strings.SplitN(override, "=", 2)
		keys[parts[0]] = override
	}
	for _, name := range []string{"HOME", "USERPROFILE", "LOCALAPPDATA", "XDG_STATE_HOME", "AACT_INSTALL_DIR", "AACT_STATE_DIR", "AACT_VAULT_PASSWORD", "AACT_VAULT_PASSWORD_FILE", "AACT_VAULT_CREDENTIAL_HOOK", "AACT_VAULT_API_BIND_IP", "AACT_RELEASE_API_URL", "AACT_INSTALLER_SKIP_USER_PATH", "AACT_INSTALLER_LIFECYCLE", "PROCESSOR_ARCHITEW6432", "PROCESSOR_ARCHITECTURE", "AACT_ARCH", "AACT_OS", "AACT_VERSION", "AACT_DOWNLOAD_BASE", "HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy"} {
		if _, set := keys[name]; !set {
			keys[name] = ""
		}
	}
	home := strings.TrimPrefix(keys["HOME"], "HOME=")
	if home == "" {
		home = t.TempDir()
		keys["HOME"] = "HOME=" + home
	}
	if keys["USERPROFILE"] == "" {
		keys["USERPROFILE"] = "USERPROFILE=" + home
	}
	if keys["LOCALAPPDATA"] == "" {
		keys["LOCALAPPDATA"] = "LOCALAPPDATA=" + home
	}
	if keys["XDG_STATE_HOME"] == "" {
		keys["XDG_STATE_HOME"] = "XDG_STATE_HOME=" + filepath.Join(home, "xdg-state")
	}
	if keys["AACT_STATE_DIR"] == "" {
		keys["AACT_STATE_DIR"] = "AACT_STATE_DIR=" + filepath.Join(home, "aact-state")
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
