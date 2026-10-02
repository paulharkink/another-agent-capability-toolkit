package release

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func archiveTree(ctx context.Context, root, path string, isZip bool) (err error) {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
	}()
	var tarWriter *tar.Writer
	var zipWriter *zip.Writer
	var gz *gzip.Writer
	if isZip {
		zipWriter = zip.NewWriter(f)
	} else {
		gz = gzip.NewWriter(f)
		tarWriter = tar.NewWriter(gz)
	}
	err = filepath.WalkDir(root, func(path string, e fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path == root {
			return nil
		}
		info, err := e.Info()
		if err != nil {
			return err
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported archive resource %s", path)
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		mode := info.Mode()
		// Windows host file modes do not retain executable bits. Distribution
		// metadata for the known native helpers is independent of host modes.
		if !info.IsDir() && strings.Contains("/"+rel, "/bin/") {
			switch strings.TrimSuffix(filepath.Base(rel), ".exe") {
			case "aact", "inspector-helper", "find-session", "repo-map":
				mode = 0755
			}
		}

		var writer io.Writer
		if isZip {
			header, err := zip.FileInfoHeader(info)
			if err != nil {
				return err
			}
			header.Name = rel
			header.Modified = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)
			header.SetMode(mode)
			if info.IsDir() {
				header.Name += "/"
			} else {
				header.Method = zip.Deflate
			}
			writer, err = zipWriter.CreateHeader(header)
			if err != nil {
				return err
			}
		} else {
			header, err := tar.FileInfoHeader(info, "")
			if err != nil {
				return err
			}
			header.Name = rel
			header.Mode = int64(mode.Perm())
			header.ModTime = time.Unix(0, 0)
			header.AccessTime = time.Time{}
			header.ChangeTime = time.Time{}
			header.Uid = 0
			header.Gid = 0
			header.Uname = ""
			header.Gname = ""
			if err = tarWriter.WriteHeader(header); err != nil {
				return err
			}
			writer = tarWriter
		}
		if info.IsDir() {
			return nil
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		_, err = io.Copy(writer, in)
		return err
	})
	if isZip {
		closeErr := zipWriter.Close()
		if err == nil {
			err = closeErr
		}
	} else {
		closeErr := tarWriter.Close()
		if err == nil {
			err = closeErr
		}
		closeErr = gz.Close()
		if err == nil {
			err = closeErr
		}
	}
	return err
}
