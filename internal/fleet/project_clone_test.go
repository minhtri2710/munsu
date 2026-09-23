package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestAddCloneTimesOutAndCleansUp runs Add against a fake `git` that creates
// the clone directory and then hangs: Add must fail with the timeout, register
// nothing, and remove the directory it created.
func TestAddCloneTimesOutAndCleansUp(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\n/bin/mkdir -p \"$3\"\nexec /bin/sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "git.bat"), []byte("@mkdir \"%3\"\r\n@ping -n 30 127.0.0.1 >nul\r\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("PATHEXT", ".BAT;.EXE")
	old := cloneTimeout
	cloneTimeout = 300 * time.Millisecond
	t.Cleanup(func() { cloneTimeout = old })

	homeDir := t.TempDir()
	if _, err := openRegistry(homeDir); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	err := Add(homeDir, "hung", "https://example.invalid/hung.git", "", false)
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("Add returned after %s; the clone bound did not hold", elapsed)
	}
	if err == nil || !strings.Contains(err.Error(), "timed out after "+cloneTimeout.String()) {
		t.Fatalf("Add error = %v, want the clone timeout", err)
	}
	if _, statErr := os.Lstat(filepath.Join(ProjectsDir(homeDir), "hung")); !os.IsNotExist(statErr) {
		t.Fatalf("partial clone directory left behind: %v", statErr)
	}
	projects, err := List(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 0 {
		t.Fatalf("timed-out clone registered projects: %+v", projects)
	}
}
