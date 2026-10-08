package install

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

func treeDigest(ctx context.Context, root string) (string, error) {
	h := sha256.New()
	err := filepath.WalkDir(root, func(path string, e fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if e.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("skill resource symlink unsupported: %s", path)
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		base := e.Name()
		if base == "package.toml" || filepath.Ext(base) == ".mustache" || filepath.Ext(base) == ".tmpl" {
			if e.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := e.Info()
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%s\x00%d\x00", filepath.ToSlash(rel), info.Mode().Perm())
		if e.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported skill resource %s", path)
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		_, err = io.Copy(h, f)
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		h.Write([]byte{0})
		return nil
	})
	return hex.EncodeToString(h.Sum(nil)), err
}
func verifyOwned(ctx context.Context, i state.Installation) error {
	info, err := os.Lstat(i.Destination)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	switch i.Mode {
	case "symlink":
		if info.Mode()&os.ModeSymlink == 0 {
			return fmt.Errorf("managed link replaced at %s", i.Destination)
		}
		target, err := os.Readlink(i.Destination)
		if err != nil {
			return err
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(i.Destination), target)
		}
		if filepath.Clean(target) != filepath.Clean(i.SourcePath) {
			return fmt.Errorf("managed link changed at %s", i.Destination)
		}
	case "copy":
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("managed copy replaced at %s", i.Destination)
		}
		digest, err := treeDigest(ctx, i.Destination)
		if err != nil {
			return err
		}
		if digest != i.Digest {
			return fmt.Errorf("managed copy edited at %s", i.Destination)
		}
	default:
		return fmt.Errorf("unknown skill install mode %q", i.Mode)
	}
	return nil
}

// VerifyInstalled checks actual content as well as the owned destination.
// Callers separately report absent paths rather than inferring presence here.
func VerifyInstalled(ctx context.Context, row state.Installation) error {
	if err := verifyOwned(ctx, row); err != nil {
		return err
	}
	source, err := filepath.EvalSymlinks(row.Destination)
	if err != nil {
		return err
	}
	digest, err := treeDigest(ctx, source)
	if err != nil {
		return err
	}
	if row.Digest != "" && digest != row.Digest {
		return fmt.Errorf("managed skill content changed at %s", row.Destination)
	}
	return nil
}
