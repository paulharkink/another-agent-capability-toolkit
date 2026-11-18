package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/process"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/releasecatalog"
)

func runVersionCommand(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(out, Version)
		return 0
	}
	switch args[0] {
	case "list":
		if len(args) != 1 {
			fmt.Fprintln(errOut, "usage: aact version list")
			return 2
		}
		releases, err := releasecatalog.List(ctx)
		if err != nil {
			fmt.Fprintln(errOut, "could not query AACT releases:", err)
			return 1
		}
		if len(releases) == 0 {
			fmt.Fprintln(out, "No stable releases found.")
			return 0
		}
		for i, release := range releases {
			label := strings.TrimPrefix(release.Tag, "v")
			if i == 0 {
				label += " (latest)"
			}
			fmt.Fprintln(out, label)
		}
		return 0
	case "upgrade", "install":
		if args[0] == "upgrade" && len(args) != 1 {
			fmt.Fprintln(errOut, "usage: aact version upgrade")
			return 2
		}
		if args[0] == "install" && len(args) != 2 {
			fmt.Fprintln(errOut, "usage: aact version install VERSION")
			return 2
		}
		releases, err := releasecatalog.List(ctx)
		if err != nil {
			fmt.Fprintln(errOut, "could not query AACT releases:", err)
			return 1
		}
		var selected *releasecatalog.Release
		if args[0] == "upgrade" {
			if len(releases) == 0 {
				fmt.Fprintln(errOut, "no stable AACT releases are available")
				return 1
			}
			selected = &releases[0]
		} else {
			version := strings.TrimPrefix(args[1], "v")
			if !releasecatalog.StableVersion("v" + version) {
				fmt.Fprintln(errOut, "invalid stable release version")
				return 2
			}
			for _, release := range releases {
				if release.Tag == "v"+version {
					selected = &release
					break
				}
			}
			if selected == nil {
				fmt.Fprintf(errOut, "stable release %s is unavailable\n", version)
				return 1
			}
		}
		version := strings.TrimPrefix(selected.Tag, "v")
		if err := installPublishedVersion(ctx, *selected, releases); err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
		fmt.Fprintf(out, "Installed AACT %s.\n", version)
		return 0
	default:
		fmt.Fprintln(errOut, "usage: aact version [list | upgrade | install VERSION]")
		return 2
	}
}

func installPublishedVersion(ctx context.Context, selected releasecatalog.Release, releases []releasecatalog.Release) error {
	version := strings.TrimPrefix(selected.Tag, "v")
	root, explicitRoot, err := resolveInstallRoot()
	if err != nil {
		return err
	}
	target, err := installerTarget()
	if err != nil {
		return err
	}
	installerName := "install.sh"
	if runtime.GOOS == "windows" {
		installerName = "install.ps1"
	}
	var archiveAsset, checksumsAsset *releasecatalog.Asset
	for _, asset := range selected.Assets {
		asset := asset
		switch asset.Name {
		case "checksums.txt":
			checksumsAsset = &asset
		default:
			if strings.HasPrefix(asset.Name, "aact_"+version+"_"+target+".") {
				archiveAsset = &asset
			}
		}
	}
	if archiveAsset == nil || checksumsAsset == nil {
		return fmt.Errorf("requested release %s is missing the platform archive or checksums.txt for %s (archive=%t checksums=%t)", version, target, archiveAsset != nil, checksumsAsset != nil)
	}
	stage, err := os.MkdirTemp("", "aact-release-*")
	if err != nil {
		return fmt.Errorf("stage AACT release: %w", err)
	}
	defer os.RemoveAll(stage)
	installerPath, err := localInstaller(installerName)
	if errors.Is(err, os.ErrNotExist) {
		installerPath, err = downloadBootstrapInstaller(ctx, releases, installerName, filepath.Join(stage, installerName))
	}
	if err != nil {
		return fmt.Errorf("locate a compatible AACT installer: %w", err)
	}
	archivePath, err := downloadVerifiedAsset(ctx, *archiveAsset, filepath.Join(stage, archiveAsset.Name), 128<<20)
	if err != nil {
		return fmt.Errorf("download verified release archive: %w", err)
	}
	if _, err := downloadVerifiedAsset(ctx, *checksumsAsset, filepath.Join(stage, "checksums.txt"), 1<<20); err != nil {
		return fmt.Errorf("download verified release checksums: %w", err)
	}
	var command *exec.Cmd
	if runtime.GOOS == "windows" {
		powershell := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
		args := []string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-File", installerPath, "-Version", version, "-LocalArchive", archivePath}
		if explicitRoot {
			args = append(args, "-InstallDir", root)
		}
		command = exec.CommandContext(ctx, powershell, args...)
	} else {
		args := []string{installerPath, "--version", version, "--local-archive", archivePath}
		if explicitRoot {
			args = append(args, "--install-dir", root)
		}
		command = exec.CommandContext(ctx, "/bin/sh", args...)
	}
	command.Env = installerEnvironment()
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("install AACT %s: %w: %s", version, err, strings.TrimSpace(string(output)))
	}
	return nil
}

