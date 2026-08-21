// Deterministic tests for the typed internal wake-to-claim latency observation
// (issue #546): the latency is defined as time since the LATEST enqueue Epoch,
// and reclaim re-enqueues wakes under a fresh Epoch rather than preserving the
// original emission age.
package home

import (
	"fmt"
	"os"
	"path/filepath"
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

func TestWakeAgeSinceEnqueue_MalformedOrFutureEpochIsZero(t *testing.T) {
	now := time.Unix(1700000000, 0)
	if got := WakeAgeSinceEnqueue("not-a-number", now); got != 0 {
		t.Fatalf("WakeAgeSinceEnqueue(malformed) = %v, want 0", got)
	}
	if got := WakeAgeSinceEnqueue("1700000001", now); got != 0 {
		t.Fatalf("WakeAgeSinceEnqueue(future epoch) = %v, want 0", got)
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

// TestReclaimReStampsEpochForLatency constructs an already-expired lease with
// a fixed old enqueue epoch and verifies reclaim writes a fresh enqueue epoch.
func TestReclaimReStampsEpochForLatency(t *testing.T) {
	home := t.TempDir()
	leaseDir := LeaseDir(home)
	if err := os.MkdirAll(leaseDir, 0755); err != nil {
		t.Fatalf("mkdir lease directory: %v", err)
	}
	const oldEpoch = "1700000000"
	lease := "lease-expired"
	contents := lease + "\tconsumer\t0\t1700000000\n" +
		oldEpoch + "\t1\tsignal\ttask-1\tpayload\n"
	if err := os.WriteFile(filepath.Join(leaseDir, lease), []byte(contents), 0600); err != nil {
		t.Fatalf("write expired lease: %v", err)
	}

	result, err := ClaimWakes(home, "consumer2", 60, 10)
	if err != nil {
		t.Fatalf("ClaimWakes: %v", err)
	}
	if result.Reclaimed != 1 || len(result.Wakes) != 1 {
		t.Fatalf("reclaimed=%d wakes=%d, want one reclaimed wake", result.Reclaimed, len(result.Wakes))
	}
	if result.Wakes[0].Epoch <= oldEpoch {
		t.Fatalf("reclaimed epoch %q <= fixed old epoch %q; reclaim must restamp enqueue time", result.Wakes[0].Epoch, oldEpoch)
	}
}
