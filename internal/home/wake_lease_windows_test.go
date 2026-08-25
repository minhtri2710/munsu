//go:build windows

package home

import (
	"os"
	"path/filepath"
	"testing"
)

// TestClaimWakesRemovesQueueAfterFullClaim pins the windows half of #549: a
// full claim must remove the queue file so the claimed wakes are not
// re-delivered by a later drain. On windows an open queue handle blocks
// os.Remove, so this exercises the close-before-remove path end to end. This
// file is compiled by the goos-vet lane (GOOS=windows go vet ./...) but is not
// executed on ubuntu, so the native removal behaviour stays unproven here.
func TestClaimWakesRemovesQueueAfterFullClaim(t *testing.T) {
	home := t.TempDir()
	// WakeQueuePath lives under state/, which ClaimWakes reads but does not
	// create. Its unix sibling TestClaimWakesClaimsAndRemovesQueue does this
	// too; this file never ran on ubuntu, so the omission went unmeasured
	// until #549's observation lane executed it.
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
