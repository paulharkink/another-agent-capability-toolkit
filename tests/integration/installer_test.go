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
	return installerArchiveWithMarker(t, "fixture skill")
}
func installerArchiveWithMarker(t *testing.T, marker string) []byte {
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
		}{{"bin/aact.exe", native}, {"packages/sample/SKILL.md", []byte(marker)}, {"release.json", []byte(`{"version":"fixture"}`)}} {
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
	}{{"bin/aact", native, 0755}, {"packages/sample/SKILL.md", []byte(marker), 0644}, {"release.json", []byte(`{"version":"fixture"}`), 0644}} {
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

func TestInstallerAcceptsLocalArchiveWithAdjacentChecksum(t *testing.T) {
	root := t.TempDir()
	archive := installerArchive(t)
	archiveDir := t.TempDir()
	extension := "tar.gz"
	if installerPlatform() == "windows" {
		extension = "zip"
	}
	version := "0.5.1-SNAPSHOT-feature-aact-remediation-pr"
	filename := fmt.Sprintf("aact_%s_%s_amd64.%s", version, installerPlatform(), extension)
	archivePath := filepath.Join(archiveDir, filename)
	if err := os.WriteFile(archivePath, archive, 0600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(archive)
	checksums := fmt.Sprintf("%x  %s\n", sum, filename)
	if err := os.WriteFile(filepath.Join(archiveDir, "checksums.txt"), []byte(checksums), 0600); err != nil {
		t.Fatal(err)
	}

	out, err := runLocalInstaller(t, root, archivePath)
	if err != nil {
		t.Fatalf("local archive install failed: %s: %v", out, err)
	}
	release := filepath.Join(root, "releases", version+"-"+installerPlatform()+"-amd64")
	if _, err := os.Stat(filepath.Join(release, "packages", "sample", "SKILL.md")); err != nil {
		t.Fatalf("local archive resources not installed: %v", err)
	}
	if !strings.Contains(out, "Installed AACT "+version) {
		t.Fatalf("local version was not reported: %s", out)
	}
}

func TestInstallerForceReplacesDifferentArchiveWithSameVersion(t *testing.T) {
	root := t.TempDir()
	version := "0.5.1-SNAPSHOT-feature-aact-remediation-pr"
	archiveA := writeLocalInstallerArchive(t, version, "snapshot A")
	archiveB := writeLocalInstallerArchive(t, version, "snapshot B")
	firstOutput, err := runLocalInstaller(t, root, archiveA)
	if err != nil {
		t.Fatalf("install archive A: %s: %v", firstOutput, err)
	}
	release := filepath.Join(root, "releases", version+"-"+installerPlatform()+"-amd64")
	manifest := filepath.Join(release, "packages", "sample", "SKILL.md")
	if got, err := os.ReadFile(manifest); err != nil || string(got) != "snapshot A" {
		t.Fatalf("initial release content = %q, %v", got, err)
	}
	launcher := filepath.Join(root, "bin", "aact")
	launcherBefore, err := launcherContents(launcher)
	if err != nil {
		t.Fatal(err)
	}
	withoutForceOutput, err := runLocalInstaller(t, root, archiveB)
	if err == nil || !strings.Contains(withoutForceOutput, "Existing version directory differs") {
		t.Fatalf("different archive replaced release without force: %q, %v", withoutForceOutput, err)
	}
	if got, err := os.ReadFile(manifest); err != nil || string(got) != "snapshot A" {
		t.Fatalf("failed update changed existing release: %q, %v", got, err)
	}
	launcherAfterFailure, err := launcherContents(launcher)
	if err != nil || launcherAfterFailure != launcherBefore {
		t.Fatalf("failed update changed launcher: before=%q after=%q err=%v", launcherBefore, launcherAfterFailure, err)
	}
	forceOutput, err := runLocalInstallerForce(t, root, archiveB)
	if err != nil {
		t.Fatalf("force install archive B: %s: %v", forceOutput, err)
	}
	if got, err := os.ReadFile(manifest); err != nil || string(got) != "snapshot B" {
		t.Fatalf("forced release content = %q, %v", got, err)
	}
	launcherAfterForce, err := launcherContents(launcher)
	if err != nil || launcherAfterForce != launcherBefore {
		t.Fatalf("launcher no longer resolves through same version path: before=%q after=%q err=%v", launcherBefore, launcherAfterForce, err)
	}
}

func TestInstallerForceRestoresPreviousReleaseIfLauncherSwitchFails(t *testing.T) {
	root := t.TempDir()
	version := "0.5.1-SNAPSHOT-feature-aact-remediation-pr"
	archiveA := writeLocalInstallerArchive(t, version, "snapshot A")
	archiveB := writeLocalInstallerArchive(t, version, "snapshot B")
	if out, err := runLocalInstaller(t, root, archiveA); err != nil {
		t.Fatalf("install archive A: %s: %v", out, err)
	}
	release := filepath.Join(root, "releases", version+"-"+installerPlatform()+"-amd64")
	manifest := filepath.Join(release, "packages", "sample", "SKILL.md")
	launcher := filepath.Join(root, "bin", "aact")
	var unrelated string
	if runtime.GOOS == "windows" {
		launcher += ".cmd"
		if err := os.Remove(launcher); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(launcher, 0700); err != nil {
			t.Fatal(err)
		}
	} else {
		if err := os.Remove(launcher); err != nil {
			t.Fatal(err)
		}
		unrelated = filepath.Join(t.TempDir(), "unrelated-aact")
		if err := os.WriteFile(unrelated, []byte("unrelated"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(unrelated, launcher); err != nil {
			t.Fatal(err)
		}
	}
	output, err := runLocalInstallerForce(t, root, archiveB)
	if err == nil {
		t.Fatalf("invalid launcher did not fail staged update: %q", output)
	}
	if runtime.GOOS != "windows" && !strings.Contains(output, "unrelated symlink") {
		t.Fatalf("unexpected POSIX launcher failure: %q, %v", output, err)
	}
	if got, err := os.ReadFile(manifest); err != nil || string(got) != "snapshot A" {
		t.Fatalf("failed forced update did not restore prior release: %q, %v", got, err)
	}
	if runtime.GOOS != "windows" {
		if got, err := os.Readlink(launcher); err != nil || got != unrelated {
			t.Fatalf("failed forced update changed unrelated launcher: %q, %v", got, err)
		}
	}
}

func writeLocalInstallerArchive(t *testing.T, version, marker string) string {
	t.Helper()
	archive := installerArchiveWithMarker(t, marker)
	archiveDir := t.TempDir()
	extension := "tar.gz"
	if installerPlatform() == "windows" {
		extension = "zip"
	}
	filename := fmt.Sprintf("aact_%s_%s_amd64.%s", version, installerPlatform(), extension)
	archivePath := filepath.Join(archiveDir, filename)
	if err := os.WriteFile(archivePath, archive, 0600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(archive)
	if err := os.WriteFile(filepath.Join(archiveDir, "checksums.txt"), []byte(fmt.Sprintf("%x  %s\n", sum, filename)), 0600); err != nil {
		t.Fatal(err)
	}
	return archivePath
}

func TestInstallerRejectsLocalArchiveWithMissingOrInvalidChecksum(t *testing.T) {
	for _, tc := range []struct {
		name      string
		checksum  string
		wantError string
	}{{"missing", "", "checksum"}, {"mismatch", strings.Repeat("0", 64), "checksum mismatch"}} {
		t.Run(tc.name, func(t *testing.T) {
			archive := installerArchive(t)
			archiveDir := t.TempDir()
			extension := "tar.gz"
			if installerPlatform() == "windows" {
				extension = "zip"
			}
			filename := fmt.Sprintf("aact_1.2.3_%s_amd64.%s", installerPlatform(), extension)
			archivePath := filepath.Join(archiveDir, filename)
			if err := os.WriteFile(archivePath, archive, 0600); err != nil {
				t.Fatal(err)
			}
			if tc.checksum != "" {
				if err := os.WriteFile(filepath.Join(archiveDir, "checksums.txt"), []byte(tc.checksum+"  "+filename+"\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			out, err := runLocalInstaller(t, t.TempDir(), archivePath)
			if err == nil || !strings.Contains(strings.ToLower(out), tc.wantError) {
				t.Fatalf("invalid local checksum accepted: %q, %v", out, err)
			}
		})
	}
}

func TestInstallerRequiresExplicitReleaseVersionWhenNoLocalArchiveIsProvided(t *testing.T) {
	cmd := exec.Command("/bin/sh", "../../install.sh", "--install-dir", t.TempDir())
	if runtime.GOOS == "windows" {
		cmd = exec.Command(powerShell(t), "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", "../../install.ps1", "-InstallDir", t.TempDir())
	}
	cmd.Env = append(os.Environ(), "AACT_OS=linux", "AACT_ARCH=amd64", "HOME="+t.TempDir(), "AACT_VERSION=", "AACT_DOWNLOAD_BASE=http://127.0.0.1:1/should-not-be-used")
	out, err := cmd.CombinedOutput()
	want := "specify --version or --local-archive"
	if runtime.GOOS == "windows" {
		want = "specify -version or -localarchive"
	}
	if err == nil || !strings.Contains(strings.ToLower(strings.ReplaceAll(string(out), " ", "")), strings.ReplaceAll(want, " ", "")) {
		t.Fatalf("installer did not explain how to select a release: %q, %v", out, err)
	}
}

func TestInstallerRejectsLocalArchiveForAnotherPlatform(t *testing.T) {
	archiveDir := t.TempDir()
	filename := "aact_1.2.3-windows_windows_amd64.zip"
	if installerPlatform() == "windows" {
		filename = "aact_1.2.3-linux_linux_amd64.tar.gz"
	}
	archivePath := filepath.Join(archiveDir, filename)
	if err := os.WriteFile(archivePath, []byte("not a platform archive"), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := runLocalInstaller(t, t.TempDir(), archivePath)
	if err == nil || !strings.Contains(strings.ToLower(out), "does not match this platform") {
		t.Fatalf("wrong-platform archive accepted: %q, %v", out, err)
	}
}

func TestInstallerRejectsVersionThatDoesNotMatchLocalArchive(t *testing.T) {
	archive := installerArchive(t)
	archiveDir := t.TempDir()
	extension := "tar.gz"
	if installerPlatform() == "windows" {
		extension = "zip"
	}
	filename := fmt.Sprintf("aact_0.5.1-SNAPSHOT-feature-aact-remediation-pr_%s_amd64.%s", installerPlatform(), extension)
	archivePath := filepath.Join(archiveDir, filename)
	if err := os.WriteFile(archivePath, archive, 0600); err != nil {
		t.Fatal(err)
	}
	out, err := runLocalInstallerVersion(t, t.TempDir(), archivePath, "1.2.4")
	if err == nil || !strings.Contains(strings.ToLower(out), "does not match local archive version") {
		t.Fatalf("conflicting version accepted: %q, %v", out, err)
	}
}

func runLocalInstaller(t *testing.T, root, archive string) (string, error) {
	return runLocalInstallerVersionForce(t, root, archive, "", false)
}

func runLocalInstallerVersion(t *testing.T, root, archive, version string) (string, error) {
	return runLocalInstallerVersionForce(t, root, archive, version, false)
}

func runLocalInstallerForce(t *testing.T, root, archive string) (string, error) {
	return runLocalInstallerVersionForce(t, root, archive, "", true)
}

func runLocalInstallerVersionForce(t *testing.T, root, archive, version string, force bool) (string, error) {
	t.Helper()
	args := []string{"--local-archive", archive, "--install-dir", root}
	if version != "" {
		args = append(args, "--version", version)
	}
	if force {
		args = append(args, "--force")
	}
	cmd := exec.Command("/bin/sh", append([]string{"../../install.sh"}, args...)...)
	if runtime.GOOS == "windows" {
		windowsArgs := []string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-File", "../../install.ps1", "-LocalArchive", archive, "-InstallDir", root}
		if version != "" {
			windowsArgs = append(windowsArgs, "-Version", version)
		}
		if force {
			windowsArgs = append(windowsArgs, "-Force")
		}
		cmd = exec.Command(powerShell(t), windowsArgs...)
	}
	cmd.Env = append(os.Environ(), "AACT_OS=linux", "AACT_ARCH=amd64", "HOME="+t.TempDir(), "AACT_DOWNLOAD_BASE=http://127.0.0.1:1/should-not-be-used")
	out, err := cmd.CombinedOutput()
	return string(out), err
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

func TestUpdateWithSymlinkedInstallDir(t *testing.T) {
	base := t.TempDir()
	realRoot := filepath.Join(base, "real-root")
	rootAlias := filepath.Join(base, "root-alias")
	if err := os.Mkdir(realRoot, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realRoot, rootAlias); err != nil {
		t.Fatal(err)
	}
	s := fixtureServer(t, false)
	if out, err := runInstaller(t, rootAlias, "1.2.3", s.URL); err != nil {
		t.Fatalf("initial install: %s: %v", out, err)
	}
	if out, err := runInstaller(t, rootAlias, "1.2.4", s.URL); err != nil {
		t.Fatalf("upgrade through symlinked install dir: %s: %v", out, err)
	}
	if _, err := os.Stat(filepath.Join(realRoot, "releases", "1.2.3-"+installerPlatform()+"-amd64", "packages", "sample", "SKILL.md")); err != nil {
		t.Fatalf("prior release resource was not preserved: %v", err)
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

func TestInstallerMigratesRecognizedLegacyBinLauncher(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix launcher migration")
	}
	home := t.TempDir()
	legacy := filepath.Join(home, ".local", "bin", "aact")
	if err := os.MkdirAll(filepath.Dir(legacy), 0700); err != nil {
		t.Fatal(err)
	}
	old := []byte(`#!/bin/sh
if [ "$1" = "--version" ]; then printf "0.0.0-SNAPSHOT-old\n"; else printf "Another Agent Capability Toolkit (aact)\n"; fi
`)
	if err := os.WriteFile(legacy, old, 0755); err != nil {
		t.Fatal(err)
	}
	canonical := filepath.Join(home, ".local", "share", "aact", "bin", "aact")
	if err := os.MkdirAll(filepath.Dir(canonical), 0700); err != nil {
		t.Fatal(err)
	}
	canonicalOld := []byte(`#!/bin/sh
if [ "$1" = "--version" ]; then printf "0.0.0-SNAPSHOT-canonical\n"; else printf "Another Agent Capability Toolkit (aact)\n"; fi
`)
	if err := os.WriteFile(canonical, canonicalOld, 0755); err != nil {
		t.Fatal(err)
	}
	stateRoot := filepath.Join(home, ".local", "state", "agent-skills")
	generated := filepath.Join(stateRoot, "generated", "skill-output")
	if err := os.MkdirAll(generated, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(generated, "SKILL.md"), []byte("managed skill"), 0600); err != nil {
		t.Fatal(err)
	}
	agentLink := filepath.Join(home, ".agents", "skills", "sample")
	if err := os.MkdirAll(filepath.Dir(agentLink), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(generated, agentLink); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(stateRoot, "manager", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0700); err != nil {
		t.Fatal(err)
	}
	settingsData := []byte("{\"agents\":[\"codex\"],\"image_source\":\"local\"}")
	defaultPack := filepath.Join(stateRoot, "manager", "default-pack")
	defaultPackData := []byte("/home/example/dso-skills/aact.toml\n")
	if err := os.WriteFile(defaultPack, defaultPackData, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, settingsData, 0600); err != nil {
		t.Fatal(err)
	}
	sources := filepath.Join(stateRoot, "manager", "sources.json")
	if err := os.WriteFile(sources, []byte("[{\"id\":\"dso-skills\",\"manifest_path\":\"/home/example/dso-skills/aact.toml\"}]"), 0600); err != nil {
		t.Fatal(err)
	}
	server := fixtureServer(t, false)
	cmd := exec.Command("/bin/sh", "../../install.sh", "--version", "1.2.3", "--base-url", server.URL)
	cmd.Env = append(os.Environ(), "HOME="+home, "AACT_OS=linux", "AACT_ARCH=amd64")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("install failed: %s: %v", output, err)
	}
	target, err := os.Readlink(legacy)
	if err != nil {
		t.Fatalf("legacy launcher was not migrated: %q %v", output, err)
	}
	want := filepath.Join(home, ".local", "share", "aact", "bin", "aact")
	if target != want {
		t.Fatalf("legacy launcher target = %q, want %q", target, want)
	}
	backup := filepath.Join(home, ".local", "share", "aact", "legacy", "bin", "aact-0.0.0-SNAPSHOT-old")
	if got, err := os.ReadFile(backup); err != nil || string(got) != string(old) {
		t.Fatalf("legacy executable backup = %q, %v", got, err)
	}
	canonicalBackup := filepath.Join(home, ".local", "share", "aact", "legacy", "bin", "aact-canonical-0.0.0-SNAPSHOT-canonical")
	if got, err := os.ReadFile(canonicalBackup); err != nil || string(got) != string(canonicalOld) {
		t.Fatalf("canonical launcher backup = %q, %v", got, err)
	}
	if !strings.Contains(string(output), "Migrated legacy launcher") {
		t.Fatalf("migration was not reported: %s", output)
	}
	if target, err := os.Readlink(agentLink); err != nil || target != generated {
		t.Fatalf("managed agent link changed: %q %v", target, err)
	}
	if got, err := os.ReadFile(settings); err != nil || string(got) != string(settingsData) {
		t.Fatalf("saved settings changed: %q %v", got, err)
	}
	if got, err := os.ReadFile(defaultPack); err != nil || string(got) != string(defaultPackData) {
		t.Fatalf("saved default pack changed: %q %v", got, err)
	}
	if _, err := os.Stat(sources); err != nil {
		t.Fatalf("saved capability references removed: %v", err)
	}
}

func TestInstallerLeavesUnrecognizedLegacyLauncherUntouched(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix launcher migration")
	}
	home := t.TempDir()
	legacy := filepath.Join(home, ".local", "bin", "aact")
	if err := os.MkdirAll(filepath.Dir(legacy), 0700); err != nil {
		t.Fatal(err)
	}
	old := []byte("#!/bin/sh\nprintf 'not-aact\\n'\n")
	if err := os.WriteFile(legacy, old, 0755); err != nil {
		t.Fatal(err)
	}
	server := fixtureServer(t, false)
	cmd := exec.Command("/bin/sh", "../../install.sh", "--version", "1.2.3", "--base-url", server.URL)
	cmd.Env = append(os.Environ(), "HOME="+home, "AACT_OS=linux", "AACT_ARCH=amd64")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("install failed: %s: %v", output, err)
	}
	got, err := os.ReadFile(legacy)
	if err != nil || string(got) != string(old) {
		t.Fatalf("unrecognized launcher changed: %q, %v", got, err)
	}
	if !strings.Contains(string(output), "not recognized") {
		t.Fatalf("unrecognized launcher was not reported: %s", output)
	}
}
