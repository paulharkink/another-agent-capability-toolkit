package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/mod/semver"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
)

// ImageVariantSpec contains resolved, non-secret build inputs for one selected CLI version.
type ImageVariantSpec struct {
	PackageRoot  string `json:"package_root"`
	Dockerfile   string `json:"dockerfile"`
	Context      string `json:"context"`
	Version      string `json:"version"`
	VersionArg   string `json:"version_arg"`
	BaseImageArg string `json:"base_image_arg"`
}

var buildArgNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func validateVariantPaths(root, dockerfile, contextDir string) error {
	if root == "" || dockerfile == "" || contextDir == "" {
		return errors.New("image variant package paths are required")
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	rootResolved, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return fmt.Errorf("resolve image variant package: %w", err)
	}
	for label, candidate := range map[string]string{"Dockerfile": dockerfile, "context": contextDir} {
		abs, err := filepath.Abs(candidate)
		if err != nil {
			return err
		}
		resolved, err := filepath.EvalSymlinks(abs)
		if err != nil {
			return fmt.Errorf("resolve image variant %s: %w", label, err)
		}
		rel, err := filepath.Rel(rootResolved, resolved)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			return fmt.Errorf("image variant %s escapes package directory", label)
		}
		info, err := os.Stat(resolved)
		if err != nil {
			return err
		}
		if label == "Dockerfile" && !info.Mode().IsRegular() {
			return errors.New("image variant Dockerfile must be a regular file")
		}
		if label == "context" && !info.IsDir() {
			return errors.New("image variant context must be a directory")
		}
	}
	return nil
}

func imageVariantContextHash(root string) (string, error) {
	type contextEntry struct {
		path string
		mode os.FileMode
		dir  bool
	}
	var entries []contextEntry
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("image variant context contains symlink %q", path)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !entry.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("image variant context contains unsupported file %q", path)
		}
		entries = append(entries, contextEntry{path: path, mode: info.Mode().Perm(), dir: entry.IsDir()})
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Slice(entries, func(i, j int) bool {
		relI, _ := filepath.Rel(root, entries[i].path)
		relJ, _ := filepath.Rel(root, entries[j].path)
		return filepath.ToSlash(relI) < filepath.ToSlash(relJ)
	})
	h := sha256.New()
	for _, entry := range entries {
		rel, _ := filepath.Rel(root, entry.path)
		kind := byte('f')
		if entry.dir {
			kind = 'd'
		}
		_, _ = h.Write([]byte(filepath.ToSlash(rel) + "\x00"))
		mode := uint16(entry.mode.Perm())
		_, _ = h.Write([]byte{kind, byte(mode >> 8), byte(mode)})
		if entry.dir {
			continue
		}
		data, err := os.ReadFile(entry.path)
		if err != nil {
			return "", err
		}
		_, _ = h.Write(data)
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func imageVariantBuildContextHash(packageRoot, dockerfile, contextDir string) (string, error) {
	contextHash, err := imageVariantContextHash(contextDir)
	if err != nil {
		return "", err
	}
	recipe, err := os.ReadFile(dockerfile)
	if err != nil {
		return "", err
	}
	relativeRecipe, err := filepath.Rel(packageRoot, dockerfile)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	_, _ = h.Write([]byte(filepath.ToSlash(relativeRecipe) + "\x00"))
	_, _ = h.Write(recipe)
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(contextHash))
	return hex.EncodeToString(h.Sum(nil)), nil
}

func variantKey(base, version, contextHash, platform string) string {
	h := sha256.Sum256([]byte(strings.Join([]string{base, version, contextHash, platform}, "\x00")))
	return hex.EncodeToString(h[:])
}

func (r *Runtime) prepareImageVariant(ctx context.Context, key state.Key, spec *RunSpec) error {
	v := spec.ImageVariant
	if !semver.IsValid(v.Version) {
		return fmt.Errorf("invalid selected MCP image variant version %q", v.Version)
	}
	if !buildArgNamePattern.MatchString(v.VersionArg) || !buildArgNamePattern.MatchString(v.BaseImageArg) || v.VersionArg == v.BaseImageArg {
		return errors.New("invalid MCP image variant build argument names")
	}
	if err := validateVariantPaths(v.PackageRoot, v.Dockerfile, v.Context); err != nil {
		return err
	}
	contentHash, err := imageVariantBuildContextHash(v.PackageRoot, v.Dockerfile, v.Context)
	if err != nil {
		return err
	}
	baseImage := spec.Image
	if spec.BuildContext != "" {
		baseImage = "aact/" + key.ID()[:24] + ":base"
		if _, err := r.run(ctx, []string{"build", "--tag", baseImage, spec.BuildContext}); err != nil {
			return err
		}
	} else {
		if baseImage == "" {
			return errors.New("MCP image variant requires a base image or build context")
		}
		if _, err := r.run(ctx, []string{"pull", baseImage}); err != nil {
			return err
		}
	}
	baseID, err := r.run(ctx, []string{"image", "inspect", "--format", "{{.Id}}", baseImage})
	if err != nil {
		return fmt.Errorf("resolve MCP image variant base image: %w", err)
	}
	baseIDText := strings.TrimSpace(string(baseID))
	if baseIDText == "" {
		return errors.New("Docker returned an empty MCP image variant base ID")
	}
	platformBytes, err := r.run(ctx, []string{"info", "--format", "{{.OSType}}/{{.Architecture}}"})
	if err != nil {
		return fmt.Errorf("resolve Docker platform for MCP image variant: %w", err)
	}
	platform := strings.TrimSpace(string(platformBytes))
	if platform == "" {
		return errors.New("Docker returned an empty platform for MCP image variant")
	}
	keyValue := variantKey(baseIDText, v.Version, contentHash, platform)
	derived := "aact/" + key.ID()[:24] + ":variant-" + keyValue[:32]
	if _, err := r.run(ctx, []string{"image", "inspect", "--format", "{{.Id}}", derived}); err != nil {
		args := []string{"build", "--tag", derived, "--file", v.Dockerfile, "--build-arg", v.BaseImageArg + "=" + baseIDText, "--build-arg", v.VersionArg + "=" + v.Version, v.Context}
		if _, err := r.run(ctx, args); err != nil {
			return fmt.Errorf("build MCP image variant: %w", err)
		}
	}
	spec.Image = derived
	spec.BuildContext = ""
	spec.ImageVariant = nil
	spec.ImageVariantKey = keyValue
	return nil
}
