//go:build !windows

package testutil

import (
	"errors"
	"fmt"
)

func installWindowsFake(string) error { return nil }

func fakeExecutablePath(path string) string { return path }

// posixShellPath prefers the well-known location over a PATH search, because
// callers put this shell's directory on a fixture PATH and expect the standard
// utilities a launch script uses to come with it. Resolving to a shell that
// ships alone — a homebrew bash, say — would hand back a directory holding no
// cat, no mkdir, and the script would fail for a reason unrelated to the test.
// The same reasoning picks Git for Windows' usr\bin on the other platform.
func posixShellPath() (string, error) {
	for _, p := range []string{"/bin/sh", "/usr/bin/sh", "/bin/bash"} {
		if isFile(p) {
			return p, nil
		}
	}
	if p := findOnPath(bootPath, "sh", "bash"); p != "" {
		return p, nil
	}
	return "", fmt.Errorf("no POSIX shell on PATH=%s: %w", bootPath, errors.ErrUnsupported)
}

func bashShellPath() (string, error) {
	for _, p := range []string{"/bin/bash", "/usr/bin/bash"} {
		if isFile(p) {
			return p, nil
		}
	}
	if p := findOnPath(bootPath, "bash"); p != "" {
		return p, nil
	}
	return "", fmt.Errorf("no bash shell on PATH=%s: %w", bootPath, errors.ErrUnsupported)
}

// userHomeEnv is the variable os.UserHomeDir reads on this platform.
const userHomeEnv = "HOME"
