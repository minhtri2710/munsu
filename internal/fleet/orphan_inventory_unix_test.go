//go:build darwin || linux

package fleet

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// scannedChild is a live child process for the inventory to find, plus the file
// its output went to. Its whole reason to exist is that the verdict of the test
// below must not depend on when the child happens to be scheduled: the child
// says when it is ready, and only then is the scan asked anything.
type scannedChild struct {
	cmd     *exec.Cmd
	logPath string
}

// output returns everything the child wrote to stdout and stderr. A child that
// dies takes its reason with it unless someone kept this — the CI failure this
// harness was rebuilt for (run 31805867146) said only "the scan missed PID
// 7745", because the child's "no such file or directory" went to /dev/null.
func (c *scannedChild) output() string {
	data, err := os.ReadFile(c.logPath)
	if err != nil {
		return fmt.Sprintf("<child log unreadable: %v>", err)
	}
	return strings.TrimSpace(string(data))
}

// startScannedChild re-executes this test binary as a marked, setsid child and
// returns once the child has signalled — over a pipe, not a sleep — that it is
// running. An error means the child never got there, and it carries the child's
// own output so the reason is in the failure message.
func startScannedChild(t *testing.T, runFilter string) (*scannedChild, error) {
	t.Helper()

	readyR, readyW, err := os.Pipe()
	if err != nil {
		t.Fatalf("readiness pipe: %v", err)
	}
	defer readyR.Close()

	logPath := filepath.Join(t.TempDir(), "scanned-child.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		readyW.Close()
		t.Fatalf("child log: %v", err)
	}
	defer logFile.Close()

	// The child is this test binary re-executed, not /bin/sleep: macOS refuses
	// to hand out the environment of an Apple platform binary, so a system
	// binary would exercise the unreadable path instead of the scan.
	child := exec.Command(os.Args[0], "-test.run="+runFilter)
	child.Env = append(os.Environ(),
		"MUNSU_ORPHAN_SCAN_CHILD=1",
		"MULTICA_TASK_ID=orphan-scan-test",
		// A directory that exists: the child is a Go test binary, and this
		// package's TestMain creates its fixtures under TMPDIR. Pointing it at
		// a path nobody creates is what killed the child on arrival.
		"TMPDIR="+t.TempDir(),
		"MULTICA_TOKEN=super-secret",
	)
	child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	child.Stdout = logFile
	child.Stderr = logFile
	child.ExtraFiles = []*os.File{readyW} // fd 3 in the child

	if err := child.Start(); err != nil {
		readyW.Close()
		t.Fatalf("starting the scanned child: %v", err)
	}
	// The parent's write end must go now, so the read below sees EOF the moment
	// the child dies instead of blocking on a pipe the parent holds open.
	readyW.Close()

	sc := &scannedChild{cmd: child, logPath: logPath}
	t.Cleanup(func() {
		_ = child.Process.Kill()
		_, _ = child.Process.Wait()
	})

	// Liveness backstop, not a race timer: it can only fire for a child that
	// started and then neither signalled nor exited. Killing it closes the
	// pipe, so the read below returns and the failure names the child.
	stuck := time.AfterFunc(30*time.Second, func() { _ = child.Process.Kill() })
	defer stuck.Stop()

	if _, err := io.ReadFull(readyR, make([]byte, 1)); err != nil {
		return sc, fmt.Errorf("the scanned child never signalled readiness (%v); wait: %v; child output:\n%s",
			err, child.Wait(), sc.output())
	}
	return sc, nil
}

