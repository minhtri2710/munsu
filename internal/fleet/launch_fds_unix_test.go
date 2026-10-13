//go:build !windows

package fleet

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// openFDsScript prints each descriptor above stdio that the process holds. It
// probes with test -e, which opens nothing of its own.
const openFDsScript = `n=3; while [ $n -lt 256 ]; do [ -e /dev/fd/$n ] && echo $n; n=$((n+1)); done`

// The launch script closes every descriptor above stdio before it execs the
// harness, so one the pane shell leaked does not reach the harness.
func TestLaunchScriptExecsHarnessWithOnlyStdio(t *testing.T) {
	in := launchArtifactInputForGuards(t)
	in.LaunchBin = "/bin/sh"
	in.LaunchArgs = []string{"-c", openFDsScript}
	if _, err := buildLaunchArtifact(in); err != nil {
		t.Fatalf("buildLaunchArtifact: %v", err)
	}
	held, err := os.Create(filepath.Join(t.TempDir(), "held"))
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	cmd := exec.Command("bash", filepath.Join(in.LaunchDir, LaunchScriptName))
	cmd.ExtraFiles = []*os.File{held}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("launch script: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("harness holds descriptors %q above stdio", out)
	}
}
