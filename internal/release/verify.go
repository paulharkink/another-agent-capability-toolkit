package release

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
)

// Verify checks distribution contents and target headers of every native helper.
// It does not execute foreign-platform binaries.
func Verify(filename, goos, goarch string) error {
	checksum, err := os.ReadFile(filename + ".sha256")
	if err != nil {
		return err
	}
	fields := strings.Fields(string(checksum))
	if len(fields) == 0 || len(fields[0]) != 64 {
		return errors.New("invalid archive checksum")
	}
	expected, err := hex.DecodeString(fields[0])
	if err != nil {
		return err
	}
	archiveFile, err := os.Open(filename)
	if err != nil {
		return err
	}
	digest := sha256.New()
	_, err = io.Copy(digest, archiveFile)
	archiveFile.Close()
	if err != nil {
		return err
	}
	if !bytes.Equal(expected, digest.Sum(nil)) {
		return errors.New("archive checksum mismatch")
	}

	files := map[string][]byte{}
	modes := map[string]os.FileMode{}
	add := func(name string, mode os.FileMode, reader io.Reader) error {
		if strings.Contains(name, "\\") || strings.HasPrefix(name, "/") || name == ".." || strings.HasPrefix(path.Clean(name), "../") || strings.Contains(name, ":") {
			return fmt.Errorf("unsafe archive entry %q", name)
		}
		if !mode.IsRegular() {
			return fmt.Errorf("non-regular archive entry %q", name)
		}
		if _, ok := files[name]; ok {
			return fmt.Errorf("duplicate archive entry %q", name)
		}
		b, err := io.ReadAll(io.LimitReader(reader, 128<<20))
		if err != nil {
			return err
		}
		files[name] = b
		modes[name] = mode
		return nil
	}
	if strings.HasSuffix(filename, ".zip") {
		z, err := zip.OpenReader(filename)
		if err != nil {
			return err
		}
		defer z.Close()
		for _, file := range z.File {
			if file.FileInfo().IsDir() {
				continue
			}
			r, err := file.Open()
			if err != nil {
				return err
			}
			err = add(file.Name, file.Mode(), r)
			r.Close()
			if err != nil {
				return err
			}
		}
	} else {
		f, err := os.Open(filename)
		if err != nil {
			return err
		}
		defer f.Close()
		gz, err := gzip.NewReader(f)
		if err != nil {
			return err
		}
		defer gz.Close()
		r := tar.NewReader(gz)
		for {
			h, err := r.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			if h.Typeflag == tar.TypeDir {
				continue
			}
			if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA {
				return fmt.Errorf("unsupported archive entry %q", h.Name)
			}
			if err = add(h.Name, os.FileMode(h.Mode), r); err != nil {
				return err
			}
		}
	}
	var metadata struct{ Version, OS, Arch string }
	if err := json.Unmarshal(files["release.json"], &metadata); err != nil {
		return fmt.Errorf("invalid release metadata: %w", err)
	}
	if metadata.OS != goos || metadata.Arch != goarch || metadata.Version == "" {
		return errors.New("release metadata does not match target")
	}
	for _, file := range []string{"README.md", "LICENSE", "THIRD_PARTY_NOTICES.md"} {
		if len(files[file]) == 0 {
			return fmt.Errorf("missing archive resource %s", file)
		}
	}
	for _, pkg := range []string{"cluster-inspector", "grafana-inspector", "azure-inspector", "forgejo", "find-session", "non-interactive-ready-planning", "git-repo-map"} {
		if len(files["packages/"+pkg+"/package.toml"]) == 0 {
			return fmt.Errorf("missing bundled package %s", pkg)
		}
	}
	extension := ""
	if goos == "windows" {
		extension = ".exe"
	}
	names := []string{"bin/aact", "bin/inspector-helper", "bin/find-session", "bin/repo-map", "packages/git-repo-map/bin/repo-map", "packages/find-session/bin/find-session", "packages/cluster-inspector/bin/inspector-helper", "packages/grafana-inspector/bin/inspector-helper", "packages/azure-inspector/bin/inspector-helper", "packages/forgejo/bin/inspector-helper"}
	for _, name := range names {
		name += extension
		b, ok := files[name]
		if !ok {
			return fmt.Errorf("missing native helper %s", name)
		}
		if modes[name].Perm()&0111 == 0 {
			return fmt.Errorf("native helper is not executable: %s", name)
		}
		if err := nativeTarget(b, goos, goarch); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	return nil
}
func nativeTarget(b []byte, goos, goarch string) error {
	invalid := errors.New("native binary header does not match archive target")
	switch goos {
	case "linux":
		if len(b) < 20 || !bytes.Equal(b[:4], []byte{0x7f, 'E', 'L', 'F'}) || b[4] != 2 || b[5] != 1 {
			return invalid
		}
		machine := binary.LittleEndian.Uint16(b[18:20])
		if (goarch == "amd64" && machine == 62) || (goarch == "arm64" && machine == 183) {
			return nil
		}
	case "darwin":
		if len(b) < 8 || binary.LittleEndian.Uint32(b[:4]) != 0xfeedfacf {
			return invalid
		}
		cpu := binary.LittleEndian.Uint32(b[4:8])
		if (goarch == "amd64" && cpu == 0x01000007) || (goarch == "arm64" && cpu == 0x0100000c) {
			return nil
		}
	case "windows":
		if len(b) < 64 || string(b[:2]) != "MZ" {
			return invalid
		}
		offset := uint64(binary.LittleEndian.Uint32(b[60:64]))
		if offset+6 > uint64(len(b)) || string(b[offset:offset+4]) != "PE\x00\x00" {
			return invalid
		}
		machine := binary.LittleEndian.Uint16(b[offset+4 : offset+6])
		if (goarch == "amd64" && machine == 0x8664) || (goarch == "arm64" && machine == 0xaa64) {
			return nil
		}
	}
	return invalid
}