// TestListMarkedProcessesSeesSetsidChildWithoutItsSecrets runs the real OS
// inventory against a real process. The child calls setsid(), so it leaves the
// test's process group and session exactly the way 3/3 of the leftovers BEO-45
// inventoried had: a scan that walked process groups would miss it. The child
// also carries a secret-looking variable, which must not survive the whitelist.
//
// The scan runs once, after the child reports itself ready. There is no
// deadline and no retry loop: a running process either is in the inventory or
// is a defect, and how long the scan takes on a loaded runner is not evidence
// either way.
func TestListMarkedProcessesSeesSetsidChildWithoutItsSecrets(t *testing.T) {
	child, err := startScannedChild(t, "TestOrphanScanChildProcess")
	if err != nil {
		t.Fatalf("%v", err)
	}

	scan, err := listMarkedProcesses()
	if err != nil {
		t.Fatalf("listMarkedProcesses: %v", err)
	}
	if scan.Total == 0 {
		t.Fatal("expected the scan to report how many processes it walked")
	}
	var found *MarkedProcess
	for i := range scan.Marked {
		if scan.Marked[i].PID == child.cmd.Process.Pid {
			found = &scan.Marked[i]
		}
	}
	if found == nil {
		t.Fatalf("the scan missed PID %d; a process that left its run's session must still be seen; child output:\n%s",
			child.cmd.Process.Pid, child.output())
	}
	if found.Markers[MarkerMulticaTask] != "orphan-scan-test" {
		t.Fatalf("expected the ownership marker to be read, got %v", found.Markers)
	}
	for key := range found.Markers {
		if !orphanMarkerKeys[key] {
			t.Fatalf("non-whitelisted key %q escaped the scan", key)
		}
	}
	if found.PGID != child.cmd.Process.Pid {
		t.Fatalf("expected the setsid child to lead its own process group, got pgid %d for PID %d", found.PGID, child.cmd.Process.Pid)
	}
}

// TestScannedChildDeathIsReportedNotScannedFor forces the exact condition that
// made the CI failure unreadable — a child that exits instead of staying alive
// — by pointing the child at a test filter that matches nothing, so it runs to
// completion immediately. The harness must say the child is gone and hand back
// what it printed, rather than scan for a dead PID and report a missed process.
func TestScannedChildDeathIsReportedNotScannedFor(t *testing.T) {
	child, err := startScannedChild(t, "TestOrphanScanChildFilterMatchesNothing")
	if err == nil {
		t.Fatal("expected an error for a child that exited before signalling readiness")
	}
	if !strings.Contains(err.Error(), "never signalled readiness") {
		t.Errorf("error does not name the missing readiness signal: %v", err)
	}
	if out := child.output(); out == "" {
		t.Error("the child's own output was not captured, so a dead child stays mute")
	} else if !strings.Contains(err.Error(), out) {
		t.Errorf("the child said %q but the failure message does not carry it: %v", out, err)
	}
}

func TestKeepMarkersDropsEverythingOutsideTheWhitelist(t *testing.T) {
	markers := keepMarkers([]string{
		"MULTICA_TASK_ID=run-a",
		"TMPDIR=/tmp/multica-task-1",
		"MULTICA_TOKEN=super-secret",
		"EXA_API_KEY=super-secret",
		"AWS_SECRET_ACCESS_KEY=super-secret",
		"malformed-entry",
	})
	if len(markers) != 2 || markers[MarkerMulticaTask] != "run-a" || markers[MarkerTmpdir] != "/tmp/multica-task-1" {
		t.Fatalf("expected only whitelisted markers, got %v", markers)
	}
}

// TestOrphanScanChildProcess is the scanned child of the test above: it
// announces itself on the readiness pipe the parent passed as fd 3, then blocks
// until killed so the scan has something to find. Under `go test` it is a
// no-op.
func TestOrphanScanChildProcess(t *testing.T) {
	if os.Getenv("MUNSU_ORPHAN_SCAN_CHILD") == "" {
		t.Skip("helper process for TestListMarkedProcessesSeesSetsidChildWithoutItsSecrets")
	}
	ready := os.NewFile(3, "readiness")
	if ready == nil {
		t.Fatal("no readiness pipe on fd 3")
	}
	if _, err := ready.Write([]byte{'1'}); err != nil {
		t.Fatalf("signalling readiness: %v", err)
	}
	time.Sleep(30 * time.Second)
}
