//go:build windows

package home

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAtomicCreateWindowsWriteThroughDoesNotReplace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "artifact")
	if err := AtomicCreate(path, []byte("original"), 0600); err != nil {
		t.Fatalf("AtomicCreate: %v", err)
	}
	if err := AtomicCreate(path, []byte("replacement"), 0600); !errors.Is(err, os.ErrExist) {
		t.Fatalf("AtomicCreate existing target = %v, want os.ErrExist", err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "original" {
		t.Fatalf("target = %q, %v; want original bytes", got, err)
	}
}
