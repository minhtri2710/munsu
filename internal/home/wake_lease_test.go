package home

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/testutil/fsaccess"
)

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

	fsaccess.MakeReadOnly(t, stateDir)

	_, err := ClaimWakes(home, "consumer", 60, 10)
	if err == nil {
		t.Fatal("expected error from failed queue removal, got nil")
	}
	if !strings.Contains(err.Error(), "removing claimed wake queue") {
		t.Fatalf("unexpected error: %v", err)
	}
}
