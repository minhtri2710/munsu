//go:build darwin || linux

package fleet

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
)

// childReadyFD is the descriptor the scanned child reports readiness on. Go
// maps exec.Cmd.ExtraFiles[0] to fd 3 in the child.
const childReadyFD = 3

// childReadyToken is what the child writes to childReadyFD once it has reached
// its blocking point and is therefore observable by a process scan.
const childReadyToken = "ready\n"

// TestListMarkedProcessesSeesSetsidChildWithoutItsSecrets runs the real OS
// inventory against a real process. The child calls setsid(), so it leaves the
// test's process group and session exactly the way 3/3 of the leftovers BEO-45
// inventoried had: a scan that walked process groups would miss it. The child
// also carries a secret-looking variable, which must not survive the whitelist.
//
// The child reports readiness before the scan runs and then blocks until this
// test closes its stdin, so its lifetime brackets the scan by construction. No
// wall-clock deadline decides this verdict: one scan pass must see a process
// that is provably alive, so there is no retry loop to time out.
func TestListMarkedProcessesSeesSetsidChildWithoutItsSecrets(t *testing.T) {
	child, err := startScannedChild(t, scannedChildTest, t.TempDir())
	if err != nil {
		t.Fatal(err)
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
		if scan.Marked[i].PID == child.Process.Pid {
			found = &scan.Marked[i]
		}
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

// TestScannedChildDeathIsReportedNotScannedFor forces the failure mode that
// made the test above flaky: a child that exits before the scan can see it.
// The child used to be started with TMPDIR pointing at a directory nobody
// created, so it died in TestMain within microseconds; with its output
// discarded and only a 5s scan deadline to fall back on, the verdict was a
// mute "the scan missed PID N" after burning the full deadline. The readiness
// handshake turns that class of failure into an immediate, explained one, and
// this test pins that: a child that never reports readiness must be reported as
// a dead child, not scanned for until a clock runs out.
func TestScannedChildDeathIsReportedNotScannedFor(t *testing.T) {
	_, err := startScannedChild(t, "TestOrphanScanChildProcessThatDoesNotExist", t.TempDir())
	if err == nil {
		t.Fatal("expected a child that exits before readiness to be reported as such")
	}
	if !strings.Contains(err.Error(), "readiness") {
		t.Fatalf("expected the failure to name the readiness handshake, got: %v", err)
	}
}

// scannedChildTest is the -test.run filter selecting the child half of the
// scan test.
const scannedChildTest = "TestOrphanScanChildProcess"

// startScannedChild re-executes this test binary as a setsid child carrying the
// ownership marker and a secret, and returns only once the child has reported
// readiness over a dedicated pipe. Readiness — not elapsed time — is the
// signal: if the child dies first it closes its end of the pipe, so the read
// ends immediately with the child's own diagnostics already forwarded to t.Log.
// The child blocks until the returned cleanup closes its stdin, so it stays
// observable for as long as the caller needs it.
func startScannedChild(t *testing.T, testName, tmpdir string) (*exec.Cmd, error) {
	t.Helper()

	readyR, readyW, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("readiness pipe: %w", err)
	}
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("child stdin pipe: %w", err)
	}

	// The child is this test binary re-executed, not /bin/sleep: macOS refuses
	// to hand out the environment of an Apple platform binary, so a system
	// binary would exercise the unreadable path instead of the scan.
	child := exec.Command(os.Args[0], "-test.run="+testName)
	child.Env = append(os.Environ(),
		"MUNSU_ORPHAN_SCAN_CHILD=1",
		"MULTICA_TASK_ID=orphan-scan-test",
		"TMPDIR="+tmpdir,
		"MULTICA_TOKEN=super-secret",
	)
	child.Stdin = stdinR
	child.Stdout = &testLogWriter{t: t, prefix: "scanned child"}
	child.Stderr = &testLogWriter{t: t, prefix: "scanned child"}
	child.ExtraFiles = []*os.File{readyW}
	child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := child.Start(); err != nil {
		return nil, fmt.Errorf("starting the scanned child: %w", err)
	}
	// Drop the parent's copies of the child's ends: once they are closed, the
	// child is the only holder, so its death is what closes the readiness pipe.
	_ = readyW.Close()
	_ = stdinR.Close()

	t.Cleanup(func() {
		_ = stdinW.Close()
		_ = child.Process.Kill()
		// Wait, not Process.Wait: it also joins the goroutines forwarding the
		// child's output into t.Log before this test is allowed to finish.
		_ = child.Wait()
		_ = readyR.Close()
	})

	token := make([]byte, len(childReadyToken))
	if _, err := io.ReadFull(readyR, token); err != nil {
		return nil, fmt.Errorf("the scanned child exited before reporting readiness: %w", err)
	}
	if string(token) != childReadyToken {
		return nil, fmt.Errorf("the scanned child sent %q on the readiness pipe, want %q", token, childReadyToken)
	}
	return child, nil
}

// testLogWriter forwards a child process's output into the test log, so a child
// that dies says why instead of leaving a mute verdict behind.
type testLogWriter struct {
	t      *testing.T
	prefix string
}

func (w *testLogWriter) Write(p []byte) (int, error) {
	w.t.Logf("%s: %s", w.prefix, strings.TrimRight(string(p), "\n"))
	return len(p), nil
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

// TestOrphanScanChildProcess is the scanned child of the test above: it reports
// readiness, then blocks until its parent closes stdin so the scan has
// something to find. Under `go test` it is a no-op.
func TestOrphanScanChildProcess(t *testing.T) {
	if os.Getenv("MUNSU_ORPHAN_SCAN_CHILD") == "" {
		t.Skip("helper process for TestListMarkedProcessesSeesSetsidChildWithoutItsSecrets")
	}
	ready := os.NewFile(childReadyFD, "readiness")
	if ready == nil {
		t.Fatalf("no readiness pipe on fd %d", childReadyFD)
	}
	if _, err := ready.WriteString(childReadyToken); err != nil {
		t.Fatalf("reporting readiness: %v", err)
	}
	if err := ready.Close(); err != nil {
		t.Fatalf("closing the readiness pipe: %v", err)
	}
	// Block on stdin rather than sleeping: the parent owns this process's
	// lifetime, so no clock in here can expire mid-scan.
	_, _ = io.Copy(io.Discard, os.Stdin)
}
