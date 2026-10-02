package picker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTerminalFiltersFileAndDirectory(t *testing.T) {
	root := t.TempDir()
	if e := os.Mkdir(filepath.Join(root, "dir"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(root, "file.txt"), []byte("content"), 0600); e != nil {
		t.Fatal(e)
	}
	dirs, e := List(root, "directory")
	if e != nil || len(dirs) != 1 || dirs[0].Name != "dir" {
		t.Fatalf("%#v %v", dirs, e)
	}
	files, e := List(root, "file")
	if e != nil || len(files) != 2 || files[0].Selectable || !files[1].Selectable {
		t.Fatalf("%#v %v", files, e)
	}
	got, e := Terminal(context.Background(), "file", root, strings.NewReader("file.txt\n"), nil)
	if e != nil || got != filepath.Join(root, "file.txt") {
		t.Fatalf("%s %v", got, e)
	}
}
func TestTerminalDirectoryNavigationAndCancel(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if e := os.Mkdir(sub, 0700); e != nil {
		t.Fatal(e)
	}
	got, e := Terminal(context.Background(), "directory", root, strings.NewReader("cd sub\n.\n"), nil)
	if e != nil || got != sub {
		t.Fatalf("%s %v", got, e)
	}
	if _, e = Terminal(context.Background(), "file", root, strings.NewReader("cancel\n"), nil); e != ErrCancelled {
		t.Fatal(e)
	}
}
