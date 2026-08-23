package orchestrator

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIsActive_FlagNotSet(t *testing.T) {
	tmp := t.TempDir()
	if IsActive(tmp) {
		t.Error("IsActive() = true for empty home, want false")
	}
}

func TestIsActive_FlagSet(t *testing.T) {
	tmp := t.TempDir()
	flagPath := filepath.Join(tmp, afkFlagFile)
	os.MkdirAll(filepath.Dir(flagPath), 0755)
	if err := os.WriteFile(flagPath, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if !IsActive(tmp) {
		t.Error("IsActive() = false when flag file exists, want true")
	}
}

func TestIsActive_FlagRemoved(t *testing.T) {
	tmp := t.TempDir()
	flagPath := filepath.Join(tmp, afkFlagFile)
	os.MkdirAll(filepath.Dir(flagPath), 0755)
	if err := os.WriteFile(flagPath, []byte("2024-01-01T00:00:00Z\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if !IsActive(tmp) {
		t.Fatal("IsActive() = false when flag file exists")
	}
	os.Remove(flagPath)
	if IsActive(tmp) {
		t.Error("IsActive() = true after flag removed, want false")
	}
}

func TestDisable_RemovesFlag(t *testing.T) {
	tmp := t.TempDir()
	flagPath := filepath.Join(tmp, afkFlagFile)
	os.MkdirAll(filepath.Dir(flagPath), 0755)
	if err := os.WriteFile(flagPath, []byte("2024-01-01T00:00:00Z\n"), 0644); err != nil {
		t.Fatal(err)
	}
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
