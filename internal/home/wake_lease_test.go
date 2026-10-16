package home

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/testutil"
)

func TestWakeLeaseIDsRejectTraversalWithoutTouchingOutsideFiles(t *testing.T) {
	t.Run("ack", func(t *testing.T) {
		home := t.TempDir()
		victim := filepath.Join(home, "victim")
		original := []byte("untouched\n")
		if err := os.WriteFile(victim, original, 0600); err != nil {
			t.Fatal(err)
		}
		if err := AckWakes(home, "../../victim", []string{"100:1"}); err == nil {
			t.Fatal("AckWakes accepted a traversal lease ID")
		}
		data, err := os.ReadFile(victim)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != string(original) {
			t.Fatalf("outside victim changed: %q", data)
		}
	})

	t.Run("resolve", func(t *testing.T) {
		home := t.TempDir()
		victim := filepath.Join(home, "victim")
		original := []byte("../../victim\tconsumer\t9999999999\t0\n100\t1\tsignal\ttask\tpayload\n")
		if err := os.WriteFile(victim, original, 0600); err != nil {
			t.Fatal(err)
		}
		if err := ResolveWake(home, "../../victim", "100:1", "done"); err == nil {
			t.Fatal("ResolveWake accepted a traversal lease ID")
		}
		data, err := os.ReadFile(victim)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != string(original) {
			t.Fatalf("outside victim changed: %q", data)
		}
	})
}

