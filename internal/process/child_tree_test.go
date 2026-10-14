package process

import (
	"os/exec"
	"testing"
)

func TestConfigureChildProcessTreeAndSignal(t *testing.T) {
	cmd := exec.Command("unused")
	ConfigureChildProcessTree(cmd)
	if err := SignalChildProcessTree(cmd, true); err == nil {
		t.Fatal("signaling an unstarted child should report that it is done")
	}
}
