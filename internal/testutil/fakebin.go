package testutil

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// bootPath is PATH as it stood before any test rewrote it. Windows fake
// installation resolves and records the shell from this path before the fake
// is launched, so changing PATH for one fixture cannot affect another.
var bootPath = os.Getenv("PATH")

// WriteFakeExecutableAt writes script as a POSIX shell program at path and
// installs any platform-native launcher needed to resolve that fake by name.
func WriteFakeExecutableAt(path, script string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		return err
	}
	return installWindowsFake(path)
}

// WriteFakeExecutable is WriteFakeExecutableAt for a test, returning the
// platform-resolvable path that runs the fake -- see FakeExecutablePath.
func WriteFakeExecutable(t *testing.T, path, script string) string {
	t.Helper()
	if err := WriteFakeExecutableAt(path, script); err != nil {
		t.Fatalf("fake %s: %v", filepath.Base(path), err)
	}
	return FakeExecutablePath(path)
}

// FakeExecutablePath returns the platform-resolvable path that runs the fake
// written at path.
func FakeExecutablePath(path string) string {
	return fakeExecutablePath(path)
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
//
// Both helpers exist because the separator is not ":" everywhere: a fixture
// that hardcodes it builds one unsplittable PATH entry on windows, and every
// fake it installed is then absent for a reason that has nothing to do with
// the contract under test (#549 group 1).
func SetPath(t *testing.T, dirs ...string) {
	t.Helper()
	t.Setenv("PATH", strings.Join(dirs, string(os.PathListSeparator)))
}

// resolvePOSIXShell picks the interpreter the windows shim hands its script
// to, searching searchPath. It prefers a shell already on that path and
// otherwise derives one from git's own install root -- every fixture that
// installs a fake also drives git, and Git for Windows ships the same shell.
// Finding none is an error rather than a skip: it means the fake would never
// run, which is a fact about the machine, not a behaviour windows lacks.
func resolvePOSIXShell(searchPath string) (string, error) {
	if p := findOnPath(searchPath, "sh.exe", "bash.exe"); p != "" {
		return p, nil
	}
	if git := findOnPath(searchPath, "git.exe"); git != "" {
		root := filepath.Dir(filepath.Dir(git)) // ...\Git\cmd or ...\Git\bin -> ...\Git
		for _, rel := range [][]string{{"usr", "bin", "sh.exe"}, {"bin", "sh.exe"}, {"usr", "bin", "bash.exe"}, {"bin", "bash.exe"}} {
			if p := filepath.Join(append([]string{root}, rel...)...); isFile(p) {
				return p, nil
			}
		}
	}
	return "", fmt.Errorf("no POSIX shell for the windows fake-binary shim on PATH=%s: %w", searchPath, errors.ErrUnsupported)
}

func findOnPath(searchPath string, names ...string) string {
	for _, dir := range filepath.SplitList(searchPath) {
		for _, name := range names {
			if p := filepath.Join(dir, name); isFile(p) {
				return p
			}
		}
	}
	return ""
}

func isFile(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}
