//go:build windows

package home

import "golang.org/x/sys/windows"

// isProcessAlive checks whether a process with the given PID is running.
//
// Windows has no `kill`, so the unix half's shell probe cannot be used: it
// would fail for every PID and report every holder dead, which makes
// ClaimWatcherLease grant the lease unconditionally — the lease layer would
// stop being a singleton guard while still reading like one. This follows the
// pattern already in the tree at internal/cli/watch_process_windows.go: open a
// handle, ask for the exit code, and treat STILL_ACTIVE (259) as alive. A PID
// we cannot open is reported dead.
//
// No lane runs this. `GOOS=windows go vet ./...` compiles it, and
// watcher_lease_windows_test.go binds it at the shape its callers use; its
// answer for a real live process stays unproven in this repository.
func isProcessAlive(pid int) bool {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(handle)
	var code uint32
	return windows.GetExitCodeProcess(handle, &code) == nil && code == 259
}
