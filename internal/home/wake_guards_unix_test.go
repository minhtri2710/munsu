//go:build !windows

package home

import (
	"os"
	"strings"
	"testing"
)

// TestCheckWakeMutationWritableRejectsUnwritableLeaseDirForRemove proves the
// writability precheck refuses a lease removal when the lease directory itself
// is not writable, so the removal fails closed before touching the file. The
// mode-bit check reads the stored permissions, so this holds even when the test
// runs as root. Unix-only: it depends on POSIX directory write permission.
func TestCheckWakeMutationWritableRejectsUnwritableLeaseDirForRemove(t *testing.T) {
	home := t.TempDir()
	leaseDir := LeaseDir(home)
	if err := os.MkdirAll(leaseDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(LeaseFilePath(home, "lease-1"), []byte("lease"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(leaseDir, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(leaseDir, 0700) })

	err := checkWakeMutationWritable(home, wakeMutation{leaseAction: wakeLeaseActionRemove, leaseID: "lease-1"})
	if err == nil || !strings.Contains(err.Error(), "removing lease file") {
		t.Fatalf("checkWakeMutationWritable removing from an unwritable lease dir: got %v, want a removing-lease-file error", err)
	}
}
