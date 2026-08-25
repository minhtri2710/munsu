package testutil

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

// bootPath is PATH as it stood before any test rewrote it. Windows shim
// resolution reads this instead of the live PATH so that installing one fake
// cannot change how the next fake is built: t.Setenv("PATH", ...) is the
// normal way these fixtures work, and a resolution that depended on it would
// be order-dependent.
var bootPath = os.Getenv("PATH")

// WriteFakeExecutable writes script as a POSIX shell program named name in dir
// and returns the path it wrote.
//
// On windows it also writes a name.cmd companion that hands the same script to
// a POSIX shell. exec.LookPath on windows only considers names that carry a
// PATHEXT extension (.COM;.EXE;.BAT;.CMD;...), so a bare `#!/bin/sh` file named
// `herdr` is invisible to it no matter what its mode bits say: production
// reports the binary as absent and the test measures a lookup failure rather
// than the contract it meant to exercise (#549 group 1). LookPath appends each
// PATHEXT entry to the whole name, so `herdr.cmd` answers a lookup for `herdr`
// and the shell script stays the single source of the fake's behaviour on both
// platforms.
func WriteFakeExecutable(t *testing.T, dir, name, script string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("fake %s: %v", name, err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("fake %s: %v", name, err)
	}
	if runtime.GOOS != "windows" {
		return path
	}
	shim := "@echo off\r\n\"" + posixShell(t) + "\" \"%~dp0" + name + "\" %*\r\nexit /b %errorlevel%\r\n"
	if err := os.WriteFile(path+".cmd", []byte(shim), 0o755); err != nil {
		t.Fatalf("fake %s: %v", name, err)
	}
	return path
}

// FakeOnPath installs one fake program in a fresh directory and prepends that
// directory to PATH for the duration of the test.
func FakeOnPath(t *testing.T, name, script string) string {
	t.Helper()
	dir := t.TempDir()
	path := WriteFakeExecutable(t, dir, name, script)
	PrependPath(t, dir)
	return path
}

// PrependPath puts dir first on PATH for the duration of the test. Fixtures
// prepend rather than replace: on windows a replaced PATH loses the system
// directories that the shim's own shell and cmd.exe are found through.
func PrependPath(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

var shellOnce struct {
	sync.Once
	path string
}

// posixShell resolves the interpreter the windows shim hands its script to. It
// prefers a shell already on the boot PATH and otherwise derives one from git's
// own install root -- every fixture that installs a fake here also drives git,
// and Git for Windows ships the same shell. It fails the test rather than
// skipping it: a missing shell means the fake never ran, which is a fact about
// the machine, not a behaviour that windows lacks.
func posixShell(t *testing.T) string {
	t.Helper()
	shellOnce.Do(func() {
		if p := findOnBootPath("sh.exe", "bash.exe"); p != "" {
			shellOnce.path = p
			return
		}
		git := findOnBootPath("git.exe")
		if git == "" {
			return
		}
		root := filepath.Dir(filepath.Dir(git)) // ...\Git\cmd or ...\Git\bin -> ...\Git
		for _, rel := range []string{`usr\bin\sh.exe`, `bin\sh.exe`, `usr\bin\bash.exe`, `bin\bash.exe`} {
			if p := filepath.Join(root, rel); isFile(p) {
				shellOnce.path = p
				return
			}
		}
	})
	if shellOnce.path == "" {
		t.Fatalf("no POSIX shell found for the windows fake-binary shim; PATH=%s", bootPath)
	}
	return shellOnce.path
}

func findOnBootPath(names ...string) string {
	for _, dir := range filepath.SplitList(bootPath) {
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
