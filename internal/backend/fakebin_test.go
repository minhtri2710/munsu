//go:build integration

package backend

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeCmd declares what a fake CLI does: what it prints, what it exits with,
// and where it records the arguments it was called with.
type fakeCmd struct {
	stdout   string // one line on stdout; empty prints nothing
	exitCode int
	argsFile string // when set, each argument is written on its own line
}

// fakeTreehouseOnPath installs a fake `treehouse` in a fresh directory and
// prepends that directory to PATH for the duration of the test.
//
// The fake is emitted in the host's own script language rather than as a
// `#!/bin/sh` file. exec.LookPath on windows only considers names carrying a
// PATHEXT extension, so a bare `treehouse` shell script is invisible there:
// selectProvider finds no treehouse, silently falls back to real
// `git worktree`, and the test then measures git rather than the fake it
// installed (#549 group 8). Declaring the behaviour instead of scripting it is
// what keeps the two languages saying the same thing.
func fakeTreehouseOnPath(t *testing.T, spec fakeCmd) {
	t.Helper()
	dir := t.TempDir()
	name, body := "treehouse", unixFake(spec)
	if runtime.GOOS == "windows" {
		name, body = "treehouse.cmd", windowsFake(spec)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func unixFake(spec fakeCmd) string {
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	if spec.argsFile != "" {
		fmt.Fprintf(&b, ": > %q\nfor a in \"$@\"; do printf '%%s\\n' \"$a\" >> %q; done\n", spec.argsFile, spec.argsFile)
	}
	if spec.stdout != "" {
		fmt.Fprintf(&b, "printf '%%s\\n' %q\n", spec.stdout)
	}
	fmt.Fprintf(&b, "exit %d\n", spec.exitCode)
	return b.String()
}

func windowsFake(spec fakeCmd) string {
	var b strings.Builder
	b.WriteString("@echo off\r\n")
	if spec.argsFile != "" {
		fmt.Fprintf(&b, "type nul > %q\r\n:munsu_arg\r\nif \"%%~1\"==\"\" goto munsu_args_done\r\n>> %q echo %%~1\r\nshift\r\ngoto munsu_arg\r\n:munsu_args_done\r\n", spec.argsFile, spec.argsFile)
	}
	if spec.stdout != "" {
		fmt.Fprintf(&b, "echo %s\r\n", spec.stdout)
	}
	fmt.Fprintf(&b, "exit /b %d\r\n", spec.exitCode)
	return b.String()
}
