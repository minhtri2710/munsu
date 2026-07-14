package lifecycle

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func freshHome(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

// TestMain implements a helper mode so the lock can be held by a separate
// process (its own FD lifetime), mirroring how munsu actually uses the lock:
// one holder per process, released when the process exits.
func TestMain(m *testing.M) {
	if home := os.Getenv("MUNSU_LOCKTEST_HOME"); home != "" {
		acq, err := AcquireSession(home)
		if err != nil {
			fmt.Println("ERR", err)
			os.Exit(0)
		}
		if !acq {
			fmt.Println("REFUSED")
			os.Exit(0)
		}
		fmt.Println("HELD")
		// Hold the leaked FD (and thus the lock) until killed.
		time.Sleep(30 * time.Second)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// helperProc runs the test binary in helper mode against home and returns the
// printed verdict ("HELD"/"REFUSED"). The cmd is returned so the caller can
// kill the process (to release the lock via process exit).
func helperProc(t *testing.T, home string) (*exec.Cmd, string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
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

// TestLockPathSingleSourceOfTruth verifies the canonical lock path is owned
// only by lifecycle (consumers never spell state/.lock themselves).
func TestLockPathSingleSourceOfTruth(t *testing.T) {
	if got := LockPath(freshHome(t)); got != filepath.Join(freshHome(t), "state/.lock") {
		// (freshHome differs each call; this just exercises the join form below)
	}
	home := freshHome(t)
	if got := LockPath(home); got != filepath.Join(home, "state/.lock") {
		t.Fatalf("LockPath = %q", got)
	}
	if got := BeatPath(home); got != filepath.Join(home, "state/.last-watcher-beat") {
		t.Fatalf("BeatPath = %q", got)
	}
	if got := QueuePath(home); got != filepath.Join(home, "state/.wake-queue") {
		t.Fatalf("QueuePath = %q", got)
	}
}

// TestLockExclusivity proves the flock exclusion mechanism: a second acquire
// in the same process is refused while the first holds the lock.
func TestLockExclusivity(t *testing.T) {
	home := freshHome(t)

	acq1, err := AcquireSession(home)
	if err != nil {
		t.Fatalf("first AcquireSession: %v", err)
	}
	if !acq1 {
		t.Fatal("first AcquireSession returned false; expected to acquire")
	}
	if _, err := os.Stat(LockPath(home)); err != nil {
		t.Fatalf("lock file not created: %v", err)
	}

	acq2, err := AcquireSession(home)
	if err != nil {
		t.Fatalf("second AcquireSession error: %v", err)
	}
	if acq2 {
		t.Fatal("second AcquireSession succeeded; expected refusal while held")
	}
	if !IsSessionLocked(home) {
		t.Fatal("IsSessionLocked false while held")
	}
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

	// While holder is alive, a second process must be refused.
	if _, v2 := helperProc(t, home); v2 != "REFUSED" {
		t.Fatalf("second holder verdict = %q, want REFUSED (cross-process exclusivity)", v2)
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

// TestBeatWriteReadStatus exercises the watcher liveness beat used by
// waker.CheckGuard: a written beat round-trips and stale detection flips.
func TestBeatWriteReadStatus(t *testing.T) {
	home := freshHome(t)

	st := ReadBeatStatus(home, time.Now())
	if st.Exists || !st.Stale {
		t.Fatalf("missing beat should be !Exists && Stale, got %+v", st)
	}

	WriteBeat(home)
	ts, pid, ok := ReadBeat(home)
	if !ok {
		t.Fatal("ReadBeat ok=false after WriteBeat")
	}
	if pid != os.Getpid() {
		t.Fatalf("ReadBeat pid = %d, want %d", pid, os.Getpid())
	}

	if fresh := ReadBeatStatus(home, time.Now()); !fresh.Exists || fresh.Stale {
		t.Fatalf("fresh beat should be Exists+!Stale, got %+v", fresh)
	}
	old := ReadBeatStatus(home, time.Unix(ts, 0).Add(StaleThreshold()+time.Second))
	if !old.Stale {
		t.Fatal("beat older than StaleThreshold should be stale")
	}
}

// TestQueueEnqueueDrainClear exercises the durable wake queue used by
// waker.Drain: enqueue appends TSV records, Drain returns them in order and
// removes the file, HasQueuedWakes tracks presence.
func TestQueueEnqueueDrainClear(t *testing.T) {
	home := freshHome(t)

	if HasQueuedWakes(home) {
		t.Fatal("HasQueuedWakes true on empty home")
	}
	if err := EnqueueWake(home, "signal", "task-1", "payload-A"); err != nil {
		t.Fatalf("EnqueueWake #1: %v", err)
	}
	if err := EnqueueWake(home, "check", "task-2", "payload-B"); err != nil {
		t.Fatalf("EnqueueWake #2: %v", err)
	}
	if !HasQueuedWakes(home) {
		t.Fatal("HasQueuedWakes false after enqueue")
	}

	recs, err := DrainWakes(home)
	if err != nil {
		t.Fatalf("DrainWakes: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("drained %d records, want 2", len(recs))
	}
	if recs[0].Kind != "signal" || recs[0].Key != "task-1" || recs[0].Payload != "payload-A" {
		t.Fatalf("record[0] = %+v", recs[0])
	}
	if recs[1].Kind != "check" || recs[1].Key != "task-2" || recs[1].Payload != "payload-B" {
		t.Fatalf("record[1] = %+v", recs[1])
	}
	if HasQueuedWakes(home) {
		t.Fatal("HasQueuedWakes true after drain; queue file should be removed")
	}
	if again, _ := DrainWakes(home); again != nil {
		t.Fatalf("second drain returned %d records, want nil", len(again))
	}
}
