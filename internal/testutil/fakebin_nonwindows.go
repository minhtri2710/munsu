//go:build !windows

package testutil

import (
	"os"
)

func installWindowsFake(string) error { return nil }

func isExecutable(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular() && st.Mode()&0111 != 0
}

func fakeExecutablePath(path string) string { return path }

func resolveBashShell(searchPath string) (string, []string, error) {
	candidates := []bashCandidate{
		{shell: "/bin/bash"},
		{shell: "/usr/bin/bash"},
	}
	if p := findOnPath(searchPath, "bash"); p != "" {
		candidates = append(candidates, bashCandidate{shell: p})
	}
	return resolveBashCandidates(searchPath, candidates, "bash", "cat", "mkdir")
}

// userHomeEnv is the variable os.UserHomeDir reads on this platform.
const userHomeEnv = "HOME"
