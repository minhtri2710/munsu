package home

import (
	"os"
	"testing"
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
