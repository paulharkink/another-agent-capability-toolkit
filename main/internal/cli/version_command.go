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
	"sort"
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
			platforms := releasePlatforms(release)
			fmt.Fprintf(out, "%s platforms: %s", label, strings.Join(platforms, ", "))
			for _, asset := range release.Assets {
				if asset.Name == "checksums.txt" && strings.HasPrefix(asset.Digest, "sha256:") {
					fmt.Fprint(out, "; SHA-256 checksums available")
					break
				}
			}
			fmt.Fprintln(out)
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
		version := ""
		if args[0] == "upgrade" {
			if len(releases) == 0 {
				fmt.Fprintln(errOut, "no stable AACT releases are available")
				return 1
			}
			version = strings.TrimPrefix(releases[0].Tag, "v")
		} else {
			version = strings.TrimPrefix(args[1], "v")
			if !releasecatalog.StableVersion("v" + version) {
				fmt.Fprintln(errOut, "invalid stable release version")
				return 2
			}
			found := false
			for _, release := range releases {
				if release.Tag == "v"+version {
					found = true
					break
				}
			}
			if !found {
				fmt.Fprintf(errOut, "stable release %s is unavailable\n", version)
				return 1
			}
		}
		if err := installPublishedVersion(ctx, version, releases); err != nil {
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

func releasePlatforms(release releasecatalog.Release) []string {
	var platforms []string
	for _, asset := range release.Assets {
		name := strings.TrimPrefix(asset.Name, "aact_"+strings.TrimPrefix(release.Tag, "v")+"_")
		for _, platform := range []string{"darwin_amd64", "darwin_arm64", "linux_amd64", "linux_arm64", "windows_amd64", "windows_arm64"} {
			if strings.HasPrefix(name, platform+".") {
				platforms = append(platforms, strings.Replace(platform, "_", "/", 1))
				break
			}
		}
	}
	sort.Strings(platforms)
	return platforms
}

func installPublishedVersion(ctx context.Context, version string, releases []releasecatalog.Release) error {
	var selected *releasecatalog.Release
	for i := range releases {
		if releases[i].Tag == "v"+version {
			selected = &releases[i]
			break
		}
	}
	if selected == nil {
		return fmt.Errorf("stable release %s is unavailable", version)
	}
	target, err := installerTarget()
	if err != nil {
		return err
	}
	installerName := "install.sh"
	if runtime.GOOS == "windows" {
		installerName = "install.ps1"
	}
	var installerAsset, archiveAsset, checksumsAsset *releasecatalog.Asset
	for _, asset := range selected.Assets {
		asset := asset
		switch asset.Name {
		case installerName:
			installerAsset = &asset
		case "checksums.txt":
			checksumsAsset = &asset
		default:
			if strings.HasPrefix(asset.Name, "aact_"+version+"_"+target+".") {
				archiveAsset = &asset
			}
		}
	}
	if installerAsset == nil || archiveAsset == nil || checksumsAsset == nil {
		return fmt.Errorf("release %s is missing the installer, platform archive, or checksums.txt for %s (installer=%t archive=%t checksums=%t)", version, target, installerAsset != nil, archiveAsset != nil, checksumsAsset != nil)
	}
	stage, err := os.MkdirTemp("", "aact-release-*")
	if err != nil {
		return fmt.Errorf("stage AACT release: %w", err)
	}
	defer os.RemoveAll(stage)
	installerPath, err := downloadVerifiedAsset(ctx, *installerAsset, filepath.Join(stage, installerName), 2<<20)
	if err != nil {
		return fmt.Errorf("download verified release installer: %w", err)
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
		command = exec.CommandContext(ctx, powershell, "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", installerPath, "-LocalArchive", archivePath, "-InstallDir", installRoot())
	} else {
		command = exec.CommandContext(ctx, "/bin/sh", installerPath, "--local-archive", archivePath, "--install-dir", installRoot())
	}
	command.Env = process.EnvironmentWithoutVaultPassword(os.Environ())
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("install AACT %s: %w: %s", version, err, strings.TrimSpace(string(output)))
	}
	return nil
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

func installRoot() string {
	if root := os.Getenv("AACT_INSTALL_DIR"); root != "" {
		return root
	}
	if runtime.GOOS == "windows" {
		return filepath.Join(os.Getenv("LOCALAPPDATA"), "aact")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "aact")
}

func installerTarget() (string, error) {
	arch := runtime.GOARCH
	if arch != "amd64" && arch != "arm64" {
		return "", fmt.Errorf("unsupported AACT upgrade architecture: %s", arch)
	}
	osName := runtime.GOOS
	if osName != "darwin" && osName != "linux" && osName != "windows" {
		return "", fmt.Errorf("unsupported AACT upgrade platform: %s", osName)
	}
	return osName + "_" + arch, nil
}
