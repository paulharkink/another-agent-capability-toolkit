//go:build darwin || linux

package main

import (
	"context"
	"os"
	"runtime/debug"
	"testing"
)

func TestWorktreeScanDoesNotLeakMetadataDescriptors(t *testing.T) {
	root := t.TempDir()
	fixtureWorktree(t, root)
	previous := debug.SetGCPercent(-1)
	t.Cleanup(func() { debug.SetGCPercent(previous) })
	count := func() int {
		entries, err := os.ReadDir("/dev/fd")
		if err != nil {
			t.Fatal(err)
		}
		return len(entries)
	}
	before := count()
	for n := 0; n < 24; n++ {
		if _, err := Scan(context.Background(), []string{root}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if leaked := count() - before; leaked > 2 {
		t.Fatalf("scanning 24 worktrees retained %d metadata descriptors", leaked)
	}
}
