//go:build !windows

package testutil

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompleteBashDirsRejectsNonExecutableFallback(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"bash", "cat", "mkdir"} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, nil, 0644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := completeBashDirs(dir, filepath.Join(dir, "bash"), nil, "bash", "cat", "mkdir"); !errors.Is(err, errors.ErrUnsupported) || !strings.Contains(err.Error(), "bash") {
		t.Fatalf("completeBashDirs error = %v, want unsupported shell error", err)
	}
}

func TestCompleteBashDirsPreservesIncompleteFallbackError(t *testing.T) {
	dir := t.TempDir()
	bash := filepath.Join(dir, "bash")
	if err := os.WriteFile(bash, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := completeBashDirs(dir, bash, nil, "bash", "cat", "mkdir"); !errors.Is(err, errors.ErrUnsupported) || !strings.Contains(err.Error(), "cat") || !strings.Contains(err.Error(), dir) {
		t.Fatalf("completeBashDirs error = %v, want actionable missing-cat error", err)
	}
}