func TestClaimWakesSurfacesQueueOpenError(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(WakeQueuePath(home)), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(WakeQueuePath(home), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := ClaimWakes(home, "consumer", 60, 1); err == nil {
		t.Fatal("ClaimWakes swallowed an error opening the queue")
	}
}

func TestReclaimExpiredLeasesRetainsLeaseWhenQueueCannotBeWritten(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(LeaseDir(home), 0755); err != nil {
		t.Fatal(err)
	}
	leaseID := "lease-expired"
	original := []byte(leaseID + "\tconsumer\t0\t0\n100\t1\tsignal\ttask\tpayload\n")
	if err := os.WriteFile(LeaseFilePath(home, leaseID), original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(WakeQueuePath(home), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := ReclaimExpiredLeases(home); err == nil {
		t.Fatal("ReclaimExpiredLeases swallowed the enqueue error")
	}
	data, err := os.ReadFile(LeaseFilePath(home, leaseID))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(original) {
		t.Fatalf("expired lease was not retained: %q", data)
	}
}

func TestClaimWakesEmptyQueueDoesNotCreateLease(t *testing.T) {
	home := t.TempDir()

	result, err := ClaimWakes(home, "consumer", 60, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Wakes) != 0 || result.LeaseID != "" || result.ExpiresAt != 0 {
		t.Fatalf("empty claim = %+v", result)
	}
	entries, err := os.ReadDir(LeaseDir(home))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("empty claim created leases: %v", entries)
	}
}

// TestClaimWakesClaimsAndRemovesQueue exercises the close-before-remove path on
// the host: after a full claim the queue file must be gone and a second claim must find
// nothing, proving the claimed wakes are not re-delivered (#549).
func TestClaimWakesClaimsAndRemovesQueue(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(WakeQueuePath(home), []byte("100\t1\tk\ta\tpa\n200\t2\tk\tb\tpb\n300\t3\tk\tc\tpc\n"), 0644); err != nil {
		t.Fatal(err)
	}

	res, err := ClaimWakes(home, "consumer", 60, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Wakes) != 3 {
		t.Fatalf("claimed %d wakes, want 3", len(res.Wakes))
	}
	if _, err := os.Stat(WakeQueuePath(home)); !os.IsNotExist(err) {
		t.Fatalf("queue file should be removed after full claim: %v", err)
	}

	res2, err := ClaimWakes(home, "consumer", 60, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(res2.Wakes) != 0 {
		t.Fatalf("second claim re-delivered %d wakes, want 0", len(res2.Wakes))
	}
}

// TestClaimWakesLeavesRemainingQueue proves a partial claim keeps the unclaimed
// tail in the queue and never re-delivers the claimed head on a later claim.
func TestClaimWakesLeavesRemainingQueue(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(WakeQueuePath(home), []byte("100\t1\tk\ta\tpa\n200\t2\tk\tb\tpb\n300\t3\tk\tc\tpc\n"), 0644); err != nil {
		t.Fatal(err)
	}

	res, err := ClaimWakes(home, "consumer", 60, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Wakes) != 1 || res.Wakes[0].Key != "a" {
		t.Fatalf("first claim = %+v, want one wake with key a", res.Wakes)
	}

	data, err := os.ReadFile(WakeQueuePath(home))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("remaining queue has %d lines, want 2: %q", len(lines), data)
	}
	for _, ln := range lines {
		parts := strings.SplitN(ln, "\t", 5)
		if len(parts) >= 4 && parts[3] == "a" {
			t.Fatalf("claimed wake key a leaked into remaining queue: %q", data)
		}
	}

	res2, err := ClaimWakes(home, "consumer", 60, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(res2.Wakes) != 2 {
		t.Fatalf("second claim returned %d wakes, want 2", len(res2.Wakes))
	}
	keys := map[string]bool{}
	for _, w := range res2.Wakes {
		keys[w.Key] = true
	}
	if !keys["b"] || !keys["c"] {
		t.Fatalf("second claim missed remaining wakes: %+v", res2.Wakes)
	}
}

// TestClaimWakesRemovalErrorPropagated forces the queue removal to fail (a
// read-only state directory with the lock and lease dir already present) and
// asserts the error is surfaced rather than silently ignored (#549).
func TestClaimWakesRemovalErrorPropagated(t *testing.T) {
	home := t.TempDir()
	stateDir := filepath.Join(home, "state")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Pre-create the lock and lease dir so their opens/writes do not need the
	// state directory to be writable; only the final os.Remove(qPath) does.
	if err := os.MkdirAll(LeaseDir(home), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, ".wake-claim.lock"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(WakeQueuePath(home), []byte("100\t1\tk\ta\tpa\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	testutil.MakeDirectoryReadOnly(t, stateDir)

	_, err := ClaimWakes(home, "consumer", 60, 10)
	if err == nil {
		t.Fatal("expected error from failed queue removal, got nil")
	}
	// chmod 0500 on stateDir permits opening the pre-created lock file, so the
	// refusal surfaces at os.Remove(qPath) with "removing claimed wake queue".
	wantSubstring := "removing claimed wake queue"
	if !strings.Contains(err.Error(), wantSubstring) {
		t.Fatalf("unexpected error: %v, want substring %q", err, wantSubstring)
	}
}

// TestClaimWakesNeverLeasesProcessEventWakes proves the reserved kind is left
// in the queue, unleased, while every other record is claimed in its original
// order, and that a later claim finds no claimable record behind it.
func TestClaimWakesNeverLeasesProcessEventWakes(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	queue := "100\t1\t" + ProcessEventWakeKind + "\tev-1\tpe1\n200\t2\tk\ta\tpa\n300\t3\t" + ProcessEventWakeKind + "\tev-2\tpe2\n400\t4\tk\tb\tpb\n"
	if err := os.WriteFile(WakeQueuePath(home), []byte(queue), 0644); err != nil {
		t.Fatal(err)
	}

	res, err := ClaimWakes(home, "consumer", 60, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Wakes) != 1 || res.Wakes[0].Key != "a" {
		t.Fatalf("claimed %#v, want only wake a (the first claimable record)", res.Wakes)
	}
	rest, err := readWakeQueue(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(rest) != 3 || rest[0].Key != "ev-1" || rest[1].Key != "ev-2" || rest[2].Key != "b" {
		t.Fatalf("queue after claim = %#v, want ev-1, ev-2, b in order", rest)
	}

	res, err = ClaimWakes(home, "consumer", 60, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Wakes) != 1 || res.Wakes[0].Key != "b" {
		t.Fatalf("second claim = %#v, want only wake b", res.Wakes)
	}
	res, err = ClaimWakes(home, "consumer", 60, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Wakes) != 0 || res.LeaseID != "" {
		t.Fatalf("claim over a queue of only process-event wakes = %#v, want nothing leased", res)
	}
	rest, err = readWakeQueue(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(rest) != 2 || rest[0].Kind != ProcessEventWakeKind || rest[1].Kind != ProcessEventWakeKind {
		t.Fatalf("queue after exhausting claimable wakes = %#v, want both process-event wakes intact", rest)
	}
}

func writeQueueLines(t *testing.T, home string, lines []string) {
	t.Helper()
	writeWakeQueueForTest(t, home, strings.Join(lines, "\n"))
}

func TestClaimWakes(t *testing.T) {
	home := t.TempDir()
	writeQueueLines(t, home, []string{
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

	records, err := DrainWakes(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Seq != "3" {
		t.Fatalf("remaining queue = %+v, want only seq 3", records)
	}
}

func TestAckWakes(t *testing.T) {
	home := t.TempDir()
	writeQueueLines(t, home, []string{
		"1780000000\t1\tsignal\tkey1\tpayload1",
		"1780000001\t2\tstale\tkey2\tpayload2",
	})

	result, err := ClaimWakes(home, "consumer", 60, 10)
	if err != nil {
		t.Fatal(err)
	}

	if err := AckWakes(home, result.LeaseID, []string{"1780000001:2"}); err != nil {
		t.Fatalf("AckWakes() error = %v", err)
	}

	leasePath := LeaseFilePath(home, result.LeaseID)
	if _, err := os.Stat(leasePath); os.IsNotExist(err) {
		t.Error("lease file should still exist with unacked wakes")
	}
}

func TestAckWakesAllRemovesLease(t *testing.T) {
	home := t.TempDir()
	writeQueueLines(t, home, []string{
		"1780000000\t1\tsignal\tkey1\tpayload1",
	})

	result, err := ClaimWakes(home, "consumer", 60, 10)
	if err != nil {
		t.Fatal(err)
	}

	if err := AckWakes(home, result.LeaseID, []string{"1780000000:1"}); err != nil {
		t.Fatalf("AckWakes() error = %v", err)
	}

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
	for _, tc := range []struct {
		consumer string
		refused  bool
	}{
		{"munsu:afk", false},
		{"a\nb", true},
	} {
		t.Run(tc.consumer, func(t *testing.T) {
			home := t.TempDir()
			writeQueueLines(t, home, []string{
				"1780000000\t1\tsignal\tkey1\tpayload1",
			})

			result, err := ClaimWakes(home, tc.consumer, 0, 10)
			if tc.refused {
				if err == nil || !strings.Contains(err.Error(), "contains a control character") {
					t.Fatalf("ClaimWakes(%q) error = %v, want the control-character refusal", tc.consumer, err)
				}
				if entries, _ := os.ReadDir(LeaseDir(home)); len(entries) != 0 {
					t.Fatalf("refused claim wrote leases %v", entries)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Wakes) != 1 {
				t.Fatalf("claimed %d wakes, want 1", len(result.Wakes))
			}

			result2, err := ClaimWakes(home, "consumer2", 60, 10)
			if err != nil {
				t.Fatal(err)
			}
			if result2.Reclaimed != 1 || len(result2.Wakes) != 1 || result2.Wakes[0].Payload != "payload1" {
				t.Fatalf("second claim = reclaimed %d, wakes %+v, want payload1 reclaimed", result2.Reclaimed, result2.Wakes)
			}
		})
	}
}

func TestClaimWakesDefaults(t *testing.T) {
	home := t.TempDir()
	var lines []string
	for i := 1; i <= 11; i++ {
		lines = append(lines, fmt.Sprintf("%d\t%d\tsignal\tkey%d\tpayload%d", 1780000000+i, i, i, i))
	}
	writeQueueLines(t, home, lines)

	result, err := ClaimWakes(home, "consumer", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if result.ExpiresAt == 0 {
		t.Error("expires_at should be set")
	}
	if len(result.Wakes) != 10 {
		t.Errorf("claimed %d wakes, want the default limit 10", len(result.Wakes))
	}
}

func TestClaimWakesLimit(t *testing.T) {
	home := t.TempDir()
	writeQueueLines(t, home, []string{
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
	home := t.TempDir()
	writeQueueLines(t, home, []string{
		"100\t1\ta\tk1\tp1",
		"101\t2\tb\tk2\tp2",
		"102\t3\tc\tk3\tp3",
	})

	r1, err := ClaimWakes(home, "consumer", 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(r1.Wakes) != 2 {
		t.Fatalf("first claim got %d wakes", len(r1.Wakes))
	}

	r2, err := ClaimWakes(home, "consumer2", 60, 10)
	if err != nil {
		t.Fatal(err)
	}

	if len(r2.Wakes) != 3 {
		t.Errorf("after reclaim, got %d wakes, want 3 (2 reclaimed + 1 remaining)", len(r2.Wakes))
	}
	if r2.Reclaimed != 2 {
		t.Errorf("reclaimed = %d, want 2", r2.Reclaimed)
	}
}

func TestParity_WakeKind_Signal(t *testing.T) {
	home := t.TempDir()
	writeQueueLines(t, home, []string{
		"1781000000\t1\tsignal\tcaptain:1\tdone: task complete",
	})

	result, err := ClaimWakes(home, "test", 60, 10)
	if err != nil {
		t.Fatalf("ClaimWakes: %v", err)
	}
	if len(result.Wakes) != 1 {
		t.Fatalf("expected 1 wake, got %d", len(result.Wakes))
	}
	w := result.Wakes[0]
	if w.Kind != "signal" {
		t.Errorf("kind = %q, want signal", w.Kind)
	}
	if w.Key != "captain:1" {
		t.Errorf("key = %q, want captain:1", w.Key)
	}
	if w.Payload != "done: task complete" {
		t.Errorf("payload = %q, want 'done: task complete'", w.Payload)
	}
	if w.Seq != "1" {
		t.Errorf("seq = %q, want 1", w.Seq)
	}

	if err := AckWakes(home, result.LeaseID, []string{w.Epoch + ":" + w.Seq}); err != nil {
		t.Fatalf("AckWakes: %v", err)
	}
}
