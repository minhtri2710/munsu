//go:build windows

package orchestrator

import "golang.org/x/sys/windows"

// isProcessAlive checks whether a process with the given PID is running.
//
// Windows has no `kill`, so the unix half's shell probe cannot be used: it
// would fail for every PID and report every process dead. Both callers read
// that answer as permission — the watcher lock policy deletes a live watcher's
// lock file (home.acquireWatcherLock) and the AFK identity lock is reclaimed
// (afk_lock.go) — so a blind implementation here fails open on singleton, which
// is worse than having no guard. This follows the pattern already in the tree at
// internal/cli/watch_process_windows.go: open a handle, ask for the exit code,
// and treat STILL_ACTIVE (259) as alive. A PID we cannot open is reported dead.
//
// No lane runs this. `GOOS=windows go vet ./...` compiles it, and its answer for
// a real live process stays unproven in this repository.
//
// process_alive_windows_test.go is not what holds the signature: this function
// has production call sites that compile in the same lane (afk_lock.go,
// afk_return.go, lifecycle_lifecycle.go), so a drifting signature turns the lane
// red at those before it reaches any test file. Do not read that test as
// coverage for the behaviour above.
func isProcessAlive(pid int) bool {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(handle)
	var code uint32
	return windows.GetExitCodeProcess(handle, &code) == nil && code == 259
}
