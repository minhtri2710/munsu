package home

import (
	"os"
	"path/filepath"
	"runtime"
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
// the host: after a full claim the queue file must be gone (which on Windows
// requires the read handle to be closed first) and a second claim must find
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
	// On Windows, MakeDirectoryReadOnly sets a deny-write DACL on stateDir that denies
	// FILE_ADD_FILE (0x2). ClaimWakes opens the lock with os.OpenFile(O_CREATE|O_RDWR),
	// and O_CREATE maps to the Win32 OPEN_ALWAYS disposition, whose create-access check is
	// performed against the parent stateDir. That parent-directory check is refused by the
	// FILE_ADD_FILE denial before the lock's own DACL is consulted, so the failure surfaces
	// at "opening wake claim lock". This is independent of ACE inheritance and of whether
	// .wake-claim.lock already existed (it was written before MakeDirectoryReadOnly). It was
	// empirically confirmed on windows-latest in GitHub Actions run 33141246285. On POSIX,
	// chmod 0500 on stateDir permits opening the pre-created lock file, so the refusal instead
	// surfaces at os.Remove(qPath) with "removing claimed wake queue".
	wantSubstring := "removing claimed wake queue"
	if runtime.GOOS == "windows" {
		wantSubstring = "opening wake claim lock"
	}
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
