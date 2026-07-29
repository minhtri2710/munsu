//go:build lifecycle_integration

package orchestrator

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestLifecycleLockHelper(t *testing.T) {
	home := os.Getenv("MUNSU_LOCKTEST_HOME")
	if home == "" {
		t.Skip("helper mode")
	}
	acquired, err := AcquireSession(home)
	if err != nil {
		fmt.Println("ERR", err)
		return
	}
	if !acquired {
		fmt.Println("REFUSED")
		return
	}
	fmt.Println("HELD")
	time.Sleep(30 * time.Second)
}

// helperProc runs the test binary in helper mode against home and returns the
// printed verdict ("HELD"/"REFUSED"). The cmd is returned so the caller can
// kill the process (to release the lock via process exit).
func helperProc(t *testing.T, home string) (*exec.Cmd, string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestLifecycleLockHelper$")
	cmd.Env = append(os.Environ(), "MUNSU_LOCKTEST_HOME="+home)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}
	scan := bufio.NewScanner(stdout)
	if !scan.Scan() {
		t.Fatalf("helper produced no output: %v", scan.Err())
	}
	return cmd, strings.TrimSpace(scan.Text())
}

// TestLockCrossProcessReleaseAtExit proves the real end-user invariant that
// munsu relies on: the lock is exclusive across processes, and it is released
// when the holding process exits (the leaked FD closes). This is the behavior
// session/sessionstart.go and supervision/watcher.go depend on.
func TestLockCrossProcessReleaseAtExit(t *testing.T) {
	home := freshHome(t)

	holder, verdict := helperProc(t, home)
	if verdict != "HELD" {
		t.Fatalf("first holder verdict = %q, want HELD", verdict)
	}

	// While holder is alive, a captain process must be refused.
	if _, v2 := helperProc(t, home); v2 != "REFUSED" {
		t.Fatalf("captain holder verdict = %q, want REFUSED (cross-process exclusivity)", v2)
	}

	// Killing the holder releases the lock (process exit closes the leaked FD).
	if err := holder.Process.Kill(); err != nil {
		t.Fatalf("kill holder: %v", err)
	}
	_ = holder.Wait()

	// After the holder exits, a new process must be able to acquire.
	if _, v3 := helperProc(t, home); v3 != "HELD" {
		t.Fatalf("post-exit holder verdict = %q, want HELD (release at exit)", v3)
	}
}
