//go:build windows

package orchestrator

import (
	"errors"

	"golang.org/x/sys/windows"
)

// isProcessAlive checks whether a process with the given PID is running.
//
// Windows has no `kill`, so the unix half's signal probe cannot be used. This
// follows the pattern already in the tree at internal/cli/watch_process_windows.go:
// open a handle, ask for the exit code, and treat STILL_ACTIVE (259) as alive.
//
// The answer is fail-closed on the same terms as the unix half (#580). Every
// caller reads false as permission to act -- the watcher lock policy deletes a
// live watcher's lock file (home.acquireWatcherLock), the AFK identity lock is
// reclaimed (afk_lock.go), and the exit waits conclude the process is gone --
// so only a positively observed absence may answer false. OpenProcess reports
// ERROR_INVALID_PARAMETER for a PID that does not exist; every other failure,
// ERROR_ACCESS_DENIED above all, means a process we could not inspect rather
// than one that is gone, and so does a GetExitCodeProcess that fails on a
// handle we did open.
//
// No lane runs this. `GOOS=windows go vet ./...` and the native windows gate
// compile it, and its answer for a real live process stays unproven in this
// repository -- including which errno OpenProcess actually returns for an
// unopenable live PID.
//
// process_alive_windows_test.go is not what holds the signature: this function
// has production call sites that compile in the same lane (afk_lock.go,
// afk_return.go, lifecycle_lifecycle.go), so a drifting signature turns the lane
// red at those before it reaches any test file. Do not read that test as
// coverage for the behaviour above.
func isProcessAlive(pid int) bool {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return !errors.Is(err, windows.ERROR_INVALID_PARAMETER)
	}
	defer windows.CloseHandle(handle)
	var code uint32
	if err := windows.GetExitCodeProcess(handle, &code); err != nil {
		return true
	}
	return code == 259
}
