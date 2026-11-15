package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadBrowserAdapterStaysWithinCapability(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "adapter.py"), []byte("def run(credentials, config): return {}"), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := readBrowserAdapter(dir, "adapter.py"); err != nil || got == "" {
		t.Fatalf("adapter %q %v", got, err)
	}
	if _, err := readBrowserAdapter(dir, "../escape.py"); err == nil {
		t.Fatal("accepted parent traversal")
	}
	outside := filepath.Join(t.TempDir(), "outside.py")
	if err := os.WriteFile(outside, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "link.py")); err == nil {
		if _, err := readBrowserAdapter(dir, "link.py"); err == nil {
			t.Fatal("accepted escaping symlink")
		}
	}
}
