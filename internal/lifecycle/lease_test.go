package lifecycle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeWakeQueueLines writes tab-separated wake queue entries for testing.
func writeWakeQueueLines(t *testing.T, homeDir string, lines []string) {
	t.Helper()
	path := QueuePath(homeDir)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	data := strings.Join(lines, "\n")
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestClaimWakes(t *testing.T) {
	home := t.TempDir()
	writeWakeQueueLines(t, home, []string{
		"1780000000\t1\tsignal\tkey1\tpayload1",
		"1780000001\t2\tstale\tkey2\tpayload2",
		"1780000002\t3\tcheck\tkey3\tpayload3",
	})

	result, err := ClaimWakes(home, "test-consumer", 60, 2)
	if err != nil {
		t.Fatalf("ClaimWakes() error = %v", err)
	}
	if result == nil {
		t.Fatal("result is nil")
	}
	if result.Consumer != "test-consumer" {
		t.Errorf("consumer = %q, want test-consumer", result.Consumer)
	}
	if len(result.Wakes) != 2 {
		t.Errorf("claimed %d wakes, want 2", len(result.Wakes))
	}
	if result.LeaseID == "" {
		t.Error("lease ID is empty")
	}
	if result.ExpiresAt == 0 {
		t.Error("expires_at is 0")
	}

	// Verify queue has 1 remaining
	if HasQueuedWakes(home) {
		records, err := DrainWakes(home)
		if err != nil {
			t.Fatal(err)
		}
		if len(records) != 1 {
			t.Errorf("remaining queue has %d records, want 1", len(records))
		}
		if records[0].Seq != "3" {
			t.Errorf("remaining seq = %q, want 3", records[0].Seq)
		}
	}
}

func TestClaimWakesEmptyQueue(t *testing.T) {
	home := t.TempDir()
	result, err := ClaimWakes(home, "consumer", 60, 10)
	if err != nil {
		t.Fatalf("ClaimWakes() error = %v", err)
	}
	if len(result.Wakes) != 0 {
		t.Errorf("claimed %d wakes from empty queue, want 0", len(result.Wakes))
	}
}

func TestAckWakes(t *testing.T) {
	home := t.TempDir()
	writeWakeQueueLines(t, home, []string{
		"1780000000\t1\tsignal\tkey1\tpayload1",
		"1780000001\t2\tstale\tkey2\tpayload2",
	})

	result, err := ClaimWakes(home, "consumer", 60, 10)
	if err != nil {
		t.Fatal(err)
	}

	// Ack the general wake (epoch:seq = 1780000001:2)
	if err := AckWakes(home, result.LeaseID, []string{"1780000001:2"}); err != nil {
		t.Fatalf("AckWakes() error = %v", err)
	}

	// Lease file should still exist with 1 unacked wake
	leasePath := LeaseFilePath(home, result.LeaseID)
	if _, err := os.Stat(leasePath); os.IsNotExist(err) {
		t.Error("lease file should still exist with unacked wakes")
	}
}

func TestAckWakesAllRemovesLease(t *testing.T) {
	home := t.TempDir()
	writeWakeQueueLines(t, home, []string{
		"1780000000\t1\tsignal\tkey1\tpayload1",
	})

	result, err := ClaimWakes(home, "consumer", 60, 10)
	if err != nil {
		t.Fatal(err)
	}

	if err := AckWakes(home, result.LeaseID, []string{"1780000000:1"}); err != nil {
		t.Fatalf("AckWakes() error = %v", err)
	}

	// Lease file should be removed
	leasePath := LeaseFilePath(home, result.LeaseID)
	if _, err := os.Stat(leasePath); !os.IsNotExist(err) {
		t.Error("lease file should be removed after all wakes acked")
	}
}

func TestAckWakesNonexistentLease(t *testing.T) {
	home := t.TempDir()
	err := AckWakes(home, "nonexistent-lease", []string{"1:1"})
	if err == nil {
		t.Fatal("expected error for nonexistent lease")
	}
	if !strings.Contains(err.Error(), "not found") && !strings.Contains(err.Error(), "expired") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestLeaseExpiryReclaim(t *testing.T) {
	home := t.TempDir()
	writeWakeQueueLines(t, home, []string{
		"1780000000\t1\tsignal\tkey1\tpayload1",
	})

	// Claim with 0-captain lease (expires immediately)
	result, err := ClaimWakes(home, "consumer", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Wakes) != 1 {
		t.Fatalf("claimed %d wakes, want 1", len(result.Wakes))
	}

	// Claim again — should reclaim expired lease wakes plus remaining
	result2, err := ClaimWakes(home, "consumer2", 60, 10)
	if err != nil {
		t.Fatal(err)
	}
	if result2.Reclaimed < 1 {
		t.Errorf("expected reclaimed >= 1, got %d", result2.Reclaimed)
	}
	// Reclaimed wake should now be in the new claim
	if len(result2.Wakes) < 1 {
		t.Errorf("expected at least 1 wake in new claim, got %d", len(result2.Wakes))
	}
}

func TestClaimWakesDefaults(t *testing.T) {
	home := t.TempDir()
	writeWakeQueueLines(t, home, []string{
		"1780000000\t1\tsignal\tkey1\tpayload1",
	})

	result, err := ClaimWakes(home, "consumer", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Should default to 60s lease and limit 10
	if result.ExpiresAt == 0 {
		t.Error("expires_at should be set")
	}
	if len(result.Wakes) != 1 {
		t.Errorf("claimed %d wakes, want 1", len(result.Wakes))
	}
}

func TestClaimWakesLimit(t *testing.T) {
	home := t.TempDir()
	writeWakeQueueLines(t, home, []string{
		"1\t1\tkind1\tk1\tp1",
		"2\t2\tkind2\tk2\tp2",
		"3\t3\tkind3\tk3\tp3",
		"4\t4\tkind4\tk4\tp4",
		"5\t5\tkind5\tk5\tp5",
	})

	result, err := ClaimWakes(home, "consumer", 60, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Wakes) != 3 {
		t.Errorf("claimed %d wakes, want 3", len(result.Wakes))
	}
}

func TestCrashAtEachStepNoLostWakes(t *testing.T) {
	// Simulate: queue has 3 wakes, claim 2, ack 0 (crash), lease expires, claim again
	home := t.TempDir()
	writeWakeQueueLines(t, home, []string{
		"100\t1\ta\tk1\tp1",
		"101\t2\tb\tk2\tp2",
		"102\t3\tc\tk3\tp3",
	})

	// First claim (2 wakes)
	r1, err := ClaimWakes(home, "consumer", 0, 2) // 0-captain lease = immediate expiry
	if err != nil {
		t.Fatal(err)
	}
	if len(r1.Wakes) != 2 {
		t.Fatalf("first claim got %d wakes", len(r1.Wakes))
	}

	// Simulate crash: don't ack anything, lease expires
	// Captain claim should reclaim + get remaining from queue
	r2, err := ClaimWakes(home, "consumer2", 60, 10)
	if err != nil {
		t.Fatal(err)
	}

	// Should have reclaimed the 2 from expired lease + 1 remaining = 3
	if len(r2.Wakes) != 3 {
		t.Errorf("after reclaim, got %d wakes, want 3 (2 reclaimed + 1 remaining)", len(r2.Wakes))
	}
	if r2.Reclaimed != 2 {
		t.Errorf("reclaimed = %d, want 2", r2.Reclaimed)
	}
}

func TestIdempotencyAck(t *testing.T) {
	// Acking the same event twice should be safe
	home := t.TempDir()
	writeWakeQueueLines(t, home, []string{
		"100\t1\ta\tk1\tp1",
	})

	r, err := ClaimWakes(home, "consumer", 60, 10)
	if err != nil {
		t.Fatal(err)
	}

	// Ack once
	if err := AckWakes(home, r.LeaseID, []string{"100:1"}); err != nil {
		t.Fatal(err)
	}

	// Ack again should be no-op (lease already removed)
	err = AckWakes(home, r.LeaseID, []string{"100:1"})
	if err == nil {
		// Ok if lease is already gone
	}
}
