package picker

import (
	"bufio"
	"context"
	"fmt"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Entry struct {
	Name, Path            string
	Directory, Selectable bool
}

func List(dir, kind string) ([]Entry, error) {
	entries, e := os.ReadDir(dir)
	if e != nil {
		return nil, e
	}
	out := []Entry{}
	for _, entry := range entries {
		info, e := entry.Info()
		if e != nil {
			return nil, e
		}
		isDir := info.IsDir()
		if entry.Type()&os.ModeSymlink != 0 {
			if actual, e := os.Stat(filepath.Join(dir, entry.Name())); e == nil {
				isDir = actual.IsDir()
			}
		}
		if kind == "directory" && !isDir {
			continue
		}
		out = append(out, Entry{Name: entry.Name(), Path: filepath.Join(dir, entry.Name()), Directory: isDir, Selectable: kind == "directory" && isDir || kind == "file" && !isDir && info.Mode().IsRegular()})
	}
	return out, nil
}

// Terminal browses in process and accepts a single manual native path per selection.
func Terminal(ctx context.Context, kind, initial string, reader io.Reader, writer io.Writer) (string, error) {
	if kind != "file" && kind != "directory" {
		return "", fmt.Errorf("unsupported picker kind %q", kind)
	}
	if writer == nil {
		writer = io.Discard
	}
	cwd, e := os.Getwd()
	if e != nil {
		return "", e
	}
	dir := cwd
	if initial != "" {
		path, e := config.ResolvePath(initial, filepath.Join(cwd, "picker"))
		if e != nil {
			return "", e
		}
		if info, e := os.Stat(path); e == nil && info.IsDir() {
			dir = path
		} else if info, e := os.Stat(filepath.Dir(path)); e == nil && info.IsDir() {
			dir = filepath.Dir(path)
		}
	}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for {
		if e := ctx.Err(); e != nil {
			return "", e
		}
		entries, e := List(dir, kind)
		if e != nil {
			return "", e
		}
		fmt.Fprintf(writer, "\nSelect %s in %s\n", kind, dir)
		for i, entry := range entries {
			suffix := ""
			if entry.Directory {
				suffix = "/"
			}
			fmt.Fprintf(writer, "%d %s%s\n", i+1, entry.Name, suffix)
		}
		fmt.Fprint(writer, "Path or row number; cd <directory>, up, . to select directory, cancel: ")
		if !scanner.Scan() {
			if e := scanner.Err(); e != nil {
				return "", e
			}
			return "", ErrCancelled
		}
		text := strings.TrimSpace(scanner.Text())
		if text == "" || text == "ls" {
			continue
		}
		if text == "cancel" || text == "esc" {
			return "", ErrCancelled
		}
		if text == "up" {
			dir = filepath.Dir(dir)
			continue
		}
		if strings.HasPrefix(text, "cd ") {
			path, e := config.ResolvePath(strings.TrimSpace(strings.TrimPrefix(text, "cd ")), filepath.Join(dir, "picker"))
			if e == nil {
				if info, e := os.Stat(path); e == nil && info.IsDir() {
					dir = path
					continue
				}
			}
			fmt.Fprintln(writer, "Directory unavailable")
			continue
		}
		if index, e := strconv.Atoi(text); e == nil && index > 0 && index <= len(entries) {
			entry := entries[index-1]
			if !entry.Selectable && entry.Directory {
				dir = entry.Path
				continue
			}
			text = entry.Path
		}
		path, e := selectedPath(text, kind, dir)
		if e != nil {
			fmt.Fprintln(writer, e)
			continue
		}
		return path, nil
	}
}
