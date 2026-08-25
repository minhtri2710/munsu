//go:build windows

package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestFakeExecutablePathUsesNativeLauncher(t *testing.T) {
	dir := t.TempDir()
	path := WriteFakeExecutable(t, filepath.Join(dir, "fake"), "#!/bin/sh\nprintf native\\n")
	SetPath(t, dir)
	resolved, err := exec.LookPath("fake")
	if err != nil {
		t.Fatalf("LookPath: %v", err)
	}
	if !strings.HasSuffix(strings.ToLower(resolved), ".exe") {
		t.Fatalf("resolved = %q, want .exe launcher", resolved)
	}
	if resolved != path {
		t.Fatalf("resolved = %q, want %q", resolved, path)
	}
}

// TestFakeLauncherIsDeletableAfterItRuns pins why the launcher is a copy of the
// test binary rather than a hard link to it. A hard link shares the running
// binary's image section, and windows refuses to unlink a file that has one --
// so every fake stayed undeletable for the whole test process and t.TempDir
// cleanup failed with "Access is denied" (#549). The removal below is the
// assertion: this test binary is still running while it happens.
func TestFakeLauncherIsDeletableAfterItRuns(t *testing.T) {
	dir := t.TempDir()
	path := WriteFakeExecutable(t, filepath.Join(dir, "fake"), "#!/bin/sh\nprintf ran\n")
	out, err := exec.Command(path).Output()
	if err != nil {
		t.Fatalf("running the fake at %s: %v", path, err)
	}
	if got := strings.TrimSpace(string(out)); got != "ran" {
		t.Fatalf("fake stdout = %q, want %q", got, "ran")
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("removing the launcher while this test binary runs: %v", err)
	}
}
