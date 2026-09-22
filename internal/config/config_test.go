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

func TestConfigRejectsUnknownKeysBeforeFilesystemAccess(t *testing.T) {
	home := t.TempDir()
	for _, key := range []string{"../../escaped", "base.json"} {
		t.Run(key, func(t *testing.T) {
			if err := Set(home, key, "pwned"); err == nil {
				t.Errorf("Set(%q) succeeded; want unknown-key refusal", key)
			}
			if _, err := Get(home, key); err == nil {
				t.Errorf("Get(%q) succeeded; want unknown-key refusal", key)
			}
		})
	}

	outside := filepath.Join(ConfigDir(home), "../../escaped")
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatalf("unknown-key write escaped config directory at %s: %v", outside, err)
	}
	if _, err := os.Stat(filepath.Join(ConfigDir(home), "base.json")); !os.IsNotExist(err) {
		t.Fatalf("unknown-key write created base.json: %v", err)
	}
	if _, err := os.Stat(ConfigDir(home)); !os.IsNotExist(err) {
		t.Fatalf("unknown-key access created config directory: %v", err)
	}
}

func TestGetFromFileOnly(t *testing.T) {
	// Config only from the flat file, no environment involvement.
	tmp := t.TempDir()
	if err := Set(tmp, "model-allowlist", "bar"); err != nil {
		t.Fatal(err)
	}

	val, err := Get(tmp, "model-allowlist")
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
	expected := []string{"backend", "parent-home", "soldier-harness", "captain-harness", "model", "model-allowlist", "default-mode", "wake-delivery-mode", "require-no-mistakes", "allow-direct-pr-fallback", "afk-digest-window", "afk-wedge-stale-beat", "afk-wedge-max-repeat", "afk-max-defer", "install-root"}
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
