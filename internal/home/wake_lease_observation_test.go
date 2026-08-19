// Deterministic tests for the typed internal wake-to-claim latency observation
// (issue #546): the latency is defined as time since the LATEST enqueue Epoch,
// and reclaim re-enqueues wakes under a fresh Epoch rather than preserving the
// original emission age.
package home

import (
	"fmt"
	"testing"
	"time"
)

func TestWakeAgeSinceEnqueue_ExactClicks(t *testing.T) {
	epoch := "1700000000"
	now := time.Unix(1700000000, 0).Add(90 * time.Second)
	if got := WakeAgeSinceEnqueue(epoch, now); got != 90*time.Second {
		t.Fatalf("WakeAgeSinceEnqueue(%q, now) = %v, want 90s", epoch, got)
	}
}

func TestWakeAgeSinceEnqueue_MalformedEpochIsZero(t *testing.T) {
	if got := WakeAgeSinceEnqueue("not-a-number", time.Now()); got != 0 {
		t.Fatalf("WakeAgeSinceEnqueue(malformed) = %v, want 0", got)
	}
}

// TestClaimReportsWakeToClaimLatency verifies ClaimWakes reports one latency
// per claimed wake, aligned with the Wakes slice, measured since the latest
// enqueue Epoch. The bound is loose (under a few minutes) so the assertion is
// about alignment and non-negativity, not wall-clock precision.
func TestClaimReportsWakeToClaimLatency(t *testing.T) {
	home := t.TempDir()
	for i := 0; i < 3; i++ {
		if err := EnqueueWake(home, "signal", fmt.Sprintf("task-%d", i), "payload"); err != nil {
			t.Fatalf("EnqueueWake #%d: %v", i, err)
		}
	}

	res, err := ClaimWakes(home, "consumer", 60, 10)
	if err != nil {
		t.Fatalf("ClaimWakes: %v", err)
	}
	if len(res.Wakes) != 3 {
		t.Fatalf("claimed %d wakes, want 3", len(res.Wakes))
	}
	if len(res.WakeToClaimLatencies) != len(res.Wakes) {
		t.Fatalf("latencies = %d, want %d (aligned with Wakes)", len(res.WakeToClaimLatencies), len(res.Wakes))
	}
	for i, lat := range res.WakeToClaimLatencies {
		if lat < 0 {
			t.Fatalf("wake %d latency %v is negative (clock skew)", i, lat)
		}
		if lat > 10*time.Minute {
			t.Fatalf("wake %d latency %v exceeds sane bound", i, lat)
		}
	}
}

// TestReclaimReStampsEpochForLatency proves the "reclaim creates a new Epoch"
// invariant that the latency definition relies on: a wake reclaimed from an
// expired lease is re-enqueued, which stamps a FRESH epoch, so the claimed
// latency is measured from the latest enqueue, never the original emission
// age. The stale original epoch, if it had been preserved, would report a far
// older age after the sleep.
func TestReclaimReStampsEpochForLatency(t *testing.T) {
	home := t.TempDir()
	if err := EnqueueWake(home, "signal", "task-1", "payload"); err != nil {
		t.Fatalf("EnqueueWake: %v", err)
	}

	// First claim with an immediate-expiry lease: the wake moves to the lease
	// under its ORIGINAL epoch.
	first, err := ClaimWakes(home, "consumer", 0, 10)
	if err != nil {
		t.Fatalf("first ClaimWakes: %v", err)
	}
	if len(first.Wakes) != 1 {
		t.Fatalf("first claim woke %d wakes, want 1", len(first.Wakes))
	}
	origEpoch := first.Wakes[0].Epoch

	// Let the wall clock advance so a re-stamped epoch is strictly newer than
	// the original emission second.
	time.Sleep(1100 * time.Millisecond)

	// Second claim triggers reclaim of the expired lease, which re-enqueues the
	// wake under a FRESH epoch.
	second, err := ClaimWakes(home, "consumer2", 60, 10)
	if err != nil {
		t.Fatalf("second ClaimWakes: %v", err)
	}
	var reclaimed *ClaimedWakeRecord
	for i, w := range second.Wakes {
		if w.Key == "task-1" {
			reclaimed = &second.Wakes[i]
		}
	}
	if reclaimed == nil {
		t.Fatal("reclaimed wake for task-1 not found")
	}
	if reclaimed.Epoch <= origEpoch {
		t.Fatalf("reclaimed epoch %q <= original epoch %q; reclaim must create a fresh Epoch", reclaimed.Epoch, origEpoch)
	}
	// The latency is measured from the fresh epoch (recent enqueue), so it must
	// be far below the elapsed since the original emission (which is ~1.1s+).
	if lat := WakeAgeSinceEnqueue(reclaimed.Epoch, time.Now()); lat >= 1000*time.Millisecond {
		t.Fatalf("reclaimed wake latency %v too large; latency must be since the latest (fresh) enqueue, not original emission age", lat)
	}
}
