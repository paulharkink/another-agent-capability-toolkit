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
	archiveName := fmt.Sprintf("aact_1.2.3_%s_%s", runtime.GOOS, runtime.GOARCH)
	archiveExt := "tar.gz"
	if runtime.GOOS == "windows" {
		archiveExt = "zip"
	}
	archiveName += "." + archiveExt
	installerName := "install.sh"
	if runtime.GOOS == "windows" {
		installerName = "install.ps1"
	}
	installer, err := os.ReadFile("../../" + installerName)
	if err != nil {
		t.Fatal(err)
	}
	checksumText := []byte(fmt.Sprintf("%s  %s\n", digestHex(archive), archiveName))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/releases":
			_ = json.NewEncoder(w).Encode([]any{map[string]any{"tag_name": "v1.2.3", "assets": []any{
				map[string]any{"name": installerName, "browser_download_url": serverAssetURL(r, "/assets/installer"), "digest": "sha256:" + digestHex(installer)},
				map[string]any{"name": "checksums.txt", "browser_download_url": serverAssetURL(r, "/assets/checksums"), "digest": "sha256:" + digestHex(checksumText)},
				map[string]any{"name": archiveName, "browser_download_url": serverAssetURL(r, "/assets/archive"), "digest": "sha256:" + digestHex(archive)},
			}}})
		case r.URL.Path == "/assets/installer":
			_, _ = w.Write(installer)
		case r.URL.Path == "/assets/checksums":
			_, _ = w.Write(checksumText)
		case r.URL.Path == "/assets/archive":
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	bin := buildVersionCommandBinary(t)
	root := t.TempDir()
	cmd := exec.Command(bin, "version", "install", "1.2.3")
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "AACT_RELEASE_API_URL="+server.URL+"/releases", "AACT_DOWNLOAD_BASE="+server.URL+"/download", "AACT_INSTALL_DIR="+root)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("version install failed: %s: %v", output, err)
	}
	if !strings.Contains(string(output), "Installed AACT 1.2.3") {
		t.Fatalf("installer result was not reported: %q", output)
	}
	if _, err := os.Stat(filepath.Join(root, "releases", "1.2.3-"+runtime.GOOS+"-"+runtime.GOARCH, "bin", func() string {
		if runtime.GOOS == "windows" {
			return "aact.exe"
		}
		return "aact"
	}())); err != nil {
		t.Fatalf("managed release missing: %v", err)
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
	cmd := exec.Command("go", "build", "-o", path, "./cmd/aact")
	cmd.Dir = "../../main"
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build aact: %s: %v", output, err)
	}
	return path
}
