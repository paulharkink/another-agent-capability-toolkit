package browsercontroller

import (
	"path/filepath"
	"testing"
)

func TestRuntimeLockAllowsExactlyOneController(t *testing.T) {
	path := filepath.Join(t.TempDir(), "controller.lock")
	release, acquired, err := acquireRuntimeLock(path)
	if err != nil || !acquired {
		t.Fatalf("first lock: acquired=%v err=%v", acquired, err)
	}
	defer release()
	otherRelease, otherAcquired, err := acquireRuntimeLock(path)
	if err != nil || otherAcquired {
		if otherRelease != nil {
			otherRelease()
		}
		t.Fatalf("second lock: acquired=%v err=%v, want locked", otherAcquired, err)
	}
	release()
	release, acquired, err = acquireRuntimeLock(path)
	if err != nil || !acquired {
		t.Fatalf("lock after release: acquired=%v err=%v", acquired, err)
	}
	release()
}
