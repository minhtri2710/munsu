//go:build darwin || linux

package fleet

import (
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// TestListMarkedProcessesSeesSetsidChildWithoutItsSecrets runs the real OS
// inventory against a real process. The child calls setsid(), so it leaves the
// test's process group and session exactly the way 3/3 of the leftovers BEO-45
// inventoried had: a scan that walked process groups would miss it. The child
// also carries a secret-looking variable, which must not survive the whitelist.
func TestListMarkedProcessesSeesSetsidChildWithoutItsSecrets(t *testing.T) {
	// The child is this test binary re-executed, not /bin/sleep: macOS refuses
	// to hand out the environment of an Apple platform binary, so a system
	// binary would exercise the unreadable path instead of the scan.
	child := exec.Command(os.Args[0], "-test.run=TestOrphanScanChildProcess")
	child.Env = append(os.Environ(),
		"MUNSU_ORPHAN_SCAN_CHILD=1",
		"MULTICA_TASK_ID=orphan-scan-test",
		"TMPDIR=/tmp/multica-task-orphan-scan-test",
		"MULTICA_TOKEN=super-secret",
	)
	child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := child.Start(); err != nil {
		t.Fatalf("starting the scanned child: %v", err)
	}
	t.Cleanup(func() {
		_ = child.Process.Kill()
		_, _ = child.Process.Wait()
	})

	var found *MarkedProcess
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		scan, err := listMarkedProcesses()
		if err != nil {
			t.Fatalf("listMarkedProcesses: %v", err)
		}
		if scan.Total == 0 {
			t.Fatal("expected the scan to report how many processes it walked")
		}
		for i := range scan.Marked {
			if scan.Marked[i].PID == child.Process.Pid {
				found = &scan.Marked[i]
			}
		}
		if found != nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if found == nil {
		t.Fatalf("the scan missed PID %d; a process that left its run's session must still be seen", child.Process.Pid)
	}
	if found.Markers[MarkerMulticaTask] != "orphan-scan-test" {
		t.Fatalf("expected the ownership marker to be read, got %v", found.Markers)
	}
	for key := range found.Markers {
		if !orphanMarkerKeys[key] {
			t.Fatalf("non-whitelisted key %q escaped the scan", key)
		}
	}
	if found.PGID != child.Process.Pid {
		t.Fatalf("expected the setsid child to lead its own process group, got pgid %d for PID %d", found.PGID, child.Process.Pid)
	}
}

// TestOrphanScanChildProcess is the scanned child of the test above: it blocks
// until killed so the scan has something to find. Under `go test` it is a
// no-op.
func TestOrphanScanChildProcess(t *testing.T) {
	if os.Getenv("MUNSU_ORPHAN_SCAN_CHILD") == "" {
		t.Skip("helper process for TestListMarkedProcessesSeesSetsidChildWithoutItsSecrets")
	}
	time.Sleep(30 * time.Second)
}