func downloadBootstrapInstaller(ctx context.Context, releases []releasecatalog.Release, installerName, destination string) (string, error) {
	var failures []string
	for _, release := range releases {
		version := strings.TrimPrefix(release.Tag, "v")
		if !releasecatalog.StableVersion(release.Tag) || (version != minimumLocalArchiveInstallerVersion && !releasecatalog.NewerThan(version, minimumLocalArchiveInstallerVersion)) {
			continue
		}
		var candidate *releasecatalog.Asset
		for i := range release.Assets {
			if release.Assets[i].Name == installerName {
				candidate = &release.Assets[i]
				break
			}
		}
		if candidate == nil {
			continue
		}
		if !hasSHA256ReleaseDigest(candidate.Digest) {
			failures = append(failures, release.Tag+" has no valid SHA-256 installer digest")
			continue
		}
		if _, err := releasecatalog.ValidateAssetURL(candidate.URL); err != nil {
			failures = append(failures, release.Tag+" installer URL rejected: "+err.Error())
			continue
		}
		path, err := downloadVerifiedAsset(ctx, *candidate, destination, 2<<20)
		if err != nil {
			failures = append(failures, release.Tag+" installer unavailable or failed digest verification: "+err.Error())
			continue
		}
		return path, nil
	}
	message := "no SHA-256 verified local-archive installer found in stable releases at or above v" + minimumLocalArchiveInstallerVersion
	if len(failures) > 0 {
		message += ": " + strings.Join(failures, "; ")
	}
	return "", errors.New(message)
}

const minimumLocalArchiveInstallerVersion = "0.5.1"

func hasSHA256ReleaseDigest(digest string) bool {
	if !strings.HasPrefix(digest, "sha256:") {
		return false
	}
	encoded := strings.TrimPrefix(digest, "sha256:")
	if len(encoded) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(encoded)
	return err == nil
}

func installerEnvironment() []string {
	filtered := process.EnvironmentWithoutVaultPassword(os.Environ())
	clean := make([]string, 0, len(filtered))
	powerShellModulePath := ""
	for _, entry := range filtered {
		name, value, _ := strings.Cut(entry, "=")
		switch strings.ToUpper(name) {
		case "AACT_ARCH", "AACT_OS", "AACT_VERSION", "AACT_DOWNLOAD_BASE":
			continue
		case "PSMODULEPATH":
			if runtime.GOOS == "windows" {
				powerShellModulePath = value
				continue
			}
		}
		clean = append(clean, entry)
	}
	if runtime.GOOS == "windows" {
		if systemRoot := os.Getenv("SystemRoot"); systemRoot != "" {
			// The installer runs Windows PowerShell 5.1 even when AACT's parent used PowerShell 7.
			windowsPowerShellModules := filepath.Join(systemRoot, "System32", "WindowsPowerShell", "v1.0", "Modules")
			if powerShellModulePath != "" {
				windowsPowerShellModules += string(os.PathListSeparator) + powerShellModulePath
			}
			clean = append(clean, "PSModulePath="+windowsPowerShellModules)
		} else if powerShellModulePath != "" {
			clean = append(clean, "PSModulePath="+powerShellModulePath)
		}
	}
	return clean
}

func downloadVerifiedAsset(ctx context.Context, asset releasecatalog.Asset, destination string, maxBytes int64) (string, error) {
	assetURL, err := releasecatalog.ValidateAssetURL(asset.URL)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(asset.Digest, "sha256:") || len(strings.TrimPrefix(asset.Digest, "sha256:")) != 64 {
		return "", fmt.Errorf("%s has no SHA-256 digest in release metadata", asset.Name)
	}
	expected, err := hex.DecodeString(strings.TrimPrefix(asset.Digest, "sha256:"))
	if err != nil {
		return "", fmt.Errorf("%s has an invalid SHA-256 digest", asset.Name)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, assetURL, nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 2 * time.Minute, CheckRedirect: func(request *http.Request, via []*http.Request) error {
		if len(via) >= 5 || !releasecatalog.IsAllowedAssetRedirect(request.URL.String()) {
			return errors.New("release asset redirect refused")
		}
		return nil
	}}
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil {
		return "", err
	}
	if int64(len(data)) > maxBytes {
		return "", fmt.Errorf("%s exceeds the %d-byte limit", asset.Name, maxBytes)
	}
	actual := sha256.Sum256(data)
	if !equalDigest(actual[:], expected) {
		return "", fmt.Errorf("%s SHA-256 does not match release metadata", asset.Name)
	}
	if err := os.WriteFile(destination, data, 0600); err != nil {
		return "", err
	}
	return destination, nil
}

