//go:build !windows

package home

import (
	"os"
	"path/filepath"
	"testing"
)

// TestVerifyProtectionRefusesNotOwnerPrivateUnix confirms that verifyProtection
// fails closed when a path grants access to group or other principals. Mode
// 0644 carries read bits outside the owner, so the owner-private guarantee the
// guard is asked to confirm is absent.
func TestVerifyProtectionRefusesNotOwnerPrivateUnix(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wide.txt")
	if err := os.WriteFile(path, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if err := verifyProtection(path, false); err == nil {
		t.Fatal("verifyProtection accepted a mode granting group/other access")
	}
}

// TestVerifyProtectionRefusesDirMismatchUnix confirms that verifyProtection
// fails closed when a path declared to be a directory is actually a regular
// file: the protection contract is bound to the shape it was asked about, so
// a file at a directory-typed path means the guarantee cannot be confirmed.
func TestVerifyProtectionRefusesDirMismatchUnix(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notadir")
	if err := os.WriteFile(path, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := verifyProtection(path, true); err == nil {
		t.Fatal("verifyProtection accepted a file where a directory was required")
	}
}

// TestWriteWatcherBeatRestrictsTheStateDir: the beat secures the state
// directory it writes under instead of leaving it group or world accessible.
func TestWriteWatcherBeatRestrictsTheStateDir(t *testing.T) {
	home := t.TempDir()
	state := filepath.Join(home, "state")
	if err := os.Mkdir(state, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(state, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WriteWatcherBeat(home); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(state)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf("state dir mode = %o, want 700", got)
	}
}
