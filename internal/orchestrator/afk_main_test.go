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

func TestStatus_NoFlagFile(t *testing.T) {
	tmp := t.TempDir()
	active, startedAt, err := Status(tmp)
	if err != nil {
		t.Fatalf("Status() with no flag: unexpected error: %v", err)
	}
	if active {
		t.Error("Status() with no flag: active=true, want false")
	}
	if startedAt != "" {
		t.Errorf("Status() with no flag: startedAt=%q, want empty", startedAt)
	}
}

func TestStatus_WithFlag(t *testing.T) {
	tmp := t.TempDir()
	flagPath := filepath.Join(tmp, afkFlagFile)
	os.MkdirAll(filepath.Dir(flagPath), 0755)
	ts := "2024-06-01T12:00:00Z"
	if err := os.WriteFile(flagPath, []byte(ts+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	active, startedAt, err := Status(tmp)
	if err != nil {
		t.Fatalf("Status() with flag: unexpected error: %v", err)
	}
	if !active {
		t.Error("Status() with flag: active=false, want true")
	}
	if startedAt != ts {
		t.Errorf("Status() with flag: startedAt=%q, want %q", startedAt, ts)
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
		t.Fatalf("Disable() captain call: unexpected error: %v", err)
	}
}
