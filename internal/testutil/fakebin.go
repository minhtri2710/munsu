package testutil

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// bootPath is PATH as it stood before any test rewrote it, so changing PATH
// for one fixture cannot affect which bash another resolves.
var bootPath = os.Getenv("PATH")

// WriteFakeExecutableAt writes script as a POSIX shell program at path.
func WriteFakeExecutableAt(path, script string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		return err
	}
	return nil
}

// WriteFakeExecutable is WriteFakeExecutableAt for a test, returning the path
// that runs the fake.
func WriteFakeExecutable(t *testing.T, path, script string) string {
	t.Helper()
	if err := WriteFakeExecutableAt(path, script); err != nil {
		t.Fatalf("fake %s: %v", filepath.Base(path), err)
	}
	return path
}

// FakeOnPath installs one fake program in a fresh directory and prepends that
// directory to PATH for the duration of the test.
func FakeOnPath(t *testing.T, name, script string) string {
	t.Helper()
	dir := t.TempDir()
	path := WriteFakeExecutable(t, filepath.Join(dir, name), script)
	PrependPath(t, dir)
	return path
}

// PrependPath puts dirs, in order, ahead of the current PATH for the duration
// of the test.
func PrependPath(t *testing.T, dirs ...string) {
	t.Helper()
	t.Setenv("PATH", strings.Join(append(dirs, os.Getenv("PATH")), string(os.PathListSeparator)))
}

// SetPath replaces PATH with exactly dirs for the duration of the test, for
// fixtures that decide presence by what they installed and nothing else.
func SetPath(t *testing.T, dirs ...string) {
	t.Helper()
	t.Setenv("PATH", strings.Join(dirs, string(os.PathListSeparator)))
}

func findOnPath(searchPath string, names ...string) string {
	for _, dir := range filepath.SplitList(searchPath) {
		for _, name := range names {
			if p := filepath.Join(dir, name); isExecutable(p) {
				return p
			}
		}
	}
	return ""
}

func isExecutable(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular() && st.Mode()&0111 != 0
}

func resolveBashShell(searchPath string) (string, []string, error) {
	candidates := []string{"/bin/bash", "/usr/bin/bash"}
	if p := findOnPath(searchPath, "bash"); p != "" {
		candidates = append(candidates, p)
	}
	return resolveBashCandidates(searchPath, candidates, "bash", "cat", "mkdir")
}

func resolveBashCandidates(searchPath string, candidates []string, names ...string) (string, []string, error) {
	var firstErr error
	for _, shell := range candidates {
		if !isExecutable(shell) {
			continue
		}
		dirs, err := completeBashDirs(searchPath, shell, names...)
		if err == nil {
			return shell, dirs, nil
		}
		if firstErr == nil {
			firstErr = fmt.Errorf("bash candidate %s: %w", shell, err)
		}
	}
	if firstErr != nil {
		return "", nil, firstErr
	}
	return "", nil, fmt.Errorf("no executable bash candidate on PATH=%s: %w", searchPath, errors.ErrUnsupported)
}

func completeBashDirs(searchPath, shell string, names ...string) ([]string, error) {
	return completeBashDirsWithFind(searchPath, shell, findOnPath, names...)
}

func completeBashDirsWithFind(searchPath, shell string, find func(string, ...string) string, names ...string) ([]string, error) {
	dirs := []string{filepath.Dir(shell)}
	lookupPath := strings.Join(append(append([]string{}, dirs...), filepath.SplitList(searchPath)...), string(os.PathListSeparator))
	for _, name := range names {
		path := find(lookupPath, name)
		if path == "" {
			return nil, fmt.Errorf("bash fixture requires %s on PATH=%s: %w", name, searchPath, errors.ErrUnsupported)
		}
		dirs = appendUnique(dirs, filepath.Dir(path))
	}
	for _, name := range names {
		if find(strings.Join(dirs, string(os.PathListSeparator)), name) == "" {
			return nil, fmt.Errorf("bash fixture cannot resolve %s from support PATH: %w", name, errors.ErrUnsupported)
		}
	}
	return dirs, nil
}

func appendUnique(dirs []string, values ...string) []string {
	for _, value := range values {
		found := false
		for _, dir := range dirs {
			if dir == value {
				found = true
				break
			}
		}
		if !found {
			dirs = append(dirs, value)
		}
	}
	return dirs
}

// BashShell returns a real executable bash from an environment that can also
// resolve the external utilities required by generated launch scripts.
func BashShell(t *testing.T) string {
	t.Helper()
	shell, _, err := resolveBashShell(bootPath)
	if err != nil {
		t.Fatalf("resolve bash shell: %v", err)
	}
	return shell
}

// BashShellDirs returns ordered, deduplicated fixture PATH directories that
// resolve bash, cat, and mkdir.
func BashShellDirs(t *testing.T) []string {
	t.Helper()
	_, dirs, err := resolveBashShell(bootPath)
	if err != nil {
		t.Fatalf("resolve bash shell: %v", err)
	}
	return dirs
}
