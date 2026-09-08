// Deterministic tests for the typed internal wake-to-claim latency observation
// (issue #546): the latency is defined as time since the record's Epoch, which
// is stamped once at original emission and preserved across reclaim generations,
// so a reclaimed wake reports its full age since emission rather than an age
// reset by the reclaim.
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
// enqueue Epoch. The clock is injected, so each latency is asserted exactly
// (enqueue 1700000000, claim 1700000010) rather than against a loose bound.
func TestClaimReportsWakeToClaimLatency(t *testing.T) {
	home := t.TempDir()
	enqueueAt := time.Unix(1700000000, 0)
	if err := os.MkdirAll(filepath.Dir(WakeQueuePath(home)), 0755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := enqueueWakeAt(home, "signal", fmt.Sprintf("task-%d", i), "payload", enqueueAt); err != nil {
			t.Fatalf("EnqueueWake #%d: %v", i, err)
		}
	}

	claimAt := time.Unix(1700000010, 0)
	res, err := claimWakesAt(home, "consumer", 60, 10, func() time.Time { return claimAt })
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
		if lat != 10*time.Second {
			t.Fatalf("wake %d latency %v, want 10s", i, lat)
		}
	}
}

func TestReclaimUsesOneEligibilitySnapshot(t *testing.T) {
	home := t.TempDir()
	leaseDir := LeaseDir(home)
	if err := os.MkdirAll(leaseDir, 0755); err != nil {
		t.Fatalf("mkdir lease directory: %v", err)
	}
	for i := 0; i < 2; i++ {
		expiresAt := int64(1700000005)
		if i == 0 {
			expiresAt = 1700000000
		}
		contents := fmt.Sprintf("lease-%d\tconsumer\t%d\t1700000000\n1700000000\t%d\tsignal\ttask-%d\tpayload\n", i, expiresAt, i+1, i)
		if err := os.WriteFile(filepath.Join(leaseDir, fmt.Sprintf("lease-%d", i)), []byte(contents), 0600); err != nil {
			t.Fatalf("write lease %d: %v", i, err)
		}
	}
	clockValues := []time.Time{time.Unix(1700000000, 0), time.Unix(1700000010, 0)}
	clockIndex := 0
	reclaimed, err := reclaimExpiredLeasesAt(home, func() time.Time {
		value := clockValues[clockIndex]
		if clockIndex < len(clockValues)-1 {
			clockIndex++
		}
		return value
	})
	if err != nil {
		t.Fatalf("reclaimExpiredLeasesAt: %v", err)
	}
	if reclaimed != 1 {
		t.Fatalf("reclaimed=%d, want 1 eligible lease from the initial snapshot", reclaimed)
	}
}

// TestReclaimPreservesEpochForLatency constructs an already-expired lease with
// a fixed old enqueue epoch and verifies reclaim preserves it, so the reclaimed
// wake reports its full age since original emission rather than a reset age.
func TestReclaimPreservesEpochForLatency(t *testing.T) {
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

	claimAt := time.Unix(1700000010, 0)
	result, err := claimWakesAt(home, "consumer2", 60, 10, func() time.Time { return claimAt })
	if err != nil {
		t.Fatalf("ClaimWakes: %v", err)
	}
	if result.Reclaimed != 1 || len(result.Wakes) != 1 {
		t.Fatalf("reclaimed=%d wakes=%d, want one reclaimed wake", result.Reclaimed, len(result.Wakes))
	}
	if result.Wakes[0].Epoch != oldEpoch {
		t.Fatalf("reclaimed epoch %q, want %q preserved across reclaim", result.Wakes[0].Epoch, oldEpoch)
	}
	if result.Wakes[0].Seq != "1" {
		t.Fatalf("reclaimed seq %q, want \"1\" preserved across reclaim", result.Wakes[0].Seq)
	}
	if result.WakeToClaimLatencies[0] != 10*time.Second {
		t.Fatalf("reclaimed latency = %v, want 10s (claim 1700000010 - emission 1700000000)", result.WakeToClaimLatencies[0])
	}
}
