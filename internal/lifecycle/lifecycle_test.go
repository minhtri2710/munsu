package lifecycle

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func freshHome(t *testing.T) string {
	t.Helper()
	return t.TempDir()
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

// TestLockExclusivity proves the flock exclusion mechanism: a captain acquire
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
		t.Fatalf("captain AcquireSession error: %v", err)
	}
	if acq2 {
		t.Fatal("captain AcquireSession succeeded; expected refusal while held")
	}
	if !IsSessionLocked(home) {
		t.Fatal("IsSessionLocked false while held")
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
}

// TestWakeIDUniqueness_RapidEnqueue proves that multiple enqueues within the
// same process and same clock second produce unique epoch:seq pairs.
func TestWakeIDUniqueness_RapidEnqueue(t *testing.T) {
	home := freshHome(t)

	// Enqueue 5 wakes in rapid succession (same PID, same second).
	for i := 0; i < 5; i++ {
		if err := EnqueueWake(home, "signal", fmt.Sprintf("task-%d", i), "payload"); err != nil {
			t.Fatalf("EnqueueWake #%d: %v", i, err)
		}
	}

	recs, err := DrainWakes(home)
	if err != nil {
		t.Fatalf("DrainWakes: %v", err)
	}
	if len(recs) != 5 {
		t.Fatalf("drained %d records, want 5", len(recs))
	}

	// Verify unique epoch:seq pairs.
	seen := make(map[string]bool)
	for i, r := range recs {
		key := r.Epoch + ":" + r.Seq
		if seen[key] {
			t.Fatalf("duplicate event ID %q at record %d", key, i)
		}
		seen[key] = true
	}
}

// TestAckWakes_NoCollision proves that acking one wake does not ack another
// wake sharing the same epoch:seq pair (which happens with duplicate IDs).
func TestAckWakes_NoCollision(t *testing.T) {
	home := freshHome(t)

	// Enqueue 3 wakes rapidly.
	for i := 0; i < 3; i++ {
		if err := EnqueueWake(home, "signal", fmt.Sprintf("key-%d", i), fmt.Sprintf("payload-%d", i)); err != nil {
			t.Fatalf("EnqueueWake: %v", err)
		}
	}

	// Claim all 3.
	result, err := ClaimWakes(home, "test-consumer", 60, 10)
	if err != nil {
		t.Fatalf("ClaimWakes: %v", err)
	}
	if len(result.Wakes) != 3 {
		t.Fatalf("claimed %d wakes, want 3", len(result.Wakes))
	}

	// Ack only the middle wake.
	mid := result.Wakes[1]
	if err := AckWakes(home, result.LeaseID, []string{mid.Epoch + ":" + mid.Seq}); err != nil {
		t.Fatalf("AckWakes: %v", err)
	}

	// Remaining lease file should contain the other 2 wakes.
	leasePath := LeaseFilePath(home, result.LeaseID)
	data, err := os.ReadFile(leasePath)
	if err != nil {
		t.Fatalf("reading lease file: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 { // header + 2 unacked
		t.Fatalf("lease has %d lines, want 3 (header + 2 unacked)", len(lines))
	}
	// The unacked events must not include the acked one.
	for _, line := range lines[1:] {
		parts := strings.SplitN(line, "\t", 5)
		if len(parts) < 2 {
			continue
		}
		if parts[0] == mid.Epoch && parts[1] == mid.Seq {
			t.Fatal("acked wake should not remain in lease file")
		}
	}
}

// TestReclaimPath_UniqueIDs proves that reclaimed wakes (re-enqueued by
// reclaimExpiredLeases) get unique event IDs, not duplicates colliding
// with other enqueues in the same second.
func TestReclaimPath_UniqueIDs(t *testing.T) {
	home := freshHome(t)

	// Enqueue 2 wakes, claim with 0-second lease (immediate expiry).
	for i := 0; i < 2; i++ {
		if err := EnqueueWake(home, "signal", fmt.Sprintf("key-%d", i), "payload"); err != nil {
			t.Fatalf("EnqueueWake: %v", err)
		}
	}

	result, err := ClaimWakes(home, "consumer", 0, 10)
	if err != nil {
		t.Fatalf("ClaimWakes: %v", err)
	}
	if len(result.Wakes) != 2 {
		t.Fatalf("claimed %d wakes, want 2", len(result.Wakes))
	}

	// Claim again with a new consumer — triggers reclaim of expired lease.
	// Also enqueue a fresh wake at the same time to test collision.
	EnqueueWake(home, "signal", "fresh-key", "fresh-payload")

	result2, err := ClaimWakes(home, "consumer2", 60, 10)
	if err != nil {
		t.Fatalf("ClaimWakes: %v", err)
	}

	// Should have: 2 reclaimed + 1 fresh = 3.
	if len(result2.Wakes) != 3 {
		t.Errorf("after reclaim, got %d wakes, want 3 (2 reclaimed + 1 fresh)", len(result2.Wakes))
	}

	// Verify all 3 have unique epoch:seq pairs.
	seen := make(map[string]bool)
	for i, w := range result2.Wakes {
		key := w.Epoch + ":" + w.Seq
		if seen[key] {
			t.Fatalf("duplicate event ID %q in reclaimed batch at index %d", key, i)
		}
		seen[key] = true
	}
}
