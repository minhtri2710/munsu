//go:build darwin || linux

package cli

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestOrphanScanReportsRealGarbageAndLeavesItRunning drives the production
// path of `munsu doctor --orphans` — the runtime writer fence, the real OS
// process inventory, the real oracle — against a real leftover: a process that
// carries a run marker whose run-scoped TMPDIR is gone, started with setsid()
// so it sits outside this test's process group the way the leftovers BEO-45
// inventoried do. The scan must find it, call it GARBAGE, and leave it alive.
func TestOrphanScanReportsRealGarbageAndLeavesItRunning(t *testing.T) {
	vanished := filepath.Join(t.TempDir(), "multica-task-vanished")

	child := exec.Command(os.Args[0], "-test.run=TestOrphanScanChildProcess")
	child.Env = append(os.Environ(),
		"MUNSU_ORPHAN_SCAN_CHILD=1",
		"MULTICA_TASK_ID=doctor-orphan-scan-test",
		"TMPDIR="+vanished,
	)
	child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := child.Start(); err != nil {
		t.Fatalf("starting the leftover under test: %v", err)
	}
	pid := child.Process.Pid
	t.Cleanup(func() {
		_ = child.Process.Kill()
		_, _ = child.Process.Wait()
	})

	var out bytes.Buffer
	var scanErr error
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		out.Reset()
		scanErr = runOrphanScan(&out, t.TempDir())
		if strings.Contains(out.String(), fmt.Sprintf("PID %d", pid)) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	report := out.String()
	garbage := sectionOf(report, "GARBAGE")
	if !strings.Contains(garbage, fmt.Sprintf("PID %d", pid)) {
		t.Fatalf("expected PID %d in the GARBAGE group, got:\n%s", pid, report)
	}
	if !strings.Contains(garbage, vanished) {
		t.Fatalf("expected the vanished run TMPDIR as the evidence, got:\n%s", garbage)
	}
	var contract *contractError
	if !errors.As(scanErr, &contract) || contract.status != orphanExitGarbage {
		t.Fatalf("expected exit status %d when leftovers are found, got %v", orphanExitGarbage, scanErr)
	}
	if !strings.Contains(report, "does not terminate anything") {
		t.Fatalf("the report must state that it terminates nothing, got:\n%s", report)
	}
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("the scan killed PID %d (%v); it must only report", pid, err)
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

// TestOrphanScanChildProcess is the leftover the test above scans: it blocks
// until killed. Under `go test` it is a no-op.
func TestOrphanScanChildProcess(t *testing.T) {
	if os.Getenv("MUNSU_ORPHAN_SCAN_CHILD") == "" {
		t.Skip("helper process for TestOrphanScanReportsRealGarbageAndLeavesItRunning")
	}
	time.Sleep(30 * time.Second)
}
