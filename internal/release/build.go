package release

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/process"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type Artifact struct{ Path, SHA256 string }
type Builder struct {
	SourceDir, Go string
	Executor      process.Executor
}

var safeVersion = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func Build(ctx context.Context, version, goos, goarch, out string) (Artifact, error) {
	return (Builder{}).Build(ctx, version, goos, goarch, out)
}
func (b Builder) Build(ctx context.Context, version, goos, goarch, out string) (Artifact, error) {
	if !safeVersion.MatchString(version) {
		return Artifact{}, errors.New("invalid release version")
	}
	if (goos != "darwin" && goos != "linux" && goos != "windows") || (goarch != "amd64" && goarch != "arm64") {
		return Artifact{}, errors.New("unsupported release target")
	}
	if err := ctx.Err(); err != nil {
		return Artifact{}, err
	}
	root := b.SourceDir
	if root == "" {
		var err error
		root, err = os.Getwd()
		if err != nil {
			return Artifact{}, err
		}
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return Artifact{}, err
	}
	out, err = filepath.Abs(out)
	if err != nil {
		return Artifact{}, err
	}
	if err = os.MkdirAll(out, 0755); err != nil {
		return Artifact{}, err
	}
	temp, err := os.MkdirTemp(out, ".aact-build-*")
	if err != nil {
		return Artifact{}, err
	}
	defer os.RemoveAll(temp)
	tree := filepath.Join(temp, "tree")
	if err = os.Mkdir(tree, 0755); err != nil {
		return Artifact{}, err
	}
	if err = copyResources(ctx, filepath.Join(root, "packages"), filepath.Join(tree, "packages")); err != nil {
		return Artifact{}, err
	}
	for _, file := range []string{"README.md", "LICENSE", "THIRD_PARTY_NOTICES.md"} {
		if err = copyResources(ctx, filepath.Join(root, file), filepath.Join(tree, file)); err != nil {
			return Artifact{}, err
		}
	}
	tool := b.Go
	if tool == "" {
		tool = os.Getenv("AACT_GO")
	}
	if tool == "" {
		tool = "go"
	}
	runner := b.Executor
	if runner == nil {
		runner = process.OSExecutor{}
	}
	extension := ""
	if goos == "windows" {
		extension = ".exe"
	}
	if err = os.MkdirAll(filepath.Join(tree, "bin"), 0755); err != nil {
		return Artifact{}, err
	}
	programs := []struct{ name, pkg string }{{"aact", "./cmd/aact"}, {"inspector-helper", "./cmd/inspector-helper"}, {"find-session", "./cmd/find-session"}, {"repo-map", "./packages/git-repo-map/generators/repo-map"}}
	for _, program := range programs {
		dest := filepath.Join(tree, "bin", program.name+extension)
		args := []string{tool, "build", "-trimpath", "-buildvcs=false"}
		if program.name == "aact" {
			args = append(args, "-ldflags", "-X=github.com/paulharkink/another-agent-capability-toolkit/internal/cli.Version="+version)
		}
		args = append(args, "-o", dest, program.pkg)
		_, err = runner.Run(ctx, args, root, nil, map[string]string{"GOOS": goos, "GOARCH": goarch, "CGO_ENABLED": "0"}, func(p []byte) { os.Stderr.Write(p) })
		if err != nil {
			return Artifact{}, fmt.Errorf("build %s %s/%s: %w", program.name, goos, goarch, err)
		}
		if err = os.Chmod(dest, 0755); err != nil {
			return Artifact{}, err
		}
	}
	copies := map[string]string{"git-repo-map": "repo-map", "find-session": "find-session", "cluster-inspector": "inspector-helper", "grafana-inspector": "inspector-helper", "azure-inspector": "inspector-helper", "forgejo": "inspector-helper"}
	for pkg, helper := range copies {
		dest := filepath.Join(tree, "packages", pkg, "bin", helper+extension)
		if err = copyResources(ctx, filepath.Join(tree, "bin", helper+extension), dest); err != nil {
			return Artifact{}, err
		}
	}
	metadata, _ := json.MarshalIndent(struct {
		Version string `json:"version"`
		OS      string `json:"os"`
		Arch    string `json:"arch"`
	}{version, goos, goarch}, "", "  ")
	if err = os.WriteFile(filepath.Join(tree, "release.json"), append(metadata, '\n'), 0644); err != nil {
		return Artifact{}, err
	}
	ext := ".tar.gz"
	if goos == "windows" {
		ext = ".zip"
	}
	name := "aact_" + version + "_" + goos + "_" + goarch + ext
	archivePath := filepath.Join(temp, name)
	if err = archiveTree(ctx, tree, archivePath, goos == "windows"); err != nil {
		return Artifact{}, err
	}
	f, err := os.Open(archivePath)
	if err != nil {
		return Artifact{}, err
	}
	hash := sha256.New()
	_, err = io.Copy(hash, f)
	f.Close()
	if err != nil {
		return Artifact{}, err
	}
	sum := hex.EncodeToString(hash.Sum(nil))
	final := filepath.Join(out, name)
	if err = os.Rename(archivePath, final); err != nil {
		return Artifact{}, err
	}
	if err = os.WriteFile(final+".sha256", []byte(sum+"  "+name+"\n"), 0644); err != nil {
		return Artifact{}, err
	}
	return Artifact{Path: final, SHA256: sum}, nil
}
func copyResources(ctx context.Context, source, destination string) error {
	return filepath.WalkDir(source, func(path string, e fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if e.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("release resource is a symlink: %s", path)
		}
		if e.IsDir() && (e.Name() == ".git" || e.Name() == "__pycache__" || e.Name() == ".pytest_cache" || e.Name() == ".venv" || e.Name() == "node_modules") {
			return filepath.SkipDir
		}
		if strings.HasSuffix(e.Name(), ".pyc") {
			return nil
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		dest := destination
		if rel != "." {
			dest = filepath.Join(destination, rel)
		}
		info, err := e.Info()
		if err != nil {
			return err
		}
		if e.IsDir() {
			return os.MkdirAll(dest, info.Mode().Perm())
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("non-regular release resource: %s", path)
		}
		if err = os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
		if err != nil {
			return err
		}
		_, err = io.Copy(out, in)
		closeErr := out.Close()
		if err != nil {
			return err
		}
		return closeErr
	})
}