func equalDigest(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var mismatch byte
	for i := range a {
		mismatch |= a[i] ^ b[i]
	}
	return mismatch == 0
}

func resolveInstallRoot() (root string, explicit bool, err error) {
	if configured, ok := os.LookupEnv("AACT_INSTALL_DIR"); ok {
		if strings.TrimSpace(configured) == "" {
			return "", false, errors.New("AACT_INSTALL_DIR is empty")
		}
		absolute, err := filepath.Abs(configured)
		return absolute, true, err
	}
	root, managed, err := managedInstallRootFromExecutable()
	if err != nil {
		return "", false, err
	}
	if !managed {
		return "", false, errors.New("cannot determine the managed AACT installation root from this executable; set AACT_INSTALL_DIR")
	}
	defaultRoot, err := defaultInstallRoot()
	if err != nil {
		return "", false, err
	}
	return root, !samePath(root, defaultRoot), nil
}

func managedInstallRootFromExecutable() (string, bool, error) {
	releaseDir, managed, err := managedReleaseDirFromExecutable()
	if err != nil || !managed {
		return "", managed, err
	}
	return filepath.Dir(filepath.Dir(releaseDir)), true, nil
}

func managedReleaseDirFromExecutable() (string, bool, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", false, fmt.Errorf("locate current AACT executable: %w", err)
	}
	if resolved, resolveErr := filepath.EvalSymlinks(executable); resolveErr == nil {
		executable = resolved
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return "", false, fmt.Errorf("resolve current AACT executable: %w", err)
	}
	if name := filepath.Base(executable); name != "aact" && name != "aact.exe" {
		return "", false, nil
	}
	bin := filepath.Dir(executable)
	releases := filepath.Dir(filepath.Dir(bin))
	if filepath.Base(bin) != "bin" || filepath.Base(releases) != "releases" {
		return "", false, nil
	}
	return filepath.Dir(bin), true, nil
}

func localInstaller(name string) (string, error) {
	if releaseDir, managed, err := managedReleaseDirFromExecutable(); err != nil {
		return "", err
	} else if managed {
		path := filepath.Join(releaseDir, name)
		info, err := os.Lstat(path)
		if err != nil {
			return "", err
		}
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("bundled installer is not a regular file: %s", path)
		}
		return path, nil
	}
	return "", os.ErrNotExist
}

func defaultInstallRoot() (string, error) {
	if runtime.GOOS == "windows" {
		local := os.Getenv("LOCALAPPDATA")
		if local == "" {
			return "", errors.New("LOCALAPPDATA is required to locate the default AACT installation")
		}
		return filepath.Join(local, "aact"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate user home for default AACT installation: %w", err)
	}
	return filepath.Join(home, ".local", "share", "aact"), nil
}

func samePath(a, b string) bool {
	left, leftErr := filepath.Abs(filepath.Clean(a))
	right, rightErr := filepath.Abs(filepath.Clean(b))
	if leftErr != nil || rightErr != nil {
		return false
	}
	if resolved, err := filepath.EvalSymlinks(left); err == nil {
		left = resolved
	}
	if resolved, err := filepath.EvalSymlinks(right); err == nil {
		right = resolved
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func installerTarget() (string, error) {
	arch := runtime.GOARCH
	if runtime.GOOS == "windows" {
		native := os.Getenv("PROCESSOR_ARCHITEW6432")
		if native == "" {
			native = os.Getenv("PROCESSOR_ARCHITECTURE")
		}
		switch strings.ToUpper(native) {
		case "AMD64", "X86_64", "X64":
			arch = "amd64"
		case "ARM64", "AARCH64":
			arch = "arm64"
		}
	}
	if arch != "amd64" && arch != "arm64" {
		return "", fmt.Errorf("unsupported AACT upgrade architecture: %s", arch)
	}
	osName := runtime.GOOS
	if osName != "darwin" && osName != "linux" && osName != "windows" {
		return "", fmt.Errorf("unsupported AACT upgrade platform: %s", osName)
	}
	return osName + "_" + arch, nil
}
