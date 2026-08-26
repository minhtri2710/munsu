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
	if want := filepath.Join(withoutUsrBash, "bin", "bash.exe"); shell != want {
		t.Fatalf("fallback shell = %q, want %q", shell, want)
	}
	if len(dirs) != 2 || dirs[0] != filepath.Join(withoutUsrBash, "usr", "bin") || dirs[1] != filepath.Join(withoutUsrBash, "bin") {
		t.Fatalf("fallback support dirs = %q, want usr/bin then bin", dirs)
	}
}

func TestResolveBashCandidatesSelectsCompleteEnvironment(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()
	firstShell := filepath.Join(first, "bash")
	secondShell := WriteFakeExecutable(t, filepath.Join(second, "bash"), "#!/usr/bin/env sh\nexit 0\n")
	cat := WriteFakeExecutable(t, filepath.Join(second, "cat"), "#!/usr/bin/env sh\nexit 0\n")
	mkdir := WriteFakeExecutable(t, filepath.Join(second, "mkdir"), "#!/usr/bin/env sh\nexit 0\n")
	if err := os.WriteFile(firstShell, nil, 0644); err != nil {
		t.Fatal(err)
	}
	names := []string{filepath.Base(cat), filepath.Base(mkdir)}
	shell, dirs, err := resolveBashCandidates(strings.Join([]string{first, second}, string(os.PathListSeparator)), []bashCandidate{{shell: firstShell}, {shell: secondShell}}, append([]string{filepath.Base(secondShell)}, names...)...)
	if err != nil {
		t.Fatal(err)
	}
	if shell != secondShell || len(dirs) != 1 || dirs[0] != second {
		t.Fatalf("shell=%q dirs=%q, want shell %q and dirs %q", shell, dirs, secondShell, []string{second})
	}
}

func TestResolveBashCandidatesPreservesFirstError(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()
	firstShell := WriteFakeExecutable(t, filepath.Join(first, "bash"), "#!/usr/bin/env sh\nexit 0\n")
	secondShell := WriteFakeExecutable(t, filepath.Join(second, "bash"), "#!/usr/bin/env sh\nexit 0\n")
	cat := filepath.Base(WriteFakeExecutable(t, filepath.Join(first, "cat"), "#!/usr/bin/env sh\nexit 0\n"))
	mkdir := filepath.Base(filepath.Join(first, "mkdir"))
	_, _, err := resolveBashCandidates(strings.Join([]string{first, second}, string(os.PathListSeparator)), []bashCandidate{{shell: firstShell}, {shell: secondShell}}, filepath.Base(firstShell), cat, mkdir)
	if !errors.Is(err, errors.ErrUnsupported) || !strings.Contains(err.Error(), mkdir) || !strings.Contains(err.Error(), firstShell) || strings.Contains(err.Error(), secondShell) {
		t.Fatalf("error = %v, want first actionable incomplete-layout error", err)
	}
}

func TestCompleteBashDirsRejectsMissingUtilities(t *testing.T) {
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "bash.exe"))
	if _, err := completeBashDirs(dir, filepath.Join(dir, "bash.exe"), nil, "bash.exe", "cat.exe", "mkdir.exe"); !errors.Is(err, errors.ErrUnsupported) || !strings.Contains(err.Error(), "cat.exe") || !strings.Contains(err.Error(), dir) {
		t.Fatalf("completeBashDirs error = %v, want actionable missing-cat error", err)
	}

	touch(t, filepath.Join(dir, "cat.exe"))
	touch(t, filepath.Join(dir, "mkdir.exe"))
	dirs, err := completeBashDirs(dir, filepath.Join(dir, "bash.exe"), nil, "bash.exe", "cat.exe", "mkdir.exe")
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) != 1 || dirs[0] != dir {
		t.Fatalf("complete support dirs = %q, want %q", dirs, []string{dir})
	}

	shellDir := t.TempDir()
	utilityDir := t.TempDir()
	shell := WriteFakeExecutable(t, filepath.Join(shellDir, "bash"), "#!/usr/bin/env sh\nexit 0\n")
	cat := WriteFakeExecutable(t, filepath.Join(utilityDir, "cat"), "#!/usr/bin/env sh\nexit 0\n")
	mkdir := WriteFakeExecutable(t, filepath.Join(utilityDir, "mkdir"), "#!/usr/bin/env sh\nexit 0\n")
	searchPath := strings.Join([]string{shellDir, utilityDir}, string(os.PathListSeparator))
	dirs, err = completeBashDirs(searchPath, shell, []string{utilityDir, shellDir, utilityDir}, filepath.Base(shell), filepath.Base(cat), filepath.Base(mkdir))
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) != 2 || dirs[0] != utilityDir || dirs[1] != shellDir {
		t.Fatalf("separated support dirs = %q, want utilities then shell", dirs)
	}
	for _, name := range []string{filepath.Base(shell), filepath.Base(cat), filepath.Base(mkdir)} {
		if findOnPath(strings.Join(dirs, string(os.PathListSeparator)), name) == "" {
			t.Fatalf("separated support PATH does not resolve %s", name)
		}
	}
}

func TestCompleteBashDirsRejectsUnresolvableSupportPath(t *testing.T) {
	name := "fixture-tool"
	findCalls := 0
	find := func(_ string, names ...string) string {
		findCalls++
		if findCalls == 1 {
			return filepath.Join(t.TempDir(), names[0])
		}
		return ""
	}

	_, err := completeBashDirsWithFind(t.TempDir(), filepath.Join(t.TempDir(), "shell"), nil, find, name)
	if !errors.Is(err, errors.ErrUnsupported) || !strings.Contains(err.Error(), name) || !strings.Contains(err.Error(), "support PATH") {
		t.Fatalf("completeBashDirsWithFind error = %v, want unsupported unresolved-support-path error", err)
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
