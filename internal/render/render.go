package render

import (
	"context"
	"errors"
	"fmt"
	"github.com/cbroglie/mustache"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type Renderer struct {
	Generator *Generator
	Profile   *config.Profile
}

func Stage(ctx context.Context, p catalog.Package, inputs map[string]any, target config.Target, parent string) (string, error) {
	return (Renderer{}).Stage(ctx, p, inputs, target, parent)
}
func (r Renderer) Stage(ctx context.Context, p catalog.Package, inputs map[string]any, target config.Target, parent string) (staging string, err error) {
	if p.Skill == nil {
		return "", errors.New("package has no skill")
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	if err = os.MkdirAll(parent, 0700); err != nil {
		return "", err
	}
	staging, err = os.MkdirTemp(parent, ".aact-stage-*")
	if err != nil {
		return "", err
	}
	stagePath := staging
	defer func() {
		if err != nil {
			os.RemoveAll(stagePath)
		}
	}()
	for _, name := range p.Skill.Files {
		src, e := contained(p.Dir, name, true)
		if e != nil {
			return "", e
		}
		if forbidden(name) {
			continue
		}
		dest, e := contained(staging, name, false)
		if e != nil {
			return "", e
		}
		if e = CopyTree(ctx, src, dest); e != nil {
			return "", e
		}
	}
	// Plain skills still produce a complete installable stage.
	if len(p.Templates) == 0 {
		src, e := contained(p.Dir, "SKILL.md", true)
		if e != nil {
			return "", e
		}
		if e = CopyTree(ctx, src, filepath.Join(staging, "SKILL.md")); e != nil {
			return "", e
		}
	}
	g := r.Generator
	if g == nil {
		g = &Generator{}
	}
	var generated map[string]any
	var e error
	if r.Profile != nil {
		generated, e = g.GenerateProfile(ctx, p, inputs, *r.Profile, staging)
	} else {
		generated, e = g.Generate(ctx, p, inputs, target, staging)
	}
	if e != nil {
		return "", e
	}
	data := map[string]any{"inputs": inputs, "generated": generated}
	seen := map[string]bool{}
	for _, t := range p.Templates {
		if e = ctx.Err(); e != nil {
			return "", e
		}
		src, e := contained(p.Dir, t.Source, true)
		if e != nil {
			return "", e
		}
		dest, e := contained(staging, t.Destination, false)
		if e != nil {
			return "", e
		}
		if forbidden(t.Destination) {
			return "", fmt.Errorf("template destination %q is a source/manifest file", t.Destination)
		}
		if seen[dest] {
			return "", fmt.Errorf("duplicate template destination %q", t.Destination)
		}
		seen[dest] = true
		b, e := os.ReadFile(src)
		if e != nil {
			return "", e
		}
		rendered, e := mustache.Render(string(b), data)
		if e != nil {
			return "", e
		}
		if e = os.MkdirAll(filepath.Dir(dest), 0755); e != nil {
			return "", e
		}
		if e = os.WriteFile(dest, []byte(rendered), 0644); e != nil {
			return "", e
		}
	}
	info, e := os.Stat(filepath.Join(staging, "SKILL.md"))
	if e != nil || !info.Mode().IsRegular() {
		return "", errors.New("rendered skill must contain a regular SKILL.md")
	}
	return staging, nil
}
func forbidden(name string) bool {
	base := filepath.Base(name)
	return base == "package.toml" || strings.HasSuffix(base, ".mustache") || strings.HasSuffix(base, ".tmpl")
}
func contained(root, name string, existing bool) (string, error) {
	// Reject both native and other-platform escape spellings, even on Unix.
	if name == "" || filepath.IsAbs(name) || strings.HasPrefix(name, "\\") || strings.Contains(name, "\\") || (len(name) > 1 && name[1] == ':') {
		return "", fmt.Errorf("unsafe package path %q", name)
	}
	clean := filepath.Clean(name)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe package path %q", name)
	}
	abs, e := filepath.Abs(root)
	if e != nil {
		return "", e
	}
	path := filepath.Join(abs, clean)
	if existing {
		resolved, e := filepath.EvalSymlinks(path)
		if e != nil {
			return "", e
		}
		resolvedRoot, e := filepath.EvalSymlinks(abs)
		if e != nil {
			return "", e
		}
		rel, e := filepath.Rel(resolvedRoot, resolved)
		if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("package resource %q escapes source", name)
		}
	}

	if !existing {
		ancestor := path
		for {
			_, statErr := os.Lstat(ancestor)
			if statErr == nil {
				break
			}
			if !os.IsNotExist(statErr) {
				return "", statErr
			}
			parent := filepath.Dir(ancestor)
			if parent == ancestor {
				return "", errors.New("resource parent does not exist")
			}
			ancestor = parent
		}
		resolved, e := filepath.EvalSymlinks(ancestor)
		if e != nil {
			return "", e
		}
		resolvedRoot, e := filepath.EvalSymlinks(abs)
		if e != nil {
			return "", e
		}
		rel, e := filepath.Rel(resolvedRoot, resolved)
		if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("package destination %q escapes root", name)
		}
	}
	return path, nil
}

// CopyTree copies regular files and directories without following symlinks.
// Template sources and manifests are excluded from supporting resources.
func CopyTree(ctx context.Context, source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("resource symlink is not supported: %s", path)
		}
		rel, e := filepath.Rel(source, path)
		if e != nil {
			return e
		}
		if forbidden(entry.Name()) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		dest := destination
		if rel != "." {
			dest = filepath.Join(destination, rel)
		}
		info, e := entry.Info()
		if e != nil {
			return e
		}
		if entry.IsDir() {
			return os.MkdirAll(dest, info.Mode().Perm())
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("non-regular package resource %s", path)
		}
		if e = os.MkdirAll(filepath.Dir(dest), 0755); e != nil {
			return e
		}
		in, e := os.Open(path)
		if e != nil {
			return e
		}
		defer in.Close()
		out, e := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
		if e != nil {
			return e
		}
		_, e = io.Copy(out, in)
		closeErr := out.Close()
		if e != nil {
			return e
		}
		return closeErr
	})
}
