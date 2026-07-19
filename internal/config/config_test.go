package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSetAndGet(t *testing.T) {
	tmp := t.TempDir()

	// Set a value
	if err := Set(tmp, "backend", "tmux"); err != nil {
		t.Fatal(err)
	}

	// Read it back
	val, err := Get(tmp, "backend")
	if err != nil {
		t.Fatal(err)
	}
	if val != "tmux" {
		t.Errorf("Get() = %q, want %q", val, "tmux")
	}

	// File should exist with the value
	data, err := os.ReadFile(filepath.Join(ConfigDir(tmp), "backend"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "tmux\n" {
		t.Errorf("file content = %q, want %q", string(data), "tmux\n")
	}
}

func TestGetNotFound(t *testing.T) {
	tmp := t.TempDir()
	_, err := Get(tmp, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent key, got nil")
	}
}

func TestGetOverrideEnv(t *testing.T) {
	tmp := t.TempDir()

	// Write a value to file
	if err := Set(tmp, "backend", "tmux"); err != nil {
		t.Fatal(err)
	}

	// Override via env var
	os.Setenv("MUNSU_BACKEND_OVERRIDE", "docker")
	defer os.Unsetenv("MUNSU_BACKEND_OVERRIDE")

	val, err := Get(tmp, "backend")
	if err != nil {
		t.Fatal(err)
	}
	if val != "docker" {
		t.Errorf("Get() = %q, want %q", val, "docker")
	}
}

func TestGetOverrideEnvOnly(t *testing.T) {
	// Config only from env override, no file
	os.Setenv("MUNSU_FOO_OVERRIDE", "bar")
	defer os.Unsetenv("MUNSU_FOO_OVERRIDE")

	val, err := Get(t.TempDir(), "foo")
	if err != nil {
		t.Fatal(err)
	}
	if val != "bar" {
		t.Errorf("Get() = %q, want %q", val, "bar")
	}
}

func TestSetOverwrites(t *testing.T) {
	tmp := t.TempDir()

	if err := Set(tmp, "backend", "tmux"); err != nil {
		t.Fatal(err)
	}
	if err := Set(tmp, "backend", "docker"); err != nil {
		t.Fatal(err)
	}

	val, err := Get(tmp, "backend")
	if err != nil {
		t.Fatal(err)
	}
	if val != "docker" {
		t.Errorf("Get() = %q, want %q", val, "docker")
	}
}

func TestKnownKeys(t *testing.T) {
	known := KnownKeys
	expected := []string{"backend", "soldier-harness", "captain-harness", "model", "backlog-backend", "default-mode", "require-no-mistakes"}
	if len(known) != len(expected) {
		t.Errorf("KnownKeys length = %d, want %d", len(known), len(expected))
	}
	for i, k := range expected {
		if i < len(known) && known[i] != k {
			t.Errorf("KnownKeys[%d] = %q, want %q", i, known[i], k)
		}
	}
}

func TestIsKnownKey(t *testing.T) {
	tests := []struct {
		key      string
		expected bool
	}{
		{"backend", true},
		{"soldier-harness", true},
		{"captain-harness", true},
		{"backlog-backend", true},
		{"default-mode", true},
		{"unknown", false},
		{"nonexistent", false},
		{"", false},
		{"BACKEND", false}, // case-sensitive
	}
	for _, tt := range tests {
		got := IsKnownKey(tt.key)
		if got != tt.expected {
			t.Errorf("IsKnownKey(%q) = %v, want %v", tt.key, got, tt.expected)
		}
	}
}
