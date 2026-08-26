package testutil

import (
	"errors"
	"os"
	"os/exec"
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
func TestResolveBashShellSupportPaths(t *testing.T) {
	gitRoot := t.TempDir()
	touch(t, filepath.Join(gitRoot, "cmd", "git.exe"))
	touch(t, filepath.Join(gitRoot, "bin", "bash.exe"))
	touch(t, filepath.Join(gitRoot, "usr", "bin", "bash.exe"))
	touch(t, filepath.Join(gitRoot, "usr", "bin", "cat.exe"))
	touch(t, filepath.Join(gitRoot, "usr", "bin", "mkdir.exe"))

	shell, dirs, ok := resolveGitBash(filepath.Join(gitRoot, "cmd"))
	if !ok {
		t.Fatal("resolveGitBash did not find the Git bash layout")
	}
	var err error
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(gitRoot, "usr", "bin", "bash.exe"); shell != want {
		t.Fatalf("shell = %q, want %q", shell, want)
	}
	if len(dirs) != 2 || dirs[0] != filepath.Join(gitRoot, "usr", "bin") || dirs[1] != filepath.Join(gitRoot, "bin") {
		t.Fatalf("support dirs = %q, want usr/bin then bin", dirs)
	}
	path := strings.Join(dirs, string(os.PathListSeparator))
	for _, name := range []string{"bash.exe", "cat.exe", "mkdir.exe"} {
		if got := findOnPath(path, name); got == "" {
			t.Fatalf("support PATH does not resolve %s", name)
		}
	}

	withoutUsrBash := t.TempDir()
	touch(t, filepath.Join(withoutUsrBash, "cmd", "git.exe"))
	touch(t, filepath.Join(withoutUsrBash, "bin", "bash.exe"))
	touch(t, filepath.Join(withoutUsrBash, "usr", "bin", "cat.exe"))
	shell, dirs, ok = resolveGitBash(filepath.Join(withoutUsrBash, "cmd"))
	if !ok {
		t.Fatal("resolveGitBash did not find the Git bash fallback")
	}
	err = nil
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(withoutUsrBash, "bin", "bash.exe"); shell != want {
		t.Fatalf("fallback shell = %q, want %q", shell, want)
	}
	if len(dirs) != 2 || dirs[0] != filepath.Join(withoutUsrBash, "usr", "bin") || dirs[1] != filepath.Join(withoutUsrBash, "bin") {
		t.Fatalf("fallback support dirs = %q, want usr/bin then bin", dirs)
	}
}

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

func TestFakeOnPathRunsShellBehavior(t *testing.T) {
	name := "fake-args"
	FakeOnPath(t, name, "#!/bin/sh\nprintf 'out:%s:%s\\n' \"$1\" \"$2\"\nprintf 'err\\n' >&2\nexit 7\n")
	cmd := exec.Command(name, "one", "two")
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("fake command succeeded, want exit status 7")
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 7 {
		t.Fatalf("err = %v, want exit status 7", err)
	}
	if got, want := string(output), "out:one:two\nerr\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestFakeExecutablePathResolvesOnPath(t *testing.T) {
	dir := t.TempDir()
	path := WriteFakeExecutable(t, filepath.Join(dir, "fake"), "#!/bin/sh\nprintf ok\n")
	SetPath(t, dir)
	resolved, err := exec.LookPath(filepath.Base(path))
	if err != nil {
		t.Fatalf("LookPath: %v", err)
	}
	if resolved != path {
		t.Fatalf("resolved = %q, want %q", resolved, path)
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
