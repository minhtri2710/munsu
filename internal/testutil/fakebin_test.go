package testutil

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestResolvePOSIXShell exercises the windows shim's shell resolution on every
// host. Nothing else can: the shim is only written on windows, so a bug in the
// search would otherwise be invisible until an observation run, and the failure
// it produces there ("binary not found on PATH") is the very symptom the shim
// exists to remove.
func TestResolvePOSIXShell(t *testing.T) {
	onPath := t.TempDir()
	touch(t, filepath.Join(onPath, "sh.exe"))
	gitRoot := t.TempDir()
	touch(t, filepath.Join(gitRoot, "cmd", "git.exe"))
	touch(t, filepath.Join(gitRoot, "usr", "bin", "sh.exe"))

	tests := []struct {
		name   string
		dirs   []string
		want   string
		absent bool
	}{
		{name: "shell on the search path", dirs: []string{onPath}, want: filepath.Join(onPath, "sh.exe")},
		{name: "derived from git's install root", dirs: []string{filepath.Join(gitRoot, "cmd")}, want: filepath.Join(gitRoot, "usr", "bin", "sh.exe")},
		{name: "neither", dirs: []string{t.TempDir()}, absent: true},
		{name: "empty search path", dirs: nil, absent: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolvePOSIXShell(strings.Join(tt.dirs, string(os.PathListSeparator)))
			if tt.absent {
				if !errors.Is(err, errors.ErrUnsupported) {
					t.Fatalf("err = %v, want unsupported; got shell %q", err, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolvePOSIXShell: %v", err)
			}
			if got != tt.want {
				t.Errorf("shell = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestWriteFakeExecutableAtWritesTheScriptVerbatim pins the property the whole
// shim rests on: the shell script is the single source of the fake's behaviour
// on both platforms, so it is never rewritten for the host.
func TestWriteFakeExecutableAtWritesTheScriptVerbatim(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "herdr")
	script := "#!/bin/sh\nprintf '%s\\n' hello\n"
	if err := WriteFakeExecutableAt(path, script); err != nil {
		t.Fatalf("WriteFakeExecutableAt: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != script {
		t.Errorf("script = %q, want %q", got, script)
	}
}

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o755); err != nil {
		t.Fatal(err)
	}
}
