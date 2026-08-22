//go:build darwin || linux

package cli

import (
	"bytes"
	"errors"
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

// orphanChildReady is what the leftover helper writes to its readiness pipe
// once it is executing its own test body.
const orphanChildReady = "ready\n"

// TestOrphanScanReportsRealGarbageAndLeavesItRunning drives the production
// path of `munsu doctor --orphans` — the runtime writer fence, the real OS
// process inventory, the real oracle — against a real leftover: a process that
// carries a run marker whose run-scoped TMPDIR is gone, started with setsid()
// so it sits outside this test's process group the way the leftovers BEO-45
// inventoried do. The scan must find it, call it GARBAGE, and leave it alive.
func TestOrphanScanReportsRealGarbageAndLeavesItRunning(t *testing.T) {
	vanished := filepath.Join(t.TempDir(), "multica-task-vanished")

	readyR, readyW, err := os.Pipe()
	if err != nil {
		t.Fatalf("readiness pipe: %v", err)
	}
	defer readyR.Close()

	child := exec.Command(os.Args[0], "-test.run=TestOrphanScanChildProcess")
	child.Env = append(os.Environ(),
		"MUNSU_ORPHAN_SCAN_CHILD=1",
		"MULTICA_TASK_ID=doctor-orphan-scan-test",
		"TMPDIR="+vanished,
	)
	child.ExtraFiles = []*os.File{readyW}
	child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := child.Start(); err != nil {
		readyW.Close()
		t.Fatalf("starting the leftover under test: %v", err)
	}
	// The parent's copy must go, or the read in awaitLeftoverScannable cannot
	// see EOF when the child dies before signalling.
	readyW.Close()
	pid := child.Process.Pid
	t.Cleanup(func() {
		_ = child.Process.Kill()
		_, _ = child.Process.Wait()
	})

	awaitLeftoverScannable(t, readyR, pid)

	var out bytes.Buffer
	scanErr := runOrphanScan(&out, t.TempDir())

	report := out.String()
	garbage := sectionOf(report, "GARBAGE")
	if !strings.Contains(garbage, fmt.Sprintf("PID %d", pid)) {
		t.Fatalf("the scan did not report PID %d in the GARBAGE group, and the leftover had already signalled readiness from inside its own test body, so it was live and scannable when this scan ran; got:\n%s", pid, report)
	}
	if !strings.Contains(garbage, vanished) {
		t.Fatalf("expected the vanished run TMPDIR as the evidence, got:\n%s", garbage)
	}
	var contract *contractError
	if !errors.As(scanErr, &contract) || orphanShellExit(t, scanErr) != documentedOrphanExitGarbage {
		t.Fatalf("expected exit status %d when leftovers are found, got %v", documentedOrphanExitGarbage, scanErr)
	}
	if !strings.Contains(report, "does not terminate anything") {
		t.Fatalf("the report must state that it terminates nothing, got:\n%s", report)
	}
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("the scan killed PID %d (%v); it must only report", pid, err)
	}
}

// spawnBackstop bounds a wait on a spawned process by a share of the test
// binary's OWN remaining -timeout budget instead of by a literal. It is the
// same helper internal/backend grew for #574. A shared home does exist —
// internal/testutil is test infrastructure only by the topology rule in
// internal/testutil/architecture_policy_test.go — so this is restated rather
// than shared only because two copies sit below the threshold where that
// indirection pays. A third would not.
//
// Halving what remains leaves room for the rest of the test and still fails
// ahead of the binary's own timeout panic, so the failure keeps its message
// instead of becoming a goroutine dump. This is a backstop for a genuine hang,
// never the primary signal: the caller also ends on evidence. `go test
// -timeout 0` asks for no deadline at all, so honour that with a nil channel
// that never fires and let the evidence do the work.
func spawnBackstop(t *testing.T) <-chan time.Time {
	t.Helper()
	deadline, ok := t.Deadline()
	if !ok {
		return nil
	}
	return time.After(time.Until(deadline) / 2)
}

// awaitLeftoverScannable blocks until the spawned leftover has proven it is
// running its own test body, and on failure says so in the message — this wait
// is never reported as a runOrphanScan verdict.
//
// What ends the wait is evidence, not a clock (#576). The readiness pipe
// carries exactly three outcomes:
//
//   - the readiness bytes arrive. The child is executing user code, so the
//     exec that published its argv and environment to the platform process
//     inventory the scan walks is long past. Every assertion after this point
//     is about the scan.
//   - EOF. The child exited without signalling, so no scan could ever have
//     found it. That signal is exact and immediate.
//   - neither, because the child hung before reaching its test body. The bound
//     for that is derived from this binary's own remaining -timeout budget
//     rather than written as a literal, so a loaded runner and an idle one get
//     the same semantics; the slow one merely takes longer to reach the same
//     verdict.
//
// What this replaces: a poll of the scan itself bounded by a literal 5s, which
// fell through on expiry so that the GARBAGE-group assertion failed next. That
// made a machine too slow to fork, exec and initialise a Go test binary look
// like a scan that had missed a leftover.
func awaitLeftoverScannable(t *testing.T, readyR *os.File, pid int) {
	t.Helper()
	signalled := make(chan error, 1)
	go func() {
		buf := make([]byte, len(orphanChildReady))
		if _, err := io.ReadFull(readyR, buf); err != nil {
			signalled <- err
			return
		}
		if string(buf) != orphanChildReady {
			signalled <- fmt.Errorf("readiness pipe carried %q, want %q", buf, orphanChildReady)
			return
		}
		signalled <- nil
	}()
	select {
	case err := <-signalled:
		if err != nil {
			t.Fatalf("waiting for the leftover: PID %d exited before signalling readiness from its own test body (%v), so no orphan scan could ever have observed it; this is the test's wait for its child, not a runOrphanScan verdict", pid, err)
		}
	case <-spawnBackstop(t):
		t.Fatalf("waiting for the leftover: PID %d had not signalled readiness from its own test body when the backstop derived from this binary's -timeout budget elapsed, so it is still starting up or hung short of it; this is the test's wait for its child, not a runOrphanScan verdict", pid)
	}
}

// sectionOf returns the report block for one verdict group.
func sectionOf(report, verdict string) string {
	start := strings.Index(report, "\n"+verdict+" (")
	if start < 0 {
		return ""
	}
	rest := report[start+1:]
	if end := strings.Index(rest[1:], "\n\n"); end >= 0 {
		return rest[:end+1]
	}
	return rest
}

// TestOrphanScanChildProcess is the leftover the test above scans: it signals
// readiness on fd 3 and then blocks until killed. Under `go test` it is a
// no-op.
//
// Signalling from inside the test body is what lets the parent tell "the child
// is not up yet" from "the scan missed a leftover it should have reported":
// reaching this line proves the exec that published this process's argv and
// environment to the platform inventory has completed.
func TestOrphanScanChildProcess(t *testing.T) {
	if os.Getenv("MUNSU_ORPHAN_SCAN_CHILD") == "" {
		t.Skip("helper process for TestOrphanScanReportsRealGarbageAndLeavesItRunning")
	}
	ready := os.NewFile(3, "ready")
	if _, err := ready.WriteString(orphanChildReady); err != nil {
		t.Fatalf("signalling readiness on fd 3: %v", err)
	}
	ready.Close()
	time.Sleep(30 * time.Second)
}
