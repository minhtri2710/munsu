//go:build windows

package testutil

import (
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
