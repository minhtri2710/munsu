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
// otherwise derives one from a Git for Windows install on PATH, which ships
// the same shell.
// Finding none is an error rather than a skip: it means the fake would never
// run, which is a fact about the machine, not a behaviour windows lacks.
func resolvePOSIXShell(searchPath string) (string, error) {
	if p := findOnPath(searchPath, "sh.exe", "bash.exe"); p != "" {
		return p, nil
	}
	if git := findOnPath(searchPath, "git.exe"); git != "" {
		root := filepath.Dir(filepath.Dir(git)) // ...\Git\cmd or ...\Git\bin -> ...\Git
		for _, rel := range [][]string{{"usr", "bin", "sh.exe"}, {"bin", "sh.exe"}, {"usr", "bin", "bash.exe"}, {"bin", "bash.exe"}} {
			if p := filepath.Join(append([]string{root}, rel...)...); isExecutable(p) {
				return p, nil
			}
		}
	}
	return "", fmt.Errorf("no POSIX shell for the windows fake-binary shim on PATH=%s: %w", searchPath, errors.ErrUnsupported)
}

func resolveGitBash(searchPath string) (string, []string, bool) {
	git := findOnPath(searchPath, "git.exe")
	if git == "" {
		return "", nil, false
	}
	root := filepath.Dir(filepath.Dir(git))
	usrBin := filepath.Join(root, "usr", "bin")
	bin := filepath.Join(root, "bin")
	if bash := filepath.Join(usrBin, "bash.exe"); isExecutable(bash) {
		return bash, existingDirs(usrBin, bin), true
	}
	if bash := filepath.Join(bin, "bash.exe"); isExecutable(bash) {
		return bash, existingDirs(usrBin, bin), true
	}
	return "", nil, false
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

func existingDirs(dirs ...string) []string {
	out := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		if st, err := os.Stat(dir); err == nil && st.IsDir() {
			out = append(out, dir)
		}
	}
	return out
}

type bashCandidate struct {
	shell         string
	preferredDirs []string
}

func resolveBashCandidates(searchPath string, candidates []bashCandidate, names ...string) (string, []string, error) {
	var firstErr error
	for _, candidate := range candidates {
		if !isExecutable(candidate.shell) {
			continue
		}
		dirs, err := completeBashDirs(searchPath, candidate.shell, candidate.preferredDirs, names...)
		if err == nil {
			return candidate.shell, dirs, nil
		}
		if firstErr == nil {
			firstErr = fmt.Errorf("bash candidate %s: %w", candidate.shell, err)
		}
	}
	if firstErr != nil {
		return "", nil, firstErr
	}
	return "", nil, fmt.Errorf("no executable bash candidate on PATH=%s: %w", searchPath, errors.ErrUnsupported)
}

func completeBashDirs(searchPath, shell string, preferred []string, names ...string) ([]string, error) {
	dirs := appendUnique(nil, existingDirs(preferred...)...)
	dirs = appendUnique(dirs, filepath.Dir(shell))
	lookupPath := strings.Join(append(append([]string{}, dirs...), filepath.SplitList(searchPath)...), string(os.PathListSeparator))
	for _, name := range names {
		path := findOnPath(lookupPath, name)
		if path == "" {
			return nil, fmt.Errorf("bash fixture requires %s on PATH=%s: %w", name, searchPath, errors.ErrUnsupported)
		}
		dirs = appendUnique(dirs, filepath.Dir(path))
	}
	for _, name := range names {
		if findOnPath(strings.Join(dirs, string(os.PathListSeparator)), name) == "" {
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

// POSIXShell returns the absolute path of a real POSIX interpreter for
// portable fake executable and sidecar fixtures, failing the test if the
// machine has none. Bash-dependent launch fixtures should use BashShell and
// BashShellDirs instead, because those helpers also guarantee the required
// support utilities are resolvable from the fixture PATH.
func POSIXShell(t *testing.T) string {
	t.Helper()
	shell, err := posixShellPath()
	if err != nil {
		t.Fatalf("resolve POSIX shell: %v", err)
	}
	return shell
}

// POSIXShellDir returns the directory to place on PATH so that a POSIX shell
// resolves by name. It is the portable stand-in for "/bin" in a PATH fixture.
func POSIXShellDir(t *testing.T) string {
	t.Helper()
	return filepath.Dir(POSIXShell(t))
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
// resolve bash, cat, and mkdir while preserving preferred support-directory
// order.
func BashShellDirs(t *testing.T) []string {
	t.Helper()
	_, dirs, err := resolveBashShell(bootPath)
	if err != nil {
		t.Fatalf("resolve bash shell: %v", err)
	}
	return dirs
}
