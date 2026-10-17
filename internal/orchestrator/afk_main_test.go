package orchestrator

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

func TestIsActive_FlagNotSet(t *testing.T) {
	tmp := t.TempDir()
	if IsActive(tmp) {
		t.Error("IsActive() = true for empty home, want false")
	}
}

// TestIsActive_FlagOwner pins that the consent flag counts only while a daemon
// holds the AFK lock: a daemon killed without cleanup leaves the flag and its
// pid in the lock file, but no flock.
func TestIsActive_FlagOwner(t *testing.T) {
	cases := []struct {
		name string
		hold func(t *testing.T, homeDir string)
		want bool
	}{
		{name: "dead owner", hold: func(t *testing.T, homeDir string) {
			if err := os.WriteFile(home.AFKLockPath(homeDir), []byte("99999\t2024-01-01T00:00:00Z\n"), 0600); err != nil {
				t.Fatal(err)
			}
		}, want: false},
		{name: "live owner", hold: func(t *testing.T, homeDir string) { holdAFKLockFor(t, homeDir, os.Getpid()) }, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			setAFKFlag(t, tmp)
			tc.hold(t, tmp)
			if got := IsActive(tmp); got != tc.want {
				t.Errorf("IsActive() = %v, want %v", got, tc.want)
			}
		})
	}
}

func setAFKFlag(t *testing.T, homeDir string) {
	t.Helper()
	flagPath := filepath.Join(homeDir, afkFlagFile)
	if err := os.MkdirAll(filepath.Dir(flagPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(flagPath, []byte("2024-01-01T00:00:00Z\n"), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestDisable_RemovesFlag(t *testing.T) {
	tmp := t.TempDir()
	setAFKFlag(t, tmp)
	holdAFKLockFor(t, tmp, os.Getpid())
	if !IsActive(tmp) {
		t.Fatal("IsActive() should be true before Disable()")
	}
	if err := Disable(tmp); err != nil {
		t.Fatalf("Disable() error: %v", err)
	}
	if IsActive(tmp) {
		t.Error("IsActive() = true after Disable(), want false")
	}
}

func TestDisable_Idempotent(t *testing.T) {
	tmp := t.TempDir()
	// Disable() on an already-clean home should not error
	if err := Disable(tmp); err != nil {
		t.Fatalf("Disable() on empty home: unexpected error: %v", err)
	}
	// Repeated calls should also succeed
	if err := Disable(tmp); err != nil {
		t.Fatalf("Disable() second call: unexpected error: %v", err)
	}
}
