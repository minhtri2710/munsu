package testutil

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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
	shell, dirs, err := resolveBashCandidates(strings.Join([]string{first, second}, string(os.PathListSeparator)), []string{firstShell, secondShell}, append([]string{filepath.Base(secondShell)}, names...)...)
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
	_, _, err := resolveBashCandidates(strings.Join([]string{first, second}, string(os.PathListSeparator)), []string{firstShell, secondShell}, filepath.Base(firstShell), cat, mkdir)
	if !errors.Is(err, errors.ErrUnsupported) || !strings.Contains(err.Error(), mkdir) || !strings.Contains(err.Error(), firstShell) || strings.Contains(err.Error(), secondShell) {
		t.Fatalf("error = %v, want first actionable incomplete-layout error", err)
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

	_, err := completeBashDirsWithFind(t.TempDir(), filepath.Join(t.TempDir(), "shell"), find, name)
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

func TestCompleteBashDirsRejectsNonExecutableFallback(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"bash", "cat", "mkdir"} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, nil, 0644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := completeBashDirs(dir, filepath.Join(dir, "bash"), "bash", "cat", "mkdir"); !errors.Is(err, errors.ErrUnsupported) || !strings.Contains(err.Error(), "bash") {
		t.Fatalf("completeBashDirs error = %v, want unsupported shell error", err)
	}
}

func TestCompleteBashDirsPreservesIncompleteFallbackError(t *testing.T) {
	dir := t.TempDir()
	bash := filepath.Join(dir, "bash")
	if err := os.WriteFile(bash, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := completeBashDirs(dir, bash, "bash", "cat", "mkdir"); !errors.Is(err, errors.ErrUnsupported) || !strings.Contains(err.Error(), "cat") || !strings.Contains(err.Error(), dir) {
		t.Fatalf("completeBashDirs error = %v, want actionable missing-cat error", err)
	}
}
