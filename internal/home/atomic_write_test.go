package home

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestAtomicWriteReplacesWithMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc")
	if err := os.WriteFile(path, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := AtomicWrite(path, []byte("new"), 0600); err != nil {
		t.Fatalf("AtomicWrite: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "new" {
		t.Fatalf("content = %q, %v; want %q", got, err, "new")
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("mode = %v, %v; want 0600", info.Mode().Perm(), err)
		}
	}
	if leftovers, _ := filepath.Glob(filepath.Join(dir, ".home-write-*")); len(leftovers) != 0 {
		t.Fatalf("temp files left behind: %v", leftovers)
	}
}

// TestAtomicWriteRefusesUnsyncedData pins durability: data that cannot be
// fsynced is never renamed into place, and the old content survives.
func TestAtomicWriteRefusesUnsyncedData(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc")
	if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	syncErr := errors.New("injected fsync failure")
	orig := syncTemp
	syncTemp = func(*os.File) error { return syncErr }
	t.Cleanup(func() { syncTemp = orig })

	if err := AtomicWrite(path, []byte("new"), 0600); !errors.Is(err, syncErr) {
		t.Fatalf("AtomicWrite err = %v, want the sync failure", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "old" {
		t.Fatalf("content = %q, want the old content kept", got)
	}
	if leftovers, _ := filepath.Glob(filepath.Join(dir, ".home-write-*")); len(leftovers) != 0 {
		t.Fatalf("temp files left behind: %v", leftovers)
	}
}
